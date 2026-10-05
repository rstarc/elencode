package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/provider/openai"
)

func TestLoadProvidersBuildsOnlyTheKeyedProviders(t *testing.T) {
	providers := mustLoadProviders(t, config.Config{OpenAIAPIKey: "sk-oai", ThinkingEffort: "high"})

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
	providers := mustLoadProviders(t, config.Config{AnthropicAPIKey: "sk-ant", OpenAIAPIKey: "sk-oai"})

	if _, ok := providers[agent.ProviderAnthropic].(*anthropic.Client); !ok {
		t.Errorf("anthropic provider = %T, want *anthropic.Client", providers[agent.ProviderAnthropic])
	}
	if _, ok := providers[agent.ProviderOpenAI].(*openai.Client); !ok {
		t.Errorf("openai provider = %T, want *openai.Client", providers[agent.ProviderOpenAI])
	}
}

func bothProviders() providerSet {
	providers, err := loadProviders(config.Config{AnthropicAPIKey: "sk-ant", OpenAIAPIKey: "sk-oai"})
	if err != nil {
		panic(err)
	}
	return providers
}

// mustLoadProviders is loadProviders for a config that cannot fail to load.
func mustLoadProviders(t *testing.T, cfg config.Config) providerSet {
	t.Helper()
	providers, err := loadProviders(cfg)
	if err != nil {
		t.Fatalf("loadProviders: %v", err)
	}
	return providers
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
	providers := mustLoadProviders(t, config.Config{AnthropicAPIKey: "sk-ant"})

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
	providers := mustLoadProviders(t, config.Config{OpenAIAPIKey: "sk-oai"})

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

// writeLogin saves a ChatGPT login the way `elencode login` would, and returns
// a config that has found it.
func writeLogin(t *testing.T) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chatgpt.json")
	tokens := chatgpt.Tokens{AccessToken: "access", RefreshToken: "refresh", IDToken: "id", AccountID: "acct"}
	if err := chatgpt.SaveTokens(path, tokens); err != nil {
		t.Fatal(err)
	}
	return config.Config{ChatGPTLoginPath: path}
}

func TestLoadProvidersBuildsTheChatGPTProviderFromALogin(t *testing.T) {
	providers := mustLoadProviders(t, writeLogin(t))

	if _, ok := providers[agent.ProviderChatGPT].(*openai.Client); !ok {
		t.Errorf("chatgpt provider = %T, want *openai.Client", providers[agent.ProviderChatGPT])
	}
	if _, ok := providers[agent.ProviderOpenAI]; ok {
		t.Error("built an openai client without an openai key")
	}
}

func TestLoadProvidersBuildsNoChatGPTProviderWithoutALogin(t *testing.T) {
	providers := mustLoadProviders(t, config.Config{OpenAIAPIKey: "sk-oai"})

	if _, ok := providers[agent.ProviderChatGPT]; ok {
		t.Error("built a chatgpt client without a login")
	}
}

// A login that cannot be read is worth stopping for: starting without it
// would silently move the session onto another provider's bill.
func TestLoadProvidersFailsOnABrokenLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chatgpt.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadProviders(config.Config{OpenAIAPIKey: "sk-oai", ChatGPTLoginPath: path})
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want it to name %s", err, path)
	}
}

func TestStartupModelUsesTheChatGPTDefaultForALoginAlone(t *testing.T) {
	model, _, err := startupModel(config.Config{}, mustLoadProviders(t, writeLogin(t)))
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}
	if model != openai.ChatGPTDefault() {
		t.Errorf("model = %+v, want the chatgpt default", model)
	}
}

// Signing in is the deliberate choice, so it wins over an API key that may
// have been set for something else entirely.
func TestStartupModelPrefersTheLoginOverAnOpenAIKey(t *testing.T) {
	cfg := writeLogin(t)
	cfg.OpenAIAPIKey = "sk-oai"

	model, _, err := startupModel(config.Config{}, mustLoadProviders(t, cfg))
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}
	if model.Provider != agent.ProviderChatGPT {
		t.Errorf("model = %+v, want a chatgpt model", model)
	}
}

func TestStartupModelUsesAConfiguredChatGPTModel(t *testing.T) {
	model, notice, err := startupModel(config.Config{Model: "chatgpt/gpt-6-sol"}, mustLoadProviders(t, writeLogin(t)))
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}
	if model.Provider != agent.ProviderChatGPT || model.ID != "gpt-6-sol" || notice != "" {
		t.Errorf("model = %+v, notice = %q, want chatgpt's gpt-6-sol", model, notice)
	}
}

func TestCatalogOffersTheChatGPTModels(t *testing.T) {
	for _, want := range openai.ChatGPTCatalog() {
		if !slices.Contains(catalog(), want) {
			t.Errorf("catalog is missing %s", want.Qualified())
		}
	}
}

func TestMissingCredentialNamesTheKeyForAKeyedProvider(t *testing.T) {
	if got := missingCredential(agent.ProviderOpenAI); got != "no API key for openai" {
		t.Errorf("missingCredential(openai) = %q", got)
	}
}

// The subscription has no key to set: what is missing is a login, and the way
// to get one is a command.
func TestMissingCredentialSaysHowToSignInToChatGPT(t *testing.T) {
	got := missingCredential(agent.ProviderChatGPT)
	if strings.Contains(got, "API key") || !strings.Contains(got, "elencode login") {
		t.Errorf("missingCredential(chatgpt) = %q, want it to point at elencode login", got)
	}
}

func TestStartupModelFallsBackWhenNotSignedInToChatGPT(t *testing.T) {
	_, notice, err := startupModel(config.Config{Model: "chatgpt/gpt-6-sol"}, mustLoadProviders(t, config.Config{OpenAIAPIKey: "sk-oai"}))
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}
	if !strings.Contains(notice, "elencode login") {
		t.Errorf("notice = %q, want it to say how to sign in", notice)
	}
}
