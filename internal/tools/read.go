package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
)

var readToolInputSchema InputSchema = InputSchema{
	Properties: map[string]Property{
		"path": {Type: "string", Description: "Path to the file, relative to the workspace root"},
	},
	Required: []string{"path"},
}

type readToolInput struct {
	Path string `json:"path"`
}

type ReadTool struct {
	root fs.FS // Workspace Root
}

func NewReadTool(root fs.FS) ReadTool {
	return ReadTool{root: root}
}

func (rt ReadTool) Name() string             { return "read" }
func (rt ReadTool) Description() string      { return "Read a file" }
func (rt ReadTool) InputSchema() InputSchema { return readToolInputSchema }

func (rt ReadTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	// TOOD: Implement offset and limit?

	// Decode input
	var toolInput readToolInput
	if err := json.Unmarshal(input, &toolInput); err != nil {
		return "", fmt.Errorf("read: invalid input: %v", err)
	}

	if !fs.ValidPath(toolInput.Path) {
		return "", fmt.Errorf("read: %q is not a valid workspace path", toolInput.Path)
	}

	fileBytes, err := fs.ReadFile(rt.root, toolInput.Path)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	// TODO: format output for agent
	// TODO: file size limit?
	// TODO: check file type?
	return string(fileBytes), nil
}
