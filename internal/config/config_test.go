package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
)

const realKey = "sk-ant-secret-value"

// TestSecretMasksInEveryFormatting covers the verbs a caller might reach for by
// habit. A Secret that masked only under %s would still leak through %v in a
// log line.
func TestSecretMasksInEveryFormatting(t *testing.T) {
	s := Secret(realKey)

	for _, format := range []string{"%s", "%v", "%q", "%#v", "%+v"} {
		t.Run(format, func(t *testing.T) {
			if got := fmt.Sprintf(format, s); strings.Contains(got, realKey) {
				t.Errorf("Sprintf(%q, secret) = %s, want the value masked", format, got)
			}
		})
	}
}

func TestSecretMasksAFixedLength(t *testing.T) {
	// A mask that tracked the real length would leak it
	short, long := Secret("ab"), Secret(strings.Repeat("x", 200))

	if short.String() != long.String() {
		t.Errorf("masks differ by value length: %q vs %q", short.String(), long.String())
	}
}

func TestSecretRevealReturnsTheRealValue(t *testing.T) {
	if got := Secret(realKey).Reveal(); got != realKey {
		t.Errorf("Reveal() = %q, want %q", got, realKey)
	}
}

func TestEmptySecretReadsAsUnset(t *testing.T) {
	// Masking an empty value would claim a key is configured when none is
	if got := Secret("").String(); got != "(unset)" {
		t.Errorf("empty Secret prints %q, want %q", got, "(unset)")
	}
}

func TestSecretUnmarshalsFromJSON(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"anthropic_api_key":"`+realKey+`"}`), &cfg); err != nil {
		t.Fatalf("unmarshalling config: %v", err)
	}

	if got := cfg.AnthropicAPIKey.Reveal(); got != realKey {
		t.Errorf("AnthropicAPIKey = %q, want %q", got, realKey)
	}
}

// writeConfig redirects os.UserConfigDir at a temp dir holding the given file
// body and returns the path Load should read.
//
// Both variables are set because os.UserConfigDir is platform specific: it reads
// XDG_CONFIG_HOME on Linux but $HOME/Library/Application Support on macOS.
// Setting only one lets the test escape its sandbox and read the developer's own
// config file, which is how this helper was wrong the first time.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("resolving the user config dir: %v", err)
	}
	if !strings.HasPrefix(userConfigDir, dir) {
		t.Fatalf("os.UserConfigDir() = %q, which is outside the temp dir %q", userConfigDir, dir)
	}

	configDir := path.Join(userConfigDir, configDirectoryName)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	file := path.Join(configDir, "config.json")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return file
}

func TestLoadRecordsFileAsTheSource(t *testing.T) {
	file := writeConfig(t, `{"anthropic_api_key":"`+realKey+`"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.AnthropicKeyFromEnv {
		t.Error("AnthropicKeyFromEnv is true, want false when only the file sets the key")
	}
	if cfg.Path != file {
		t.Errorf("Path = %q, want %q", cfg.Path, file)
	}
	// Compared, never printed: a broken test sandbox would otherwise splash the
	// developer's own key across the output.
	if cfg.AnthropicAPIKey.Reveal() != realKey {
		t.Error("AnthropicAPIKey is not the value from the file")
	}
}

func TestLoadRecordsEnvironmentAsTheSource(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"from-file"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, realKey)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.AnthropicKeyFromEnv {
		t.Error("AnthropicKeyFromEnv is false, want true when the environment overrides the file")
	}
	if cfg.AnthropicAPIKey.Reveal() != realKey {
		t.Error("AnthropicAPIKey is not the value from the environment")
	}
}

// TestLoadReportsThePathWhenItFails covers the caller that wants to say which
// file was wrong, which is only possible if Path survives the error.
func TestLoadReportsThePathWhenItFails(t *testing.T) {
	file := writeConfig(t, `{"anthropic_api_key":""}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err == nil {
		t.Fatal("Load succeeded with no API key set, want an error")
	}
	if cfg.Path != file {
		t.Errorf("Path = %q, want %q even on failure", cfg.Path, file)
	}
}

func TestLoadReadsTheModel(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"`+realKey+`","model":"claude-sonnet-4-5"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q, want %q", cfg.Model, "claude-sonnet-4-5")
	}
}

func TestSaveIsReadBackByLoad(t *testing.T) {
	file := writeConfig(t, `{"anthropic_api_key":"`+realKey+`"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg := Config{Path: file, Model: "claude-opus-4-5"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Model != "claude-opus-4-5" {
		t.Errorf("Model = %q, want the value Save wrote", loaded.Model)
	}
}

// TestSaveKeepsSettingsItDoesNotKnowAbout guards against a wholesale rewrite
// dropping keys a later version of elencode wrote.
func TestSaveKeepsSettingsItDoesNotKnowAbout(t *testing.T) {
	file := writeConfig(t, `{"anthropic_api_key":"`+realKey+`","future_setting":42}`)

	cfg := Config{Path: file, AnthropicAPIKey: realKey, Model: "claude-opus-4-5"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	saved := readConfig(t, file)
	if saved["future_setting"] != float64(42) {
		t.Errorf("Save dropped an unknown setting: %v", saved)
	}
	if saved["anthropic_api_key"] != realKey {
		t.Error("Save lost the API key")
	}
}

// TestSaveDoesNotWriteTheEnvironmentsAPIKey keeps a key that was only ever
// meant to live in the environment out of the file: the in-memory Config holds
// the override, so saving it back would persist a secret the user never put
// there, and pin it even after the environment changes.
func TestSaveDoesNotWriteTheEnvironmentsAPIKey(t *testing.T) {
	const fileKey = "sk-ant-from-the-file"
	file := writeConfig(t, `{"anthropic_api_key":"`+fileKey+`"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, realKey)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Model = "claude-opus-4-5"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	saved := readConfig(t, file)
	if saved["anthropic_api_key"] == realKey {
		t.Error("Save wrote the environment's API key into the config file")
	}
	if saved["anthropic_api_key"] != fileKey {
		t.Errorf("Save did not keep the file's own API key: %v", saved["anthropic_api_key"])
	}
}

func TestSaveKeepsTheFilePrivate(t *testing.T) {
	file := writeConfig(t, `{"anthropic_api_key":"`+realKey+`"}`)

	cfg := Config{Path: file, AnthropicAPIKey: realKey, Model: "claude-opus-4-5"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// The file holds an API key, so a rewrite must not widen its permissions
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file mode = %o, want 600", perm)
	}
}

func TestSaveRepairsPermissiveFilePermissions(t *testing.T) {
	file := writeConfig(t, `{"anthropic_api_key":"`+realKey+`","future_setting":42}`)
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatalf("making config permissive: %v", err)
	}

	cfg := Config{Path: file, AnthropicAPIKey: realKey, Model: "claude-opus-4-5"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file mode = %o, want 600", perm)
	}
	if saved := readConfig(t, file); saved["future_setting"] != float64(42) {
		t.Errorf("Save dropped an unknown setting: %v", saved)
	}
}

// readConfig decodes the config file as plain JSON, so a test can assert on
// what is actually on disk rather than on what Load makes of it.
func readConfig(t *testing.T, file string) map[string]any {
	t.Helper()

	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading config back: %v", err)
	}
	settings := map[string]any{}
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatalf("saved config is not valid JSON: %v\n%s", err, body)
	}
	return settings
}

// TestThinkingIsEnabledByDefault covers a config file written before the
// setting existed: an absent JSON bool unmarshals to false, so the default has
// to be in place before the file is read rather than after.
func TestThinkingIsEnabledByDefault(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"`+realKey+`"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.ThinkingEnabled {
		t.Error("thinking is off with nothing in the config file, want it on by default")
	}
}

func TestThinkingCanBeTurnedOff(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"`+realKey+`","thinking_enabled":false}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ThinkingEnabled {
		t.Error("thinking is on, want the config file's false to win over the default")
	}
}

// TestSaveKeepsThinkingOff guards the round trip: false is a bool's zero value,
// so a save that treated it as "unset" would quietly turn thinking back on.
func TestSaveKeepsThinkingOff(t *testing.T) {
	file := writeConfig(t, `{"anthropic_api_key":"`+realKey+`","thinking_enabled":false}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Model = "claude-opus-4-5"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if saved := readConfig(t, file); saved["thinking_enabled"] != false {
		t.Errorf("thinking_enabled = %v after saving, want it still off", saved["thinking_enabled"])
	}
}

func TestLoadReadsTheOpenAIProviderSettings(t *testing.T) {
	writeConfig(t, `{"provider":"openai","openai_api_key":"sk-oai","thinking_effort":"high"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")
	t.Setenv(OPENAI_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider != ProviderOpenAI || cfg.OpenAIAPIKey.Reveal() != "sk-oai" || cfg.ThinkingEffort != "high" {
		t.Fatalf("provider = %q, thinking_effort = %q, openai key set = %t", cfg.Provider, cfg.ThinkingEffort, cfg.OpenAIAPIKey != "")
	}
}

// TestLoadValidatesTheSelectedProvidersKey: an anthropic key on file must not
// satisfy the check when openai is the selected provider.
func TestLoadValidatesTheSelectedProvidersKey(t *testing.T) {
	writeConfig(t, `{"provider":"openai","anthropic_api_key":"`+realKey+`"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")
	t.Setenv(OPENAI_API_KEY_ENV_VAR_NAME, "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded without the selected provider's key")
	}
	if !strings.Contains(err.Error(), OPENAI_API_KEY_ENV_VAR_NAME) {
		t.Errorf("err = %q, want it to name the openai key", err)
	}
}

func TestLoadDefaultsProviderToAnthropic(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"`+realKey+`"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider != ProviderAnthropic {
		t.Fatalf("provider = %q, want anthropic", cfg.Provider)
	}
	if cfg.ThinkingEffort != "medium" {
		t.Fatalf("thinking_effort = %q, want the medium default", cfg.ThinkingEffort)
	}
}

func TestLoadAppliesTheOpenAIKeyFromTheEnvironment(t *testing.T) {
	writeConfig(t, `{"provider":"openai","openai_api_key":"from-file"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")
	t.Setenv(OPENAI_API_KEY_ENV_VAR_NAME, "sk-oai-env")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAIAPIKey.Reveal() != "sk-oai-env" {
		t.Error("OpenAIAPIKey is not the value from the environment")
	}
	if !cfg.OpenAIKeyFromEnv {
		t.Error("OpenAIKeyFromEnv is false, want true")
	}
}

// TestSaveWritesNeitherEnvironmentKey: with openai selected and BOTH env vars
// set, Save must not persist either environment secret — including the key of
// the provider that is not selected. One shared provenance bool cannot express
// this, which is why each key tracks its own.
func TestSaveWritesNeitherEnvironmentKey(t *testing.T) {
	file := writeConfig(t, `{"provider":"openai","anthropic_api_key":"ant-file","openai_api_key":"oai-file"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "ant-env")
	t.Setenv(OPENAI_API_KEY_ENV_VAR_NAME, "oai-env")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	saved := readConfig(t, file)
	if saved["anthropic_api_key"] != "ant-file" {
		t.Errorf("anthropic_api_key = %v, want the file's own value kept", saved["anthropic_api_key"])
	}
	if saved["openai_api_key"] != "oai-file" {
		t.Errorf("openai_api_key = %v, want the file's own value kept", saved["openai_api_key"])
	}
}

func TestLoadRejectsUnknownProvider(t *testing.T) {
	writeConfig(t, `{"provider":"nope","anthropic_api_key":"`+realKey+`"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an unknown provider")
	}
}

// TestLoadRejectsUnknownThinkingEffort: a typo like "hihg" must fail loudly
// rather than silently clamping to medium.
func TestLoadRejectsUnknownThinkingEffort(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"`+realKey+`","thinking_effort":"turbo"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an unknown thinking_effort")
	}
}

// TestLoadNormalizesExplicitlyEmptySettings: the file is unmarshalled over the
// defaults, so a key written as "" overwrites the default with nothing rather
// than leaving it alone. Both settings have to be put back, or an empty
// provider fails validation and an empty effort reaches the provider.
func TestLoadNormalizesExplicitlyEmptySettings(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"`+realKey+`","provider":"","thinking_effort":""}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Provider != ProviderAnthropic {
		t.Errorf("provider = %q, want the anthropic default", cfg.Provider)
	}
	if cfg.ThinkingEffort != "medium" {
		t.Errorf("thinking_effort = %q, want the medium default", cfg.ThinkingEffort)
	}
}

// TestSaveCreatesTheFileWhenItDoesNotExist covers the first-ever save: reading
// the existing settings to merge into must treat a missing file as empty
// rather than failing.
func TestSaveCreatesTheFileWhenItDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	file := path.Join(dir, "config.json")

	cfg := Config{Path: file, AnthropicAPIKey: realKey, Model: "claude-opus-4-5"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	saved := readConfig(t, file)
	if saved["model"] != "claude-opus-4-5" {
		t.Errorf("model = %v, want it written", saved["model"])
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	// The file holds an API key, so it must not be readable by anyone else.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}
