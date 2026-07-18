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
	Tools map[string]Tool
}

func (a Agent) UseTool(ctx context.Context, name string, input json.RawMessage) (string, error) {
	fmt.Printf("[tool: %s]\n", name)
	result, err := a.Tools[name].Execute(ctx, input)
	fmt.Printf("[>\n %s\n<]\n", result)
	return result, err
}

func NewAgent(root fs.FS) Agent {
	readTool := tools.NewReadTool(root)
	toolMap := map[string]Tool{
		readTool.Name(): &readTool,
	}
	return Agent{Tools: toolMap}
}
