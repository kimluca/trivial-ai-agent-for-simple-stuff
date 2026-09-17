package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── calculator ───────────────────────────────────────────────────────────

type calculatorTool struct{}

func (calculatorTool) Name() string { return "calculator" }
func (calculatorTool) Description() string {
	return "Perform a single arithmetic operation: add, subtract, multiply, divide, power, or sqrt (sqrt only needs 'a')."
}
func (calculatorTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": []string{"add", "subtract", "multiply", "divide", "power", "sqrt"},
			},
			"a": map[string]any{"type": "number"},
			"b": map[string]any{"type": "number", "description": "not required for sqrt"},
		},
		"required": []string{"operation", "a"},
	}
}

func (calculatorTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		Operation string  `json:"operation"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid calculator input: %w", err)
	}
	var result float64
	switch args.Operation {
	case "add":
		result = args.A + args.B
	case "subtract":
		result = args.A - args.B
	case "multiply":
		result = args.A * args.B
	case "divide":
		if args.B == 0 {
			return "", fmt.Errorf("division by zero")
		}
		result = args.A / args.B
	case "power":
		result = math.Pow(args.A, args.B)
	case "sqrt":
		if args.A < 0 {
			return "", fmt.Errorf("cannot take sqrt of a negative number")
		}
		result = math.Sqrt(args.A)
	default:
		return "", fmt.Errorf("unknown operation %q", args.Operation)
	}
	return strconv.FormatFloat(result, 'f', -1, 64), nil
}

// ── weather (mock) ──────────────────────────────────────────────────────
// No network access here on purpose — this is a deterministic, offline
// stand-in so the whole project runs with zero API keys and zero
// external dependencies. Swap Execute's body for a real HTTP call to a
// weather API and everything else keeps working unchanged.

type weatherTool struct{}

func (weatherTool) Name() string { return "get_weather" }
func (weatherTool) Description() string {
	return "Get the current weather for a city (mock data, offline demo)."
}
func (weatherTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{"type": "string"},
		},
		"required": []string{"city"},
	}
}

func (weatherTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid weather input: %w", err)
	}
	if strings.TrimSpace(args.City) == "" {
		return "", fmt.Errorf("city must not be empty")
	}
	conditions := []string{"clear skies", "light rain", "overcast", "partly cloudy", "sunny", "windy"}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(args.City)))
	seed := h.Sum32()
	tempC := int(seed%28) - 5 // -5..22 °C
	cond := conditions[seed%uint32(len(conditions))]
	return fmt.Sprintf("%s: %d°C, %s (mock data)", args.City, tempC, cond), nil
}

// ── currency conversion (mock rates) ────────────────────────────────────

type currencyTool struct{}

var mockRatesToUSD = map[string]float64{
	"USD": 1,
	"EUR": 1.08,
	"GBP": 1.27,
	"JPY": 0.0067,
	"CHF": 1.11,
}

func (currencyTool) Name() string { return "convert_currency" }
func (currencyTool) Description() string {
	return "Convert an amount between currencies (mock fixed rates, offline demo). Supported: USD, EUR, GBP, JPY, CHF."
}
func (currencyTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"amount": map[string]any{"type": "number"},
			"from":   map[string]any{"type": "string"},
			"to":     map[string]any{"type": "string"},
		},
		"required": []string{"amount", "from", "to"},
	}
}

func (currencyTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		Amount float64 `json:"amount"`
		From   string  `json:"from"`
		To     string  `json:"to"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid currency input: %w", err)
	}
	from := strings.ToUpper(args.From)
	to := strings.ToUpper(args.To)
	fromRate, ok := mockRatesToUSD[from]
	if !ok {
		return "", fmt.Errorf("unsupported currency %q", from)
	}
	toRate, ok := mockRatesToUSD[to]
	if !ok {
		return "", fmt.Errorf("unsupported currency %q", to)
	}
	usd := args.Amount * fromRate
	converted := usd / toRate
	return fmt.Sprintf("%.2f %s = %.2f %s (mock rates)", args.Amount, from, converted, to), nil
}

// ── world time ───────────────────────────────────────────────────────────

type timeTool struct{}

func (timeTool) Name() string { return "current_time" }
func (timeTool) Description() string {
	return "Get the current date and time in an IANA timezone, e.g. 'Europe/Berlin' or 'America/New_York'."
}
func (timeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"timezone": map[string]any{"type": "string"},
		},
		"required": []string{"timezone"},
	}
}

func (timeTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid time input: %w", err)
	}
	loc, err := time.LoadLocation(args.Timezone)
	if err != nil {
		return "", fmt.Errorf("unknown timezone %q: %w", args.Timezone, err)
	}
	return time.Now().In(loc).Format("Mon, 02 Jan 2006 15:04:05 MST"), nil
}

// ── notes / scratch memory (stateful) ───────────────────────────────────
// Demonstrates a tool that carries state across calls within one agent
// run — useful once the model starts doing genuinely multi-step work
// (jot something down in step 1, read it back in step 4).

type notesTool struct {
	mu    sync.Mutex
	store map[string]string
}

func newNotesTool() *notesTool {
	return &notesTool{store: make(map[string]string)}
}

func (t *notesTool) Name() string { return "notes" }
func (t *notesTool) Description() string {
	return "Save or recall short scratch notes by key. action='set' requires key+value, action='get' requires key, action='list' returns all keys."
}
func (t *notesTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"set", "get", "list"}},
			"key":    map[string]any{"type": "string"},
			"value":  map[string]any{"type": "string"},
		},
		"required": []string{"action"},
	}
}

func (t *notesTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		Action string `json:"action"`
		Key    string `json:"key"`
		Value  string `json:"value"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid notes input: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch args.Action {
	case "set":
		if args.Key == "" {
			return "", fmt.Errorf("key must not be empty")
		}
		t.store[args.Key] = args.Value
		return fmt.Sprintf("saved %q", args.Key), nil
	case "get":
		v, ok := t.store[args.Key]
		if !ok {
			return "", fmt.Errorf("no note under key %q", args.Key)
		}
		return v, nil
	case "list":
		keys := make([]string, 0, len(t.store))
		for k := range t.store {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return "(no notes yet)", nil
		}
		return strings.Join(keys, ", "), nil
	default:
		return "", fmt.Errorf("unknown action %q", args.Action)
	}
}
