package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rstarc/elencode/internal/agent"
)

var writeToolInputSchema agent.InputSchema = agent.InputSchema{
	Properties: map[string]agent.Property{
		"path":    {Type: "string", Description: "Path to the file, relative to the workspace root"},
		"content": {Type: "string", Description: "The literal file content"},
	},
	Required: []string{"path", "content"},
}

type writeToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// TODO: File Mode
}

func NewWriteTool(root fs.FS) agent.Tool {
	return agent.Tool{
		Name:             "write",
		Description:      "Create a new file or completely replace a file, recursively creating the directory tree.",
		InputSchema:      writeToolInputSchema,
		RequiresApproval: true,
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			return createOrReplaceFile(ctx, input)
		},
	}
}

func createOrReplaceFile(ctx context.Context, input json.RawMessage) (string, error) {
	// Decode input
	var toolInput writeToolInput
	err := json.Unmarshal(input, &toolInput)
	if err != nil {
		return "", fmt.Errorf("write: invalid input: %v", err)
	}

	if !fs.ValidPath(toolInput.Path) {
		return "", fmt.Errorf("write: %q is not a valid workspace path", toolInput.Path)
	}

	// Create directory tree
	err = os.MkdirAll(filepath.Dir(toolInput.Path), 0o755)
	if err != nil {
		return "", err
	}

	// Write file
	// TODO: if file exists, use existing mode?
	// TODO: Use a more robust way to write files
	// TODO: file mutex
	fileBytes := []byte(toolInput.Content)
	err = os.WriteFile(toolInput.Path, fileBytes, 0o644)
	if err != nil {
		return "", err
	}

	output := fmt.Sprintf("Successfully wrote %d bytes", len(fileBytes))

	return output, nil
}
