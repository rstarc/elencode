package main

import (
	"bytes"
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
	providers := mustLoadProviders(t, withKeys(agent.ProviderOpenAI))

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
	providers := mustLoadProviders(t, withKeys(agent.ProviderAnthropic, agent.ProviderOpenAI))

	if _, ok := providers[agent.ProviderAnthropic].(*anthropic.Client); !ok {
		t.Errorf("anthropic provider = %T, want *anthropic.Client", providers[agent.ProviderAnthropic])
	}
	if _, ok := providers[agent.ProviderOpenAI].(*openai.Client); !ok {
		t.Errorf("openai provider = %T, want *openai.Client", providers[agent.ProviderOpenAI])
	}
}

// withKeys is a config holding an API key for each of providers, as if
// saved in credentials.json.
func withKeys(providers ...agent.ProviderName) config.Config {
	creds := config.Credentials{}
	for _, provider := range providers {
		creds[provider] = config.Credential{APIKey: config.Secret("sk-" + provider)}
	}
	return config.Config{Credentials: creds}
}

// A key in the environment connects a provider as one saved in
// credentials.json does.
func TestLoadProvidersUsesAKeyFromTheEnvironment(t *testing.T) {
	cfg := config.Config{Env: func(name string) (string, bool) {
		return "sk-oai", name == "OPENAI_API_KEY"
	}}

	providers := mustLoadProviders(t, cfg)

	if _, ok := providers[agent.ProviderOpenAI].(*openai.Client); !ok {
		t.Errorf("openai provider = %T, want *openai.Client", providers[agent.ProviderOpenAI])
	}
}

func bothProviders() providerSet {
	providers, err := loadProviders(withKeys(agent.ProviderAnthropic, agent.ProviderOpenAI))
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
	providers := mustLoadProviders(t, withKeys(agent.ProviderAnthropic))

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
	providers := mustLoadProviders(t, withKeys(agent.ProviderOpenAI))

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

// With nothing connected there is no model to open on, which is a first
// start rather than a failure: the session offers to connect a provider.
func TestStartupModelIsNoneWithoutAnyProvider(t *testing.T) {
	model, notice, err := startupModel(config.Config{Model: "anthropic/model-one"}, providerSet{})
	if err != nil || model != (agent.Model{}) || notice != "" {
		t.Errorf("startupModel = %+v, %q, %v, want no model and no fuss", model, notice, err)
	}
}

// The CLI has no session to offer a connect in, so it says how.
func TestModelCLIWithNothingConnectedSaysToConnect(t *testing.T) {
	err := runModelCLI(config.Config{}, providerSet{}, testModels, agent.Model{}, "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "elencode connect") {
		t.Errorf("err = %v, want it to point at elencode connect", err)
	}
}

// writeLogin saves a ChatGPT login the way `elencode connect chatgpt` would, and returns
// a config that has found it.
func writeLogin(t *testing.T) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	tokens := chatgpt.Tokens{AccessToken: "access", RefreshToken: "refresh", IDToken: "id", AccountID: "acct"}
	creds, err := config.SaveCredential(path, agent.ProviderChatGPT, config.Credential{Login: &tokens})
	if err != nil {
		t.Fatal(err)
	}
	return config.Config{CredentialsPath: path, Credentials: creds}
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
	providers := mustLoadProviders(t, withKeys(agent.ProviderOpenAI))

	if _, ok := providers[agent.ProviderChatGPT]; ok {
		t.Error("built a chatgpt client without a login")
	}
}

// A login that cannot be used is worth stopping for: starting without it
// would silently move the session onto another provider's bill.
func TestLoadProvidersFailsOnABrokenLogin(t *testing.T) {
	cfg := withKeys(agent.ProviderOpenAI)
	cfg.Credentials[agent.ProviderChatGPT] = config.Credential{Login: &chatgpt.Tokens{AccessToken: "a"}}

	_, err := loadProviders(cfg)
	if err == nil || !strings.Contains(err.Error(), "elencode connect chatgpt") {
		t.Errorf("err = %v, want it to say how to sign in again", err)
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
	cfg.Credentials = cfg.Credentials.With(agent.ProviderOpenAI, config.Credential{APIKey: "sk-oai"})

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

// Moonshot is reached through Kimi's Anthropic-compatible API, so its client
// is the anthropic package's.
func TestLoadProvidersBuildsTheMoonshotProviderFromAKey(t *testing.T) {
	providers := mustLoadProviders(t, withKeys(agent.ProviderMoonshot))

	if _, ok := providers[agent.ProviderMoonshot].(*anthropic.Client); !ok {
		t.Errorf("moonshot provider = %T, want *anthropic.Client", providers[agent.ProviderMoonshot])
	}
}

func TestStartupModelUsesTheMoonshotDefaultForAMoonshotKeyAlone(t *testing.T) {
	providers := mustLoadProviders(t, withKeys(agent.ProviderMoonshot))

	model, _, err := startupModel(config.Config{}, providers)
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}
	if model != anthropic.MoonshotDefault() {
		t.Errorf("model = %+v, want the moonshot default", model)
	}
}

func TestCatalogOffersTheMoonshotModels(t *testing.T) {
	for _, want := range anthropic.MoonshotCatalog() {
		if !slices.Contains(catalog(), want) {
			t.Errorf("catalog is missing %s", want.Qualified())
		}
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
	if strings.Contains(got, "API key") || !strings.Contains(got, "elencode connect chatgpt") {
		t.Errorf("missingCredential(chatgpt) = %q, want it to point at elencode connect chatgpt", got)
	}
}

func TestStartupModelFallsBackWhenNotSignedInToChatGPT(t *testing.T) {
	_, notice, err := startupModel(config.Config{Model: "chatgpt/gpt-6-sol"}, mustLoadProviders(t, withKeys(agent.ProviderOpenAI)))
	if err != nil {
		t.Fatalf("startupModel: %v", err)
	}
	if !strings.Contains(notice, "elencode connect chatgpt") {
		t.Errorf("notice = %q, want it to say how to sign in", notice)
	}
}
