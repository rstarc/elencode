package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"

	"github.com/rstarc/elencode/internal/agent"
)

var readToolInputSchema agent.InputSchema = agent.InputSchema{
	Properties: map[string]agent.Property{
		"path": {Type: "string", Description: "Path to the file, relative to the workspace root"},
	},
	Required: []string{"path"},
}

type readToolInput struct {
	Path string `json:"path"`
}

func NewReadTool(root fs.FS) agent.Tool {
	return agent.Tool{
		Name:             "read",
		Description:      "Read a file.",
		InputSchema:      readToolInputSchema,
		RequiresApproval: false,
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			return readFile(ctx, input, root)
		},
	}
}

func readFile(ctx context.Context, input json.RawMessage, root fs.FS) (string, error) {
	// TOOD: Implement offset and limit?

	// Decode input
	var toolInput readToolInput
	if err := json.Unmarshal(input, &toolInput); err != nil {
		return "", fmt.Errorf("read: invalid input: %v", err)
	}

	if !fs.ValidPath(toolInput.Path) {
		return "", fmt.Errorf("read: %q is not a valid workspace path", toolInput.Path)
	}

	fileBytes, err := fs.ReadFile(root, toolInput.Path)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	// TODO: format output for agent
	// TODO: file size limit?
	// TODO: check file type?
	return string(fileBytes), nil
}
