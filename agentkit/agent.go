package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// TraceEntry records one tool invocation for later inspection — handy for
// logging, evals, or just printing a post-mortem of what the agent did.
type TraceEntry struct {
	Step     int
	Tool     string
	Input    string
	Output   string
	IsError  bool
	Duration time.Duration
}

type Agent struct {
	Provider Provider
	Tools    []Tool
	MaxSteps int

	// Verbose turns on the live spinner/box UI. Set false for silent,
	// library-style use (e.g. embedding the agent in another program).
	Verbose bool

	// history persists across calls to Ask, so a REPL session behaves
	// like an actual conversation instead of forgetting everything
	// between questions.
	history []Message
}

func NewAgent(p Provider, tools []Tool) *Agent {
	return &Agent{Provider: p, Tools: tools, MaxSteps: 8, Verbose: true}
}

// Reset clears conversation memory, starting the next Ask() fresh.
func (a *Agent) Reset() { a.history = nil }

func (a *Agent) toolByName(name string) Tool {
	for _, t := range a.Tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// Run is a stateless convenience wrapper: it starts a brand new
// conversation containing only userMessage. Use this for one-shot CLI
// invocations or tests where a clean slate matters.
func (a *Agent) Run(userMessage string) (answer string, trace []TraceEntry, err error) {
	a.history = nil
	return a.Ask(userMessage)
}

// Ask drives the think → act → observe loop until the model responds with
// plain text (no further tool calls) or MaxSteps is exhausted, appending
// to the agent's persistent conversation history so follow-up questions
// carry context (used by the REPL in main.go). When a single turn
// requests multiple tools, they're executed concurrently — there's no
// reason to make the user wait for three independent API calls to run
// back-to-back.
func (a *Agent) Ask(userMessage string) (answer string, trace []TraceEntry, err error) {
	messages := append(a.history, Message{Role: "user", Content: []ContentBlock{{Text: userMessage}}})

	if a.Verbose {
		fmt.Printf("%s%s▸ you:%s %s\n\n", bold, blue, reset, userMessage)
	}

	for step := 1; step <= a.MaxSteps; step++ {
		var sp *spinner
		if a.Verbose {
			fmt.Println(stepHeader(step))
			sp = newSpinner("thinking…")
			sp.Start()
		}

		reply, err := a.Provider.Complete(messages, a.Tools)

		if sp != nil {
			sp.Stop(err == nil, "model responded")
		}
		if err != nil {
			a.history = messages // keep whatever context we had before the failure
			return "", trace, fmt.Errorf("step %d: provider error: %w", step, err)
		}
		messages = append(messages, reply)

		var toolCalls []*ToolUseBlock
		var textParts []string
		for _, block := range reply.Content {
			if block.ToolUse != nil {
				toolCalls = append(toolCalls, block.ToolUse)
			}
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		}

		if len(toolCalls) == 0 {
			final := strings.TrimSpace(strings.Join(textParts, "\n"))
			a.history = messages
			if a.Verbose {
				fmt.Println()
				box("answer", final, green)
			}
			return final, trace, nil
		}

		results, entries := a.runToolsConcurrently(step, toolCalls)
		trace = append(trace, entries...)
		messages = append(messages, Message{Role: "user", Content: results})
	}

	a.history = messages
	return "", trace, fmt.Errorf("exceeded max steps (%d) without a final answer", a.MaxSteps)
}

// runToolsConcurrently executes every tool call requested in one turn in
// its own goroutine and waits for all of them. Results are re-assembled
// in the original call order so the transcript stays deterministic even
// though the work happened in parallel.
func (a *Agent) runToolsConcurrently(step int, calls []*ToolUseBlock) ([]ContentBlock, []TraceEntry) {
	results := make([]ContentBlock, len(calls))
	entries := make([]TraceEntry, len(calls))
	var wg sync.WaitGroup
	var printMu sync.Mutex

	for i, call := range calls {
		wg.Add(1)
		go func(i int, call *ToolUseBlock) {
			defer wg.Done()

			var sp *spinner
			if a.Verbose {
				printMu.Lock()
				sp = newSpinner(fmt.Sprintf("%s(%s)", call.Name, compactJSON(call.Input)))
				sp.Start()
				printMu.Unlock()
			}

			start := time.Now()
			tool := a.toolByName(call.Name)
			var output string
			var callErr error
			if tool == nil {
				callErr = fmt.Errorf("no such tool: %s", call.Name)
			} else {
				output, callErr = tool.Execute(call.Input)
			}
			elapsed := time.Since(start)

			isError := callErr != nil
			resultText := output
			if callErr != nil {
				resultText = callErr.Error()
			}

			if sp != nil {
				printMu.Lock()
				label := fmt.Sprintf("%s → %s", call.Name, truncate(resultText, 70))
				sp.Stop(!isError, label)
				printMu.Unlock()
			}

			results[i] = ContentBlock{ToolResult: &ToolResultBlock{
				ToolUseID: call.ID, Content: resultText, IsError: isError,
			}}
			entries[i] = TraceEntry{
				Step: step, Tool: call.Name, Input: compactJSON(call.Input),
				Output: resultText, IsError: isError, Duration: elapsed,
			}
		}(i, call)
	}

	wg.Wait()
	return results, entries
}

func compactJSON(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Summary renders the collected trace as a compact table — handy to print
// once at the end of a run, or to log/ship somewhere for observability.
func Summary(trace []TraceEntry) string {
	if len(trace) == 0 {
		return gray + "(no tool calls were made)" + reset
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s%s%-6s %-20s %-10s %s%s\n", bold, gray, "step", "tool", "time", "result", reset))
	for _, e := range trace {
		status := green + "ok" + reset
		if e.IsError {
			status = red + "err" + reset
		}
		b.WriteString(fmt.Sprintf("%-6d %-20s %-10s %s %s\n", e.Step, e.Tool, e.Duration.Round(time.Millisecond), status, truncate(e.Output, 60)))
	}
	return b.String()
}
