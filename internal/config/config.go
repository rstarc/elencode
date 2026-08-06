package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
)

// Config represents the configuration options exposed to the user
type Config struct {
	// Every field is omitempty so Save can merge into the file: an unset value
	// leaves whatever the file already said in place, rather than blanking it.
	AnthropicAPIKey Secret `json:"anthropic_api_key,omitempty"`
	// Model the provider should use. Empty means the provider's own default.
	Model string `json:"model,omitempty"`
	// ThinkingEnabled asks the provider for the model's reasoning. Not
	// omitempty, unlike the rest: false is a bool's zero value, so omitting it
	// would make turning thinking off indistinguishable from never setting it,
	// and a save would drop it back to the default.
	ThinkingEnabled bool `json:"thinking_enabled"`
	// Provenance, filled in by Load and never read from the file, so the config
	// view can say where a value came from.
	Path          string `json:"-"` // config file Load read
	APIKeyFromEnv bool   `json:"-"` // the environment overrode the file value
}

// Save writes the configuration back to c.Path.
//
// It merges into what is already on disk rather than replacing it, so a file
// holding settings this version does not know about survives a save. A key the
// environment supplied is dropped first: persisting it would put a secret in
// the file the user never chose to store there, and pin it even after the
// environment moved on.
func (c Config) Save() error {
	if c.APIKeyFromEnv {
		c.AnthropicAPIKey = ""
	}

	updates, err := json.Marshal(c)
	if err != nil {
		return err
	}

	settings, err := readSettings(c.Path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(updates, &settings); err != nil {
		return err
	}

	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.Path, append(body, '\n'), configFileMode); err != nil {
		return err
	}
	return os.Chmod(c.Path, configFileMode)
}

// readSettings decodes the config file as raw JSON keys, so Save can write back
// the ones it does not have a field for. A missing file is an empty set: the
// first save writes it.
func readSettings(path string) (map[string]json.RawMessage, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}

	settings := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &settings); err != nil {
		return nil, err
	}
	return settings, nil
}

// configFileMode keeps the file readable by its owner only: it holds an API key
const configFileMode = 0o600

const configDirectoryName = "elencode"

const ANTHROPIC_API_KEY_ENV_VAR_NAME = "ANTHROPIC_API_KEY"

// Load loads the configuration file ($XDG_CONFIG_HOME/elencode/config.json) from disk
// and unmarshals the contents into a Config, then reads any environment variables to check if they override any of the values
func Load() (Config, error) {
	// Defaults first: the file is unmarshalled over them, so a setting it does
	// not mention keeps the value here rather than a zero one.
	cfg := Config{ThinkingEnabled: true}

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
