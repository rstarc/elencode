package main

import (
	"strings"
	"testing"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/provider/openai"
)

func TestLoadProvidersBuildsOnlyTheKeyedProviders(t *testing.T) {
	providers := loadProviders(config.Config{OpenAIAPIKey: "sk-oai", ThinkingEffort: "high"})

	if _, ok := providers[agent.ProviderAnthropic]; ok {
		t.Error("built an anthropic client without an anthropic key")
	}
	client, ok := providers[agent.ProviderOpenAI]
	if !ok {
		t.Fatal("no openai client for an openai key")
	}
	if _, ok := client.(*openai.Client); !ok {
		t.Errorf("openai provider = %T, want *openai.Client", client)
	}
}

func TestLoadProvidersBuildsBothWhenBothKeysAreSet(t *testing.T) {
	providers := loadProviders(config.Config{AnthropicAPIKey: "sk-ant", OpenAIAPIKey: "sk-oai"})

	if _, ok := providers[agent.ProviderAnthropic].(*anthropic.Client); !ok {
		t.Errorf("anthropic provider = %T, want *anthropic.Client", providers[agent.ProviderAnthropic])
	}
	if _, ok := providers[agent.ProviderOpenAI].(*openai.Client); !ok {
		t.Errorf("openai provider = %T, want *openai.Client", providers[agent.ProviderOpenAI])
	}
}

func bothProviders() providerSet {
	return loadProviders(config.Config{AnthropicAPIKey: "sk-ant", OpenAIAPIKey: "sk-oai"})
}

func TestStartupModelUsesTheConfiguredModel(t *testing.T) {
	model, notice, err := startupModel(config.Config{Model: "gpt-5"}, bothProviders())
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}

	if model.ID != "gpt-5" || model.Provider != agent.ProviderOpenAI {
		t.Errorf("model = %+v, want openai's gpt-5", model)
	}
	if notice != "" {
		t.Errorf("notice = %q, want none when the config was honoured", notice)
	}
}

// Unsetting a key must not brick a session that was last used on that
// provider: it is a notice and a fallback, not a refusal to start.
func TestStartupModelFallsBackWhenTheModelsProviderHasNoKey(t *testing.T) {
	providers := loadProviders(config.Config{AnthropicAPIKey: "sk-ant"})

	model, notice, err := startupModel(config.Config{Model: "openai/gpt-5"}, providers)
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}

	if model != anthropic.Default() {
		t.Errorf("model = %+v, want the anthropic default", model)
	}
	for _, want := range []string{"gpt-5", anthropic.Default().ID} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice = %q, want it to name %q", notice, want)
		}
	}
}

// A model id that has been retired since it was saved is the same situation
func TestStartupModelFallsBackWhenTheConfiguredModelIsUnknown(t *testing.T) {
	model, notice, err := startupModel(config.Config{Model: "claude-from-the-future"}, bothProviders())
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}

	if model != anthropic.Default() {
		t.Errorf("model = %+v, want the anthropic default", model)
	}
	if !strings.Contains(notice, "claude-from-the-future") {
		t.Errorf("notice = %q, want it to name the model it could not find", notice)
	}
}

func TestStartupModelPrefersAnthropicWhenBothHaveKeys(t *testing.T) {
	model, notice, err := startupModel(config.Config{}, bothProviders())
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}

	if model != anthropic.Default() {
		t.Errorf("model = %+v, want the anthropic default", model)
	}
	// Nothing was asked for, so nothing was overruled
	if notice != "" {
		t.Errorf("notice = %q, want none", notice)
	}
}

func TestStartupModelUsesTheOnlyKeyedProvidersDefault(t *testing.T) {
	providers := loadProviders(config.Config{OpenAIAPIKey: "sk-oai"})

	model, _, err := startupModel(config.Config{}, providers)
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}
	if model != openai.Default() {
		t.Errorf("model = %+v, want the openai default", model)
	}
}

func TestStartupModelAcceptsAQualifiedModelOutsideTheCatalog(t *testing.T) {
	model, notice, err := startupModel(config.Config{Model: "openai/gpt-from-the-future"}, bothProviders())
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}

	want := agent.Model{Provider: agent.ProviderOpenAI, ID: "gpt-from-the-future", DisplayName: "gpt-from-the-future"}
	if model != want {
		t.Errorf("model = %+v, want %+v", model, want)
	}
	if notice != "" {
		t.Errorf("notice = %q, want none", notice)
	}
}

func TestStartupModelFailsWithoutAnyProvider(t *testing.T) {
	if _, _, err := startupModel(config.Config{}, providerSet{}); err == nil {
		t.Fatal("startupModel succeeded with no provider to talk to")
	}
}
