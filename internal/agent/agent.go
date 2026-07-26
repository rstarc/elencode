package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/anthropics/anthropic-sdk-go"
	"io/fs"

	"github.com/rstarc/elencode/internal/tools"
)

type Tool interface {
	Name() string
	Description() string
	InputSchema() anthropic.ToolInputSchemaParam
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

type Agent struct {
	// TODO: ContextWindow
	ToolsMap map[string]Tool
	Tools    []Tool
}

func (a Agent) UseTool(ctx context.Context, name string, input json.RawMessage) (string, error) {
	fmt.Printf("[tool: %s]\n", name)
	result, err := a.ToolsMap[name].Execute(ctx, input)
	fmt.Printf("[>\n %s\n<]\n", result)
	return result, err
}

func NewAgent(root fs.FS) Agent {
	readTool := tools.NewReadTool(root)
	tools := []Tool{&readTool}
	toolMap := map[string]Tool{}
	for _, tool := range tools {
		toolMap[tool.Name()] = tool
	}
	return Agent{ToolsMap: toolMap, Tools: tools}
}
