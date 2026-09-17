# agentkit

A tiny, dependency-free tool-calling agent loop for Go — the whole
`think → act → observe` cycle in ~500 lines of stdlib-only code, with a
terminal UI that doesn't look like a `fmt.Println` afterthought.

```
provider: MockProvider (offline demo, no API key found)
tools:    calculator, get_weather, convert_currency, current_time, notes

▸ you: What's the weather in Tokyo, and convert 100 USD to EUR?

┌─ step 1 ─────────────────────────────────────
✔ model responded (312ms)
✔ get_weather → Tokyo, Japan: 14.2°C, partly cloudy, wind 11 km/h (live, via Open-Meteo) (420ms)
✔ convert_currency → 100.00 USD = 92.59 EUR (live rate as of 2026-09-18: 1 USD = 0.9259 EUR) (180ms)
┌─ step 2 ─────────────────────────────────────
✔ model responded (208ms)

╭─ answer ──────────────────────────────────────────────────────────────╮
│ Here's what I found:                                                  │
│ - Tokyo, Japan: 14.2°C, partly cloudy, wind 11 km/h (live, via ...)   │
│ - 100.00 USD = 92.59 EUR (live rate as of 2026-09-18: 1 USD = ...)    │
╰──────────────────────────────────────────────────────────────────────╯
```

## Why this exists

Most "hello world" agent examples are 40 lines that call one tool once.
This one is small enough to read in five minutes but has the pieces an
actual agent needs:

- **Real tool-calling loop** against the Anthropic Messages API — or a
  fully offline `MockProvider` so the whole thing runs with zero API key
  and zero network access.
- **Concurrent tool execution.** If the model asks for three tools in one
  turn, they run in three goroutines, not three sequential round-trips.
- **Retries with backoff + jitter** on the provider call, distinguishing
  retryable failures (5xx, 429, network blips) from ones that won't fix
  themselves (bad key, malformed request).
- **Multi-turn memory** — the REPL keeps conversation history across
  questions, so "what about in Celsius" actually has something to refer to.
- **A live terminal UI**: animated spinners per step and per tool call,
  a boxed final answer, a post-run trace table — built with ~120 lines of
  raw ANSI codes, no TUI library required.
- **Five real tools**, all backed by genuine data — live weather and
  live exchange rates from free public APIs (no key needed), real system
  time in any timezone, and a stateful scratch-notes store — so it's
  obvious how to add your own.

## Run it

No setup required — it runs fully offline out of the box:

```bash
go run . "What's 12 to the power of 5?"
```

Or drop into interactive mode (multi-turn, with `reset`/`trace`/`exit`):

```bash
go run .
```

To hit the real Claude API instead of the offline mock, set your key:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
go run . "What's the weather in Tokyo and what's the sqrt of 2?"
```

## Project layout

```
types.go     Message / ContentBlock / Tool / Provider — the core contracts
agent.go     the think → act → observe loop, concurrent tool execution
tools.go     calculator, get_weather, convert_currency, current_time, notes
provider.go  AnthropicProvider (real API + retries) and MockProvider (offline)
ui.go        spinner, boxed output, ANSI colors — zero external deps
main.go      CLI: one-shot mode + interactive REPL
```

## Adding your own tool

Implement four methods and register it in `buildTools()` in `main.go`:

```go
type myTool struct{}

func (myTool) Name() string        { return "my_tool" }
func (myTool) Description() string { return "what it does, for the model" }
func (myTool) InputSchema() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "query": map[string]any{"type": "string"},
        },
        "required": []string{"query"},
    }
}
func (myTool) Execute(input json.RawMessage) (string, error) {
    var args struct{ Query string `json:"query"` }
    if err := json.Unmarshal(input, &args); err != nil {
        return "", err
    }
    return "result for " + args.Query, nil
}
```

That's it — the agent loop, retries, concurrency, and UI all come for free.

## Live data, no API keys

`get_weather` and `convert_currency` both fetch real, current data from
free public APIs at call time — [Open-Meteo](https://open-meteo.com) for
weather (geocodes the city, then pulls current conditions) and
[Frankfurter](https://frankfurter.dev) for exchange rates (mirrors
European Central Bank reference rates). Neither needs an API key or
signup, so the project stays zero-setup while still being accurate.
Both need an internet connection, though — including when you're
otherwise running fully offline with `MockProvider`, since that only
decides *when* to call a tool, never fakes what the tool returns.
`current_time` is likewise genuine: it reads your system clock through
Go's IANA timezone database, not a canned value.
