// Command agentkit is a small, dependency-free tool-calling agent loop.
//
// Run it with a question as arguments for a one-shot answer:
//
//	go run . "What's 12 to the power of 5?"
//
// Or with no arguments to drop into an interactive, multi-turn REPL:
//
//	go run .
//
// Set ANTHROPIC_API_KEY to use the real Claude API. Without it, the demo
// runs fully offline against a small deterministic MockProvider so you
// can see the whole loop — parallel tool calls, retries, live trace UI —
// with zero setup.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func buildTools() []Tool {
	return []Tool{
		calculatorTool{},
		newWeatherTool(),
		newCurrencyTool(),
		timeTool{},
		newNotesTool(),
	}
}

func buildProvider() (Provider, string) {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return NewAnthropicProvider(key), "AnthropicProvider (live Claude API)"
	}
	return &MockProvider{}, "MockProvider (offline demo, no API key found)"
}

func main() {
	fmt.Print(banner())

	provider, providerLabel := buildProvider()
	fmt.Printf("%sprovider:%s %s\n", gray, reset, providerLabel)
	fmt.Printf("%stools:   %s calculator, get_weather, convert_currency, current_time, notes\n\n", gray, reset)

	agent := NewAgent(provider, buildTools())

	args := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if args != "" {
		runOnce(agent, args)
		return
	}
	repl(agent)
}

func runOnce(agent *Agent, question string) {
	_, trace, err := agent.Run(question)
	if err != nil {
		fmt.Printf("\n%s%s✘ error:%s %v\n", bold, red, reset, err)
		os.Exit(1)
	}
	if len(trace) > 0 {
		fmt.Printf("\n%s%s── trace ──%s\n%s\n", bold, gray, reset, Summary(trace))
	}
}

func repl(agent *Agent) {
	fmt.Printf("%sinteractive mode — type a question, or 'exit' / 'reset' / 'trace'%s\n\n", italic+gray, reset)
	scanner := bufio.NewScanner(os.Stdin)
	var lastTrace []TraceEntry

	for {
		fmt.Printf("%s%s❯%s ", bold, magenta, reset)
		if !scanner.Scan() {
			fmt.Println()
			return
		}
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "":
			continue
		case line == "exit" || line == "quit":
			fmt.Println(gray + "bye!" + reset)
			return
		case line == "reset":
			agent.Reset()
			lastTrace = nil
			fmt.Println(gray + "conversation memory cleared." + reset)
			continue
		case line == "trace":
			fmt.Println(Summary(lastTrace))
			continue
		}

		fmt.Println()
		_, trace, err := agent.Ask(line)
		if err != nil {
			fmt.Printf("%s%s✘ error:%s %v\n", bold, red, reset, err)
			continue
		}
		lastTrace = trace
		fmt.Println()
	}
}
