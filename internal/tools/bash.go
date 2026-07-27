package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os/exec"
	"time"
)

// TODO: Change schema to allow batching multiple edits in a single tool call
var bashToolInputSchema InputSchema = InputSchema{
	Properties: map[string]Property{
		"command": {Type: "string", Description: "Bash command to execute"},
		"timeout": {Type: "string", Description: "optional, timeout in seconds. No default timeout"},
	},
	Required: []string{"command"},
}

type BashToolInput struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout"`
}

type BashTool struct {
	root fs.FS // Workspace Root
}

func NewBashTool(root fs.FS) BashTool {
	return BashTool{root: root}
}

func (rt BashTool) Name() string { return "bash" }
func (rt BashTool) Description() string {
	return "Execute a bash command in the current workspace directory. Returns stdout and stderr."
}
func (rt BashTool) InputSchema() InputSchema { return bashToolInputSchema }
func (rt BashTool) RequiresApproval() bool   { return true }

func (rt BashTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {

	// Decode input
	var toolInput BashToolInput
	if err := json.Unmarshal(input, &toolInput); err != nil {
		return "", fmt.Errorf("invalid input: %v", err)
	}

	var cmdContext context.Context
	var cancelFunc context.CancelFunc
	timeout := time.Duration(toolInput.TimeoutSeconds) * time.Second
	if toolInput.TimeoutSeconds != 0 {
		cmdContext, cancelFunc = context.WithTimeout(ctx, timeout)
		defer cancelFunc()
	} else {
		cmdContext = ctx
	}

	// TODO: Use landlock or containers to further restrict bash tool
	cmd := exec.CommandContext(cmdContext, "bash", "-c", toolInput.Command)
	// Limit the time spent waiting on the child process to exit after completion, as CommandContext only kills the direct child process otherwise
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()

	if cmd.ProcessState == nil {
		return "", fmt.Errorf("Failed to run command: %w", err)
	}
	// TODO: Limit tool output length, write full output to temp file instead
	// TODO: Consider persisting the shell between tool calls instead of starting a new one each time
	// TODO: Stream output?

	output := string(out)
	if cmdContext.Err() != nil {
		// TODO: Check if timeout is the actual cause here?
		output += fmt.Sprintf("\n[timeout after %s seconds]\n", timeout)
	} else if code := cmd.ProcessState.ExitCode(); code != 0 {
		output += fmt.Sprintf("\n[exit %d]\n", code)
	}

	return output, nil
}
