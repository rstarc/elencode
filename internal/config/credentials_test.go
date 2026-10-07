package config

import (
	"encoding/json"
	"os"
	"path"
	"runtime"
	"strings"
	"testing"

	"github.com/rstarc/elencode/internal/agent"
)

// env is an environment holding exactly vars.
func env(vars map[string]string) Env {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

// writeCredentials leaves body as credentials.json beside the config file
// writeConfig made, and returns its path.
func writeCredentials(t *testing.T, configFile, body string) string {
	t.Helper()
	file := path.Join(path.Dir(configFile), "credentials.json")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatalf("writing credentials: %v", err)
	}
	return file
}

func TestCredentialsUnmarshalTheKeyAsASecret(t *testing.T) {
	var creds Credentials
	if err := json.Unmarshal([]byte(`{"anthropic":{"api_key":"`+realKey+`"}}`), &creds); err != nil {
		t.Fatalf("unmarshalling credentials: %v", err)
	}
	if got := creds[agent.ProviderAnthropic].APIKey.Reveal(); got != realKey {
		t.Errorf("APIKey = %q, want %q", got, realKey)
	}
}

func TestLoadReadsTheCredentialsBesideTheConfig(t *testing.T) {
	file := writeConfig(t, `{}`)
	creds := writeCredentials(t, file, `{"anthropic":{"api_key":"`+realKey+`"}}`)

	cfg, err := Load(noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CredentialsPath != creds {
		t.Errorf("CredentialsPath = %q, want %q", cfg.CredentialsPath, creds)
	}
	key, source := cfg.APIKey(agent.ProviderAnthropic)
	// Compared, never printed: a broken sandbox would splash a real key
	if key.Reveal() != realKey {
		t.Error("the key is not the one in credentials.json")
	}
	if source != KeyFromCredentials {
		t.Errorf("source = %v, want KeyFromCredentials", source)
	}
}

// Nothing connected yet is how every first start begins, not an error: the
// session is what offers to connect a provider.
func TestLoadNeedsNoCredential(t *testing.T) {
	writeConfig(t, `{}`)

	cfg, err := Load(noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if key, source := cfg.APIKey(agent.ProviderAnthropic); key != "" || source != KeyMissing {
		t.Errorf("APIKey = %v, %v, want none", key, source)
	}
}

func TestTheEnvironmentWinsOverTheCredentialsFile(t *testing.T) {
	file := writeConfig(t, `{}`)
	writeCredentials(t, file, `{"openai":{"api_key":"from-file"}}`)

	cfg, err := Load(env(map[string]string{"OPENAI_API_KEY": realKey}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	key, source := cfg.APIKey(agent.ProviderOpenAI)
	if key.Reveal() != realKey {
		t.Error("the key is not the one from the environment")
	}
	if source != KeyFromEnv {
		t.Errorf("source = %v, want KeyFromEnv", source)
	}
}

// An empty variable is how a key is commonly cleared in a shell, and must not
// hide the stored one behind nothing.
func TestAnEmptyVariableDoesNotHideTheStoredKey(t *testing.T) {
	file := writeConfig(t, `{}`)
	writeCredentials(t, file, `{"openai":{"api_key":"`+realKey+`"}}`)

	cfg, err := Load(env(map[string]string{"OPENAI_API_KEY": ""}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if key, source := cfg.APIKey(agent.ProviderOpenAI); key.Reveal() != realKey || source != KeyFromCredentials {
		t.Errorf("source = %v, want the stored key", source)
	}
}

// Load reads the environment it is handed and no other, so a key set in the
// real one cannot slip in past it.
func TestLoadReadsOnlyTheEnvironmentItIsGiven(t *testing.T) {
	writeConfig(t, `{}`)
	t.Setenv("ANTHROPIC_API_KEY", realKey)

	cfg, err := Load(noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if key, _ := cfg.APIKey(agent.ProviderAnthropic); key != "" {
		t.Error("Load read the process environment")
	}
}

// ChatGPT is signed in to, not keyed: no variable can stand in for its login.
func TestChatGPTHasNoAPIKeyVariable(t *testing.T) {
	if got := APIKeyEnvVar(agent.ProviderChatGPT); got != "" {
		t.Errorf("APIKeyEnvVar(chatgpt) = %q, want none", got)
	}
	if got := APIKeyEnvVar(agent.ProviderAnthropic); got != "ANTHROPIC_API_KEY" {
		t.Errorf("APIKeyEnvVar(anthropic) = %q", got)
	}
}

// Keys used to live in config.json. They are not read from there any more,
// whatever the file still says: a key is connected or it is not.
func TestLoadIgnoresAKeyLeftInTheConfigFile(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"`+realKey+`"}`)

	cfg, err := Load(noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if key, source := cfg.APIKey(agent.ProviderAnthropic); key != "" || source != KeyMissing {
		t.Errorf("source = %v, want no key: config.json is not where keys are", source)
	}
}

func TestLoadWarnsAboutCredentialsOthersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits mean nothing on Windows")
	}
	file := writeConfig(t, `{}`)
	creds := writeCredentials(t, file, `{}`)
	if err := os.Chmod(creds, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("Warnings = %q, want one", cfg.Warnings)
	}
	for _, want := range []string{"credentials.json", "0644", "chmod 600 " + creds} {
		if !strings.Contains(cfg.Warnings[0], want) {
			t.Errorf("warning = %q, want it to mention %q", cfg.Warnings[0], want)
		}
	}
}

func TestLoadDoesNotWarnAboutPrivateFiles(t *testing.T) {
	file := writeConfig(t, `{}`)
	writeCredentials(t, file, `{}`)

	cfg, err := Load(noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none", cfg.Warnings)
	}
}

func TestSavedCredentialsAreReadBack(t *testing.T) {
	file := path.Join(t.TempDir(), "elencode", "credentials.json")
	creds := Credentials{agent.ProviderOpenAI: {APIKey: realKey}}

	if err := creds.Save(file); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadCredentials(file)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if loaded[agent.ProviderOpenAI].APIKey.Reveal() != realKey {
		t.Error("the saved key did not come back")
	}
}

func TestSavedCredentialsAreReadableByTheOwnerOnly(t *testing.T) {
	file := path.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(file, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (Credentials{agent.ProviderOpenAI: {APIKey: realKey}}).Save(file); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

// Rewritten whole, and only by elencode: a provider left out is a provider
// disconnected, and nothing else is in the file to keep.
func TestSaveReplacesTheCredentialsFile(t *testing.T) {
	file := path.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(file, []byte(`{"anthropic":{"api_key":"old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (Credentials{agent.ProviderOpenAI: {APIKey: realKey}}).Save(file); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadCredentials(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded[agent.ProviderAnthropic]; ok {
		t.Error("Save kept a provider it was not given")
	}
}

func TestAMissingCredentialsFileIsEmpty(t *testing.T) {
	creds, err := LoadCredentials(path.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if len(creds) != 0 {
		t.Errorf("credentials = %v, want none", creds)
	}
}

// With and Without hand back a changed copy: the credentials a session loaded
// may still be read elsewhere while the new ones are saved.
func TestWithAndWithoutLeaveTheOriginalAlone(t *testing.T) {
	original := Credentials{agent.ProviderAnthropic: {APIKey: "a"}}

	added := original.With(agent.ProviderOpenAI, Credential{APIKey: "o"})
	removed := original.Without(agent.ProviderAnthropic)

	if len(original) != 1 {
		t.Errorf("original = %v, want it unchanged", original)
	}
	if len(added) != 2 || added[agent.ProviderOpenAI].APIKey != "o" {
		t.Errorf("With = %v", added)
	}
	if len(removed) != 0 {
		t.Errorf("Without = %v", removed)
	}
}

// Connecting a provider needs none of the settings, so a mistake in them must
// not stand in its way.
func TestLoadConnectionsDoesNotReadTheConfigFile(t *testing.T) {
	file := writeConfig(t, `{"thinking_effort":"turbo"}`)
	creds := writeCredentials(t, file, `{"openai":{"api_key":"sk-oai"}}`)

	cfg, err := LoadConnections(noEnv)
	if err != nil {
		t.Fatalf("LoadConnections: %v", err)
	}
	if cfg.CredentialsPath != creds {
		t.Errorf("CredentialsPath = %q, want %q", cfg.CredentialsPath, creds)
	}
	if _, source := cfg.APIKey(agent.ProviderOpenAI); source != KeyFromCredentials {
		t.Errorf("openai key source = %v, want the credentials file", source)
	}
	if _, source := cfg.APIKey(agent.ProviderAnthropic); source != KeyMissing {
		t.Errorf("anthropic key source = %v, want none: config.json is not where keys are", source)
	}
}

// The ChatGPT login is a credential like any other, kept with the keys.
func TestTheChatGPTLoginIsKeptWithTheKeys(t *testing.T) {
	file := writeConfig(t, `{}`)
	writeCredentials(t, file, `{
		"anthropic": {"api_key": "sk-ant"},
		"chatgpt": {"login": {"access_token": "a", "refresh_token": "r", "id_token": "i", "account_id": "acct"}}
	}`)

	cfg, err := Load(noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	login := cfg.Credentials[agent.ProviderChatGPT].Login
	if login == nil || login.RefreshToken != "r" || login.AccountID != "acct" {
		t.Errorf("login = %+v, want the saved one", login)
	}
}

// Where the login used to be kept is not read: a provider is connected in
// credentials.json, or it is not.
func TestAChatGPTFileBesideTheConfigIsNotRead(t *testing.T) {
	file := writeConfig(t, `{}`)
	body := `{"access_token":"a","refresh_token":"r","account_id":"acct"}`
	if err := os.WriteFile(path.Join(path.Dir(file), "chatgpt.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Credentials[agent.ProviderChatGPT].Login != nil {
		t.Error("a login was read from chatgpt.json")
	}
}

// SaveCredential changes one provider's entry in the file as it is now, so a
// key another session saved since this one loaded the file survives it.
func TestSaveCredentialChangesOnlyItsProvider(t *testing.T) {
	file := path.Join(t.TempDir(), "credentials.json")
	if err := (Credentials{agent.ProviderOpenAI: {APIKey: "sk-oai"}}).Save(file); err != nil {
		t.Fatal(err)
	}

	saved, err := SaveCredential(file, agent.ProviderAnthropic, Credential{APIKey: "sk-ant"})
	if err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	onDisk, err := LoadCredentials(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, creds := range []Credentials{saved, onDisk} {
		if creds[agent.ProviderOpenAI].APIKey != "sk-oai" || creds[agent.ProviderAnthropic].APIKey != "sk-ant" {
			t.Errorf("credentials = %v, want both keys", creds)
		}
	}
}

func TestRemoveCredentialChangesOnlyItsProvider(t *testing.T) {
	file := path.Join(t.TempDir(), "credentials.json")
	if err := (Credentials{agent.ProviderOpenAI: {APIKey: "sk-oai"}, agent.ProviderAnthropic: {APIKey: "sk-ant"}}).Save(file); err != nil {
		t.Fatal(err)
	}

	left, removed, err := RemoveCredential(file, agent.ProviderAnthropic)
	if err != nil || !removed {
		t.Fatalf("RemoveCredential = %v, %v", removed, err)
	}
	if _, ok := left[agent.ProviderAnthropic]; ok || left[agent.ProviderOpenAI].APIKey != "sk-oai" {
		t.Errorf("left = %v, want only openai", left)
	}
	if _, removed, _ := RemoveCredential(file, agent.ProviderAnthropic); removed {
		t.Error("removed a credential that was not there")
	}
}
