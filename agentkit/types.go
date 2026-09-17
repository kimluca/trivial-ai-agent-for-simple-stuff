package main

import "encoding/json"

// Tool is anything the agent can invoke mid-conversation. Implementations
// are expected to be side-effect-light and fast to call — long-running
// work should report partial progress via its own mechanism, not block
// here indefinitely, since the agent may run several tools concurrently.
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(input json.RawMessage) (string, error)
}

// Message mirrors the Anthropic Messages API shape closely enough that
// AnthropicProvider can translate 1:1, while staying provider-agnostic so
// MockProvider (or any future provider) can implement the same interface.
type Message struct {
	Role    string
	Content []ContentBlock
}

type ContentBlock struct {
	Text       string
	ToolUse    *ToolUseBlock
	ToolResult *ToolResultBlock
}

type ToolUseBlock struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type ToolResultBlock struct {
	ToolUseID string
	Content   string
	IsError   bool
}

// Provider is the seam between the agent loop and whatever actually
// generates completions — a real model API, a mock, a local model, a
// recorded fixture for tests, etc.
type Provider interface {
	Complete(messages []Message, tools []Tool) (Message, error)
}
