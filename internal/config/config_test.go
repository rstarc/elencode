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

	if cfg.APIKeyFromEnv {
		t.Error("APIKeyFromEnv is true, want false when only the file sets the key")
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

	if !cfg.APIKeyFromEnv {
		t.Error("APIKeyFromEnv is false, want true when the environment overrides the file")
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
