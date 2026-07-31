package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
)

// Config represents the configuration options exposed to the user
type Config struct {
	AnthropicAPIKey string `json:"anthropic_api_key"`
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
	if val, ok := os.LookupEnv(ANTHROPIC_API_KEY_ENV_VAR_NAME); ok {
		cfg.AnthropicAPIKey = val
	}

	if cfg.AnthropicAPIKey == "" {
		return cfg, fmt.Errorf("API key not set: provide %q in the environment or %q in %q", ANTHROPIC_API_KEY_ENV_VAR_NAME, "anthropic_api_key", configFilePath)
	}

	return cfg, nil
}
