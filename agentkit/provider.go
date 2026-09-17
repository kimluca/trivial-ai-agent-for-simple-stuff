package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ── real provider ────────────────────────────────────────────────────────

type AnthropicProvider struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
	MaxRetries int
}

func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		APIKey:     apiKey,
		Model:      "claude-sonnet-4-6",
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		MaxRetries: 3,
	}
}

func (p *AnthropicProvider) Complete(messages []Message, tools []Tool) (Message, error) {
	body := map[string]any{
		"model":      p.Model,
		"max_tokens": 1024,
		"tools":      anthropicToolDefs(tools),
		"messages":   anthropicMessages(messages),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Message{}, fmt.Errorf("encode request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= p.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 300 * time.Millisecond
			backoff += time.Duration(rand.Intn(150)) * time.Millisecond // jitter, avoids thundering herd on retries
			time.Sleep(backoff)
		}

		msg, retryable, err := p.doRequest(raw)
		if err == nil {
			return msg, nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return Message{}, fmt.Errorf("after %d attempt(s): %w", p.MaxRetries+1, lastErr)
}

// doRequest performs one HTTP round trip. The bool return says whether the
// caller should retry (true for timeouts/5xx/429, false for anything that
// won't be fixed by trying again, like a bad API key).
func (p *AnthropicProvider) doRequest(raw []byte) (Message, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return Message{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return Message{}, true, err // network hiccup — worth a retry
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, true, err
	}

	retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500

	var parsed struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return Message{}, retryable, fmt.Errorf("decode response (status %d): %w", resp.StatusCode, err)
	}
	if parsed.Error != nil {
		return Message{}, retryable, fmt.Errorf("anthropic error (status %d): %s", resp.StatusCode, parsed.Error.Message)
	}

	var blocks []ContentBlock
	for _, c := range parsed.Content {
		switch c.Type {
		case "text":
			blocks = append(blocks, ContentBlock{Text: c.Text})
		case "tool_use":
			blocks = append(blocks, ContentBlock{ToolUse: &ToolUseBlock{ID: c.ID, Name: c.Name, Input: c.Input}})
		}
	}
	return Message{Role: "assistant", Content: blocks}, false, nil
}

func anthropicToolDefs(tools []Tool) []map[string]any {
	defs := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, map[string]any{
			"name":         t.Name(),
			"description":  t.Description(),
			"input_schema": t.InputSchema(),
		})
	}
	return defs
}

func anthropicMessages(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		var content []map[string]any
		for _, b := range m.Content {
			switch {
			case b.Text != "":
				content = append(content, map[string]any{"type": "text", "text": b.Text})
			case b.ToolUse != nil:
				content = append(content, map[string]any{
					"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": b.ToolUse.Input,
				})
			case b.ToolResult != nil:
				content = append(content, map[string]any{
					"type": "tool_result", "tool_use_id": b.ToolResult.ToolUseID,
					"content": b.ToolResult.Content, "is_error": b.ToolResult.IsError,
				})
			}
		}
		out = append(out, map[string]any{"role": m.Role, "content": content})
	}
	return out
}

// ── mock provider ────────────────────────────────────────────────────────
// A small offline stand-in so the whole project runs with zero API key
// and zero network access. It is NOT a language model — it can't really
// understand arbitrary questions. Instead it recognizes a handful of
// patterns (arithmetic, currency, weather, time) with regexes and fires
// the matching tool call(s), then summarizes whatever comes back. Ask it
// something outside those patterns and it says so plainly, rather than
// silently ignoring your question and repeating a canned demo answer.
// Point ANTHROPIC_API_KEY at a real key to get genuine understanding.

type MockProvider struct{}

func (m *MockProvider) Complete(messages []Message, tools []Tool) (Message, error) {
	last := messages[len(messages)-1]

	var results []*ToolResultBlock
	for _, b := range last.Content {
		if b.ToolResult != nil {
			results = append(results, b.ToolResult)
		}
	}

	if len(results) > 0 {
		summary := "Here's what I found:\n"
		for _, r := range results {
			if r.IsError {
				summary += fmt.Sprintf("- (tool call failed: %s)\n", r.Content)
				continue
			}
			summary += fmt.Sprintf("- %s\n", r.Content)
		}
		return Message{Role: "assistant", Content: []ContentBlock{{Text: summary}}}, nil
	}

	// No tool results yet, so this is a fresh question — try to figure
	// out what's being asked for from the raw text.
	var question string
	for _, b := range last.Content {
		if b.Text != "" {
			question = b.Text
			break
		}
	}

	calls := parseMockIntent(question)
	if len(calls) == 0 {
		return Message{Role: "assistant", Content: []ContentBlock{{Text: mockFallbackText}}}, nil
	}
	blocks := make([]ContentBlock, len(calls))
	for i, c := range calls {
		blocks[i] = ContentBlock{ToolUse: c}
	}
	return Message{Role: "assistant", Content: blocks}, nil
}

const mockFallbackText = `I'm the offline MockProvider — a small scripted stand-in, not a real model, so I only recognize a few patterns:
  • arithmetic, e.g. "12 * 7", "2 hoch 5", "sqrt of 16"
  • currency, e.g. "100 USD to EUR"
  • weather, e.g. "weather in Tokyo" / "wetter in Berlin"
  • time, e.g. "time in Berlin" (known cities: Tokyo, Berlin, London, Paris, New York)

For real understanding of any question, set ANTHROPIC_API_KEY and rerun — that switches this project over to the live Claude API.`

var (
	mockPowerRe    = regexp.MustCompile(`(?i)(-?\d+(?:\.\d+)?)\s*(?:\^|hoch|\*\*)\s*(-?\d+(?:\.\d+)?)`)
	mockSqrtRe     = regexp.MustCompile(`(?i)(?:sqrt|wurzel)\s*(?:von|aus|of)?\s*\(?(-?\d+(?:\.\d+)?)\)?`)
	mockArithRe    = regexp.MustCompile(`(-?\d+(?:\.\d+)?)\s*([+\-*x×/])\s*(-?\d+(?:\.\d+)?)`)
	mockCurrencyRe = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(usd|eur|gbp|jpy|chf)\s*(?:to|in|nach|->)\s*(usd|eur|gbp|jpy|chf)`)
	mockWeatherRe  = regexp.MustCompile(`(?i)(?:weather|wetter)\S{0,3}\s*(?:in|for|für)?\s+([A-Za-zÀ-ÖØ-öø-ÿ]+)`)
	mockTimeRe     = regexp.MustCompile(`(?i)(?:time|uhrzeit|zeit|spät ist es)\S{0,3}\s*(?:in|für)?\s+([A-Za-zÀ-ÖØ-öø-ÿ]+)`)
)

// mockCityTZ maps a few well-known city names to IANA timezones so
// "time in Berlin" can actually call the current_time tool correctly.
// It's intentionally small — this is a demo heuristic, not a geocoder.
var mockCityTZ = map[string]string{
	"tokyo": "Asia/Tokyo", "berlin": "Europe/Berlin", "london": "Europe/London",
	"paris": "Europe/Paris", "newyork": "America/New_York", "essen": "Europe/Berlin",
}

func parseMockIntent(q string) []*ToolUseBlock {
	var calls []*ToolUseBlock
	next := 1
	add := func(name string, input any) {
		raw, _ := json.Marshal(input)
		calls = append(calls, &ToolUseBlock{ID: fmt.Sprintf("call_%d", next), Name: name, Input: raw})
		next++
	}

	switch {
	case mockPowerRe.MatchString(q):
		m := mockPowerRe.FindStringSubmatch(q)
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		add("calculator", map[string]any{"operation": "power", "a": a, "b": b})
	case mockSqrtRe.MatchString(q):
		m := mockSqrtRe.FindStringSubmatch(q)
		a, _ := strconv.ParseFloat(m[1], 64)
		add("calculator", map[string]any{"operation": "sqrt", "a": a})
	case mockArithRe.MatchString(q):
		m := mockArithRe.FindStringSubmatch(q)
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[3], 64)
		opNames := map[string]string{"+": "add", "-": "subtract", "*": "multiply", "x": "multiply", "×": "multiply", "/": "divide"}
		add("calculator", map[string]any{"operation": opNames[strings.ToLower(m[2])], "a": a, "b": b})
	}

	if m := mockCurrencyRe.FindStringSubmatch(q); m != nil {
		amount, _ := strconv.ParseFloat(m[1], 64)
		add("convert_currency", map[string]any{"amount": amount, "from": strings.ToUpper(m[2]), "to": strings.ToUpper(m[3])})
	}

	if m := mockWeatherRe.FindStringSubmatch(q); m != nil {
		add("get_weather", map[string]any{"city": m[1]})
	}

	if m := mockTimeRe.FindStringSubmatch(q); m != nil {
		key := strings.ToLower(strings.ReplaceAll(m[1], " ", ""))
		if tz, ok := mockCityTZ[key]; ok {
			add("current_time", map[string]any{"timezone": tz})
		}
	}

	return calls
}
