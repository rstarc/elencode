package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/commands"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/provider/openai"
	"github.com/rstarc/elencode/internal/tools"
)

func main() {
	// Before the config load, so `elencode version` works without an API key.
	if len(os.Args) > 1 && os.Args[1] == "version" {
		bi, ok := debug.ReadBuildInfo()
		fmt.Println(versionLine(version, bi, ok))
		return
	}

	// Load config
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}

	// Initialize provider
	provider, defaultModelID, resolve, err := providerFromConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}
	modelID := cfg.Model
	if modelID == "" {
		modelID = defaultModelID
	}
	selectedModel := agent.Model{ID: modelID}

	// Models differ in what kind of reasoning they accept, and asking for the
	// wrong kind fails the turn. Not fatal: without it the session simply runs
	// without thinking, which beats refusing to start.
	if resolved, err := resolve(context.Background(), modelID); err != nil {
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

// providerFromConfig builds the provider the config selects, along with the two
// things that are not on the agent.Provider interface: its default model and
// its Resolve. Returning Resolve as a closure keeps main from having to reach
// for a concrete type through the interface.
func providerFromConfig(cfg config.Config) (agent.Provider, string, func(context.Context, string) (agent.Model, error), error) {
	effort := agent.Effort(cfg.ThinkingEffort)

	switch cfg.Provider {
	case config.ProviderOpenAI:
		client := openai.New(cfg.OpenAIAPIKey.Reveal(), cfg.ThinkingEnabled, effort)
		return client, openai.DefaultModelID(), client.Resolve, nil
	// Empty means a config written before the setting existed, which was
	// Anthropic-only.
	case config.ProviderAnthropic, "":
		client := anthropic.New(cfg.AnthropicAPIKey.Reveal(), cfg.ThinkingEnabled, effort)
		return client, anthropic.DefaultModelID(), client.Resolve, nil
	default:
		return nil, "", nil, fmt.Errorf("unknown provider %q", cfg.Provider)
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
