package main

import (
	"context"
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
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}

	// Initialize provider
	provider := anthropic.New(cfg.AnthropicAPIKey.Reveal(), cfg.Model, cfg.ThinkingEnabled)

	// Models differ in what kind of reasoning they accept, and asking for the
	// wrong kind fails the turn. Not fatal: without it the session simply runs
	// without thinking, which beats refusing to start.
	if err := provider.Resolve(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "elencode: could not read what %s supports, continuing without thinking: %v\n", cfg.Model, err)
	}

	// TODO: Use os.OpenRoot instead
	root := os.DirFS(".")
	tools := []agent.Tool{
		tools.NewReadTool(root),
		tools.NewWriteTool(root),
		tools.NewEditTool(root),
		tools.NewBashTool(root),
	}
	agentConfig := agent.New(provider, tools)

	tui := tea.NewProgram(newModel(agentConfig, cfg))
	if _, err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}

}
