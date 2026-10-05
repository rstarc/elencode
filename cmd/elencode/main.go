package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
	"github.com/rstarc/elencode/internal/commands"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/provider/openai"
	"github.com/rstarc/elencode/internal/tools"
)

func main() {
	// A command runs instead of the session, and before the config load: it
	// loads what it needs itself, so `elencode version` and signing in work
	// without an API key.
	if handled, err := runCLI(os.Args[1:], os.Stdout); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, providers, selectedModel, err := loadSession(os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}

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

	session := newModel(agentConfig, cfg, defaultCommands(), providers, catalog())
	// Known before there is a login: /login is what writes it
	loginPath, err := config.ChatGPTLoginPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}
	session.signIn = defaultSignIn(loginPath)
	tui := tea.NewProgram(session)
	if _, err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "elencode: %v\n", err)
		os.Exit(1)
	}
}

// loadSession is what a session starts from: the config, a client for every
// credential it names, and the model to open on, recorded in the config as the
// one in use. Shared with the CLI commands that report on a session, so they
// say what the session would. Why the model is not the configured one goes to
// notices.
func loadSession(notices io.Writer) (config.Config, providerSet, agent.Model, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, nil, agent.Model{}, err
	}

	// One client per credential found. Which of them a turn talks to is decided
	// by the model, here and at every /model after it.
	providers, err := loadProviders(cfg)
	if err != nil {
		return cfg, nil, agent.Model{}, err
	}
	selected, notice, err := startupModel(cfg, providers)
	if err != nil {
		return cfg, nil, agent.Model{}, err
	}
	if notice != "" {
		_, _ = fmt.Fprintf(notices, "elencode: %s\n", notice)
	}
	return configWithEffectiveModel(cfg, selected), providers, selected, nil
}

// providerSet holds the live client for every provider a key was found for.
// Built and read on one goroutine — a turn is handed the one client it needs,
// never the set.
type providerSet map[agent.ProviderName]agent.Provider

// loadProviders builds a client for each API key the config holds, and for the
// ChatGPT login if there is one. Which providers a session can reach is
// whichever credentials were there, and an empty set is the caller's to
// report; the only failure is a login that is there but cannot be read. Kept
// to one function because a key entered mid-session will want to run it again.
func loadProviders(cfg config.Config) (providerSet, error) {
	effort := agent.Effort(cfg.ThinkingEffort)
	providers := providerSet{}

	if cfg.AnthropicAPIKey != "" {
		providers[agent.ProviderAnthropic] = anthropic.New(cfg.AnthropicAPIKey.Reveal(), cfg.ThinkingEnabled, effort)
	}
	if cfg.OpenAIAPIKey != "" {
		providers[agent.ProviderOpenAI] = openai.New(cfg.OpenAIAPIKey.Reveal(), cfg.ThinkingEnabled, effort)
	}
	if cfg.ChatGPTLoginPath != "" {
		provider, err := newChatGPTProvider(cfg.ChatGPTLoginPath, cfg)
		if err != nil {
			return nil, err
		}
		providers[agent.ProviderChatGPT] = provider
	}
	return providers, nil
}

// newChatGPTProvider is the client for the login saved at path. Also run by
// /login, which adds the provider to a session already under way.
func newChatGPTProvider(path string, cfg config.Config) (agent.Provider, error) {
	tokens, err := chatgpt.LoadTokens(path)
	if err != nil {
		return nil, err
	}
	login := chatgpt.NewSource(path, tokens, chatgpt.OAuth{})
	return openai.NewChatGPT(login, cfg.ThinkingEnabled, agent.Effort(cfg.ThinkingEffort)), nil
}

// catalog is every model this build knows about, whether or not its provider
// has a key: what to offer is a smaller question than what exists, and naming
// a model nobody can reach deserves a better answer than "unknown model".
func catalog() []agent.Model {
	return slices.Concat(anthropic.Catalog(), openai.Catalog(), openai.ChatGPTCatalog())
}

// resolveModel is the model name refers to, if the session can reach it: the
// rules /model and `elencode model` both apply.
func resolveModel(models []agent.Model, providers providerSet, name string) (agent.Model, error) {
	chosen, ok := agent.FindModel(models, name)
	if !ok {
		// The catalog is what this build knows, so an id it does not have is
		// either a typo or a model newer than the binary — which "openai/" in
		// front of it would reach.
		return agent.Model{}, fmt.Errorf("unknown model: %s (name its provider, as in openai/%s, to use one this version does not know)", name, name)
	}
	if _, keyed := providers[chosen.Provider]; !keyed {
		return agent.Model{}, fmt.Errorf("%s, so %s cannot be reached", missingCredential(chosen.Provider), chosen.ID)
	}
	return chosen, nil
}

// reachableModels is what is offered: a model whose provider has no
// credential cannot be talked to, so offering it would only produce a failed
// turn.
func reachableModels(models []agent.Model, providers providerSet) []agent.Model {
	var reachable []agent.Model
	for _, candidate := range models {
		if _, keyed := providers[candidate.Provider]; keyed {
			reachable = append(reachable, candidate)
		}
	}
	return reachable
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
		return fallback, fmt.Sprintf("%s, so %s is out of reach: starting on %s instead", missingCredential(wanted.Provider), wanted.ID, fallback.ID), nil
	}
	return wanted, "", nil
}

// missingCredential says what a provider lacks for a session to reach it, in a
// form that leads a sentence. ChatGPT has no key: what it lacks is a login.
func missingCredential(provider agent.ProviderName) string {
	if provider == agent.ProviderChatGPT {
		return "not signed in to ChatGPT (/login chatgpt, or `elencode login chatgpt`)"
	}
	return "no API key for " + string(provider)
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
		case agent.ProviderChatGPT:
			return openai.ChatGPTDefault(), nil
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
		commands.NewLoginCommand(),
		commands.NewLogoutCommand(),
		commands.NewVersionCommand(),
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
