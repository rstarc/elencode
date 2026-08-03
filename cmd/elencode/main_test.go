package main

import (
	"testing"

	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/provider/openai"
)

func TestProviderFromConfigSelectsOpenAI(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderOpenAI, OpenAIAPIKey: "sk", ThinkingEffort: "high"}

	prov, defModel, _, err := providerFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := prov.(*openai.Client); !ok {
		t.Fatalf("provider = %T, want *openai.Client", prov)
	}
	if defModel != openai.DefaultModelID() {
		t.Fatalf("default model = %q", defModel)
	}
}

func TestProviderFromConfigSelectsAnthropic(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderAnthropic, AnthropicAPIKey: "sk"}

	prov, defModel, _, err := providerFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := prov.(*anthropic.Client); !ok {
		t.Fatalf("provider = %T, want *anthropic.Client", prov)
	}
	if defModel != anthropic.DefaultModelID() {
		t.Fatalf("default model = %q", defModel)
	}
}

func TestProviderFromConfigRejectsUnknown(t *testing.T) {
	if _, _, _, err := providerFromConfig(config.Config{Provider: "nope"}); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}
