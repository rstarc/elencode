package main

import (
	"errors"
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

	// One client per key found. Which of them a turn talks to is decided by the
	// model, here and at every /model after it.
	providers := loadProviders(cfg)
	selectedModel, notice, err := startupModel(cfg, providers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}
	if notice != "" {
		fmt.Fprintf(os.Stderr, "elencode: %s\n", notice)
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
	agentConfig := agent.New(tools)
	agentConfig.SetModel(selectedModel, providers[selectedModel.Provider])

	tui := tea.NewProgram(newModel(agentConfig, cfg, defaultCommands(), providers, catalog()))
	if _, err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}
}

// providerSet holds the live client for every provider a key was found for.
// Built and read on one goroutine — a turn is handed the one client it needs,
// never the set.
type providerSet map[agent.ProviderName]agent.Provider

// loadProviders builds a client for each API key the config holds. No error to
// return: which providers a session can reach is whichever keys were there,
// and an empty set is the caller's to report. Kept to one function because a
// key entered mid-session will want to run it again.
func loadProviders(cfg config.Config) providerSet {
	effort := agent.Effort(cfg.ThinkingEffort)
	providers := providerSet{}

	if cfg.AnthropicAPIKey != "" {
		providers[agent.ProviderAnthropic] = anthropic.New(cfg.AnthropicAPIKey.Reveal(), cfg.ThinkingEnabled, effort)
	}
	if cfg.OpenAIAPIKey != "" {
		providers[agent.ProviderOpenAI] = openai.New(cfg.OpenAIAPIKey.Reveal(), cfg.ThinkingEnabled, effort)
	}
	return providers
}

// catalog is every model this build knows about, whether or not its provider
// has a key: what to offer is a smaller question than what exists, and naming
// a model nobody can reach deserves a better answer than "unknown model".
func catalog() []agent.Model {
	return append(anthropic.Catalog(), openai.Catalog()...)
}

// startupModel decides which model the session opens on. The notice it returns
// says why that is not what the config asked for, and is empty when it is:
// a saved model whose provider lost its key, or which no longer exists, is
// worth saying out loud but not worth refusing to start over.
func startupModel(cfg config.Config, providers providerSet) (agent.Model, string, error) {
	fallback, err := defaultModel(providers)
	if err != nil {
		return agent.Model{}, "", err
	}

	if cfg.Model == "" {
		return fallback, "", nil
	}

	wanted, ok := agent.FindModel(catalog(), cfg.Model)
	if !ok {
		return fallback, fmt.Sprintf("no model named %s, starting on %s instead", cfg.Model, fallback.ID), nil
	}
	if _, keyed := providers[wanted.Provider]; !keyed {
		return fallback, fmt.Sprintf("no API key for %s, so %s is out of reach: starting on %s instead", wanted.Provider, wanted.ID, fallback.ID), nil
	}
	return wanted, "", nil
}

// defaultModel is the model a session opens on when the config names none: the
// default of the first provider that has a key, in the order agent prefers.
func defaultModel(providers providerSet) (agent.Model, error) {
	for _, name := range agent.Providers {
		if _, ok := providers[name]; !ok {
			continue
		}
		switch name {
		case agent.ProviderAnthropic:
			return anthropic.Default(), nil
		case agent.ProviderOpenAI:
			return openai.Default(), nil
		}
	}
	return agent.Model{}, errors.New("no API key for any provider")
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

// configWithEffectiveModel records what the session actually opened on, so the
// config view and the picker's highlight show the model in use rather than
// whatever the file happened to say — including nothing at all. Always the
// qualified name: a bare id would not say who to ask for it.
func configWithEffectiveModel(cfg config.Config, model agent.Model) config.Config {
	cfg.Model = model.Qualified()
	return cfg
}
