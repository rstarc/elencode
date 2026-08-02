package main

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/commands"
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
	provider := anthropic.New(cfg.AnthropicAPIKey.Reveal(), cfg.ThinkingEnabled)
	modelID := cfg.Model
	if modelID == "" {
		modelID = anthropic.DefaultModelID()
	}
	selectedModel := agent.Model{ID: modelID}

	// Models differ in what kind of reasoning they accept, and asking for the
	// wrong kind fails the turn. Not fatal: without it the session simply runs
	// without thinking, which beats refusing to start.
	if resolved, err := provider.Resolve(context.Background(), modelID); err != nil {
		fmt.Fprintf(os.Stderr, "elencode: could not read what %s supports, continuing without thinking: %v\n", modelID, err)
	} else {
		selectedModel = resolved
	}
	cfg = configWithEffectiveModel(cfg, selectedModel)

	// TODO: Use os.OpenRoot instead
	root := os.DirFS(".")
	tools := []agent.Tool{
		tools.NewReadTool(root),
		tools.NewWriteTool(root),
		tools.NewEditTool(root),
		tools.NewBashTool(root),
	}
	agentConfig := agent.New(provider, tools)
	agentConfig.SetModel(selectedModel)

	tui := tea.NewProgram(newModel(agentConfig, cfg, defaultCommands()))
	if _, err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}

}

// defaultCommands is the set of slash commands a session offers. Assembled here
// rather than in the commands package, so what exists is decided in one place,
// the way the tool set is.
func defaultCommands() commands.Registry {
	return commands.NewRegistry(
		commands.NewConfigCommand(),
		commands.NewModelCommand(),
		commands.NewQuitCommand(),
	)
}

func configWithEffectiveModel(cfg config.Config, model agent.Model) config.Config {
	if cfg.Model == "" {
		cfg.Model = model.ID
	}
	return cfg
}
