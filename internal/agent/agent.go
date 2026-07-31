package agent

import (
	"context"
	"encoding/json"
)

type Tool struct {
	Name             string
	Description      string
	InputSchema      InputSchema
	RequiresApproval bool
	Execute          func(ctx context.Context, input json.RawMessage) (string, error)
}

// Property is the typified struct of the JSON Schema for the Tool Schema's Property type
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// InputSchema is the struct matching the JSON Schema for the Tool Input
type InputSchema struct {
	Type       string `json:"type" default:"object"`
	Properties map[string]Property
	Required   []string // Names of required properties TODO: Track in Property struct instead?
}

type Agent struct {
	toolsMap      map[string]Tool
	tools         []Tool
	contextWindow []Message
	maxTokens     int64
	provider      Provider
}

func (a *Agent) UseTool(ctx context.Context, name string, input json.RawMessage) (string, error) {
	tool := a.toolsMap[name]
	return tool.Execute(ctx, input)
}

func (a *Agent) ProcessTurn(ctx context.Context) (Response, error) {
	return a.provider.Process(ctx,
		Request{
			MaxTokens: a.maxTokens, // TODO: set dynamically from API query if not set explicitly, requires streaming if set sufficiently high
			Tools:     a.tools,
			Messages:  a.contextWindow,
		},
	)
}

func (a *Agent) AppendMessage(msg Message) {
}

func New(provider Provider, tools []Tool) Agent {
	// TODO: Use functional options

	toolMap := map[string]Tool{}
	for _, tool := range tools {
		toolMap[tool.Name] = tool
	}

	return Agent{
		maxTokens:     8092,
		toolsMap:      toolMap,
		tools:         tools,
		contextWindow: []Message{},
		provider:      provider,
	}
}
