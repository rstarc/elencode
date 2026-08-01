package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
)

// Secret is a config value that must never be shown. Formatting one yields a
// mask, so it stays hidden wherever it ends up: a log line, %v in an error, the
// config view. Reveal is the only way to the real value, which makes the places
// that need it easy to find.
//
// It deliberately does not implement MarshalJSON. Masking on display is safe;
// masking on serialize would silently write bullets over a real key the first
// time anything saves a Config back to disk.
type Secret string

// secretMask is a fixed length: one that tracked the value would leak it
const secretMask = "••••••••"

func (s Secret) String() string {
	if s == "" {
		return "(unset)"
	}
	return secretMask
}

// GoString masks under %#v, which otherwise prints the underlying string
func (s Secret) GoString() string { return s.String() }

// Reveal returns the unmasked value
func (s Secret) Reveal() string { return string(s) }

// Config represents the configuration options exposed to the user
type Config struct {
	AnthropicAPIKey Secret `json:"anthropic_api_key"`
	// Provenance, filled in by Load and never read from the file, so the config
	// view can say where a value came from.
	Path          string `json:"-"` // config file Load read
	APIKeyFromEnv bool   `json:"-"` // the environment overrode the file value
}

const configDirectoryName = "elencode"

const ANTHROPIC_API_KEY_ENV_VAR_NAME = "ANTHROPIC_API_KEY"

// Load loads the configuration file ($XDG_CONFIG_HOME/elencode/config.json) from disk
// and unmarshals the contents into a Config, then reads any environment variables to check if they override any of the values
func Load() (Config, error) {

	cfg := Config{}

	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return cfg, err
	}

	configFilePath := path.Join(userConfigDir, configDirectoryName, "config.json")
	// Set before the file is read so a failing Load still reports which path was wrong
	cfg.Path = configFilePath
	// TOOD: Warn if file is world-readable

	configFileBytes, err := os.ReadFile(configFilePath)
	// TODO: if file not found, create it and start authentication prompt
	if err != nil {
		return cfg, err
	}

	// Unmarshal file into Config
	err = json.Unmarshal(configFileBytes, &cfg)
	if err != nil {
		return cfg, err
	}

	// Apply environment variable overrides
	if val, ok := os.LookupEnv(ANTHROPIC_API_KEY_ENV_VAR_NAME); ok && val != "" {
		cfg.AnthropicAPIKey = Secret(val)
		cfg.APIKeyFromEnv = true
	}

	if cfg.AnthropicAPIKey == "" {
		return cfg, fmt.Errorf("API key not set: provide %q in the environment or %q in %q", ANTHROPIC_API_KEY_ENV_VAR_NAME, "anthropic_api_key", configFilePath)
	}

	return cfg, nil
}
