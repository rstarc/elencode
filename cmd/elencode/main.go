package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/tools"
)

func main() {

	// Load config
	cfg, err := config.Load()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// Initialize provider
	provider := anthropic.New(cfg.AnthropicAPIKey)

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
