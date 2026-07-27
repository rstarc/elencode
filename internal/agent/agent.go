package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	ToolsMap      map[string]Tool
	Tools         []Tool
	ContextWindow []Message
	MaxTokens     int64
	provider      Provider
}

func (a Agent) UseTool(ctx context.Context, scanner *bufio.Scanner, name string, input json.RawMessage) (string, error) {
	fmt.Print("[\n")
	fmt.Printf(" $ %s\n", name)
	fmt.Printf(" <- %s\n", input)

	tool := a.ToolsMap[name]
	var err error
	var result string = ""

	if tool.RequiresApproval {
		// Prompt for tool use confirmation
		fmt.Printf(" > Allow tool use? [y/N])")
		// line, err := stdin.ReadString("\n")
		scanner.Scan()
		userInput := strings.ToLower(strings.TrimSpace(scanner.Text()))

		if userInput == "y" || userInput == "yes" {
			result, err = tool.Execute(ctx, input)
		} else {
			err = fmt.Errorf("Tool use rejected by user")
		}
	} else {
		result, err = tool.Execute(ctx, input)
	}

	fmt.Printf(" -> %q", result)
	fmt.Print("\n]\n")
	return result, err
}

func (a Agent) ProcessTurn(ctx context.Context) (Response, error) {
	return a.provider.Process(ctx,
		Request{
			MaxTokens: a.MaxTokens, // TODO: set dynamically from API query if not set explicitly, requires streaming if set sufficiently high
			Tools:     a.Tools,
			Messages:  a.ContextWindow,
		},
	)
}

func New(provider Provider, tools []Tool) Agent {
	// TODO: Use functional options

	toolMap := map[string]Tool{}
	for _, tool := range tools {
		toolMap[tool.Name] = tool
	}

	return Agent{
		MaxTokens:     8092,
		ToolsMap:      toolMap,
		Tools:         tools,
		ContextWindow: []Message{},
		provider:      provider,
	}
}
