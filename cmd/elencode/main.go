package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/tools"
)

const ANTHROPIC_API_KEY_ENV_VAR_NAME = "ANTHROPIC_API_KEY"

func main() {

	// Check for API Key
	if _, ok := os.LookupEnv(ANTHROPIC_API_KEY_ENV_VAR_NAME); !ok {
		fmt.Printf("API Key Environment Variable %q not set, exiting\n", ANTHROPIC_API_KEY_ENV_VAR_NAME)
		os.Exit(1)
	}

	// Initialize agent
	provider := anthropic.New()

	// TODO: Use os.OpenRoot instead
	root := os.DirFS(".")
	tools := []agent.Tool{
		tools.NewReadTool(root),
		tools.NewWriteTool(root),
		tools.NewEditTool(root),
		tools.NewBashTool(root),
	}
	agentConfig := agent.New(provider, tools)

	tui := tea.NewProgram(newModel(&agentConfig))
	if _, err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start TUI: %v\n", err)
	}

}
