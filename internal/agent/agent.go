package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/rstarc/elencode/internal/tools"
)

type Tool interface {
	Name() string
	Description() string
	InputSchema() tools.InputSchema
	RequiresApproval() bool
	Execute(ctx context.Context, input json.RawMessage) (string, error)
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

	if tool.RequiresApproval() {
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

func New(root fs.FS, provider Provider) Agent {
	// TODO: Use functional options
	tools := []Tool{
		tools.NewReadTool(root),
		tools.NewWriteTool(root),
		tools.NewEditTool(root),
		tools.NewBashTool(root),
	}

	toolMap := map[string]Tool{}
	for _, tool := range tools {
		toolMap[tool.Name()] = tool
	}

	return Agent{
		MaxTokens:     8092,
		ToolsMap:      toolMap,
		Tools:         tools,
		ContextWindow: []Message{},
		provider:      provider,
	}
}
