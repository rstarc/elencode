package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
)

// TODO: Change schema to allow batching multiple edits in a single tool call
var editToolInputSchema InputSchema = InputSchema{
	Properties: map[string]Property{
		"path":    {Type: "string", Description: "Path to the file, relative to the workspace root"},
		"oldText": {Type: "string", Description: "Exact text to find and replace. Must match byte for byte"},
		"newText": {Type: "string", Description: "New text to replace the old text"},
	},
	Required: []string{"path", "oldText"},
}

type EditToolInput struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type EditTool struct {
	root fs.FS // Workspace Root
}

func NewEditTool(root fs.FS) EditTool {
	return EditTool{root: root}
}

func (rt EditTool) Name() string { return "edit" }
func (rt EditTool) Description() string {
	return "Edit a file by replacing exact text. The old text must match byte for byte."
}
func (rt EditTool) InputSchema() InputSchema { return editToolInputSchema }

func (rt EditTool) RequiresApproval() bool { return true }

func (rt EditTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {

	// Decode input
	var toolInput EditToolInput
	if err := json.Unmarshal(input, &toolInput); err != nil {
		return "", fmt.Errorf("invalid input: %v", err)
	}

	if !fs.ValidPath(toolInput.Path) {
		return "", fmt.Errorf("%q is not a valid workspace path", toolInput.Path)
	}

	fileBytes, err := fs.ReadFile(rt.root, toolInput.Path)
	if err != nil {
		return "", err
	}

	oldTextBytes := []byte(toolInput.OldText)
	newTextBytes := []byte(toolInput.NewText)
	updatedFile := bytes.Replace(fileBytes, oldTextBytes, newTextBytes, 1)
	err = os.WriteFile(toolInput.Path, updatedFile, 0o644)
	if err != nil {
		return "", err
	}

	output := fmt.Sprintf("Successfully replaced %d lines in %s", bytes.Count(oldTextBytes, []byte{'\n'}), toolInput.Path)

	return output, nil
}
