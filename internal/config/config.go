package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/rstarc/elencode/internal/agent"
)

// effortList names the levels a user may write, for the error that rejects the
// ones they may not.
func effortList() string {
	names := make([]string, 0, len(agent.Efforts))
	for _, effort := range agent.Efforts {
		names = append(names, string(effort))
	}
	return strings.Join(names, ", ")
}

// Config represents the configuration options exposed to the user
type Config struct {
	// Every field is omitempty so Save can merge into the file: an unset value
	// leaves whatever the file already said in place, rather than blanking it.
	AnthropicAPIKey Secret `json:"anthropic_api_key,omitempty"`
	OpenAIAPIKey    Secret `json:"openai_api_key,omitempty"`
	// Model a session talks to, which is also what says who to talk to: every
	// provider a key was found for is loaded, and a model belongs to one of
	// them. Empty means the default. Stored qualified ("openai/gpt-5") when a
	// bare id would not say who serves it.
	Model string `json:"model,omitempty"`
	// ThinkingEnabled asks the provider for the model's reasoning. Not
	// omitempty, unlike the rest: false is a bool's zero value, so omitting it
	// would make turning thinking off indistinguishable from never setting it,
	// and a save would drop it back to the default.
	ThinkingEnabled bool `json:"thinking_enabled"`
	// ThinkingEffort is how hard an effort-based model reasons, used when
	// ThinkingEnabled is on. Empty means the API's own default rather than a
	// level we picked. Validated by Load: a typo must fail at startup rather
	// than silently run at some other level.
	ThinkingEffort string `json:"thinking_effort,omitempty"`
	// Provenance, filled in by Load and never read from the file, so the config
	// view can say where a value came from. One flag per key: a single bool
	// cannot say *which* key the environment supplied, and Save has to blank
	// exactly those.
	Path                string `json:"-"` // config file Load read
	AnthropicKeyFromEnv bool   `json:"-"` // the environment overrode the file value
	OpenAIKeyFromEnv    bool   `json:"-"`
}

// Save writes the configuration back to c.Path.
//
// It merges into what is already on disk rather than replacing it, so a file
// holding settings this version does not know about survives a save. A key the
// environment supplied is dropped first: persisting it would put a secret in
// the file the user never chose to store there, and pin it even after the
// environment moved on.
func (c Config) Save() error {
	if c.AnthropicKeyFromEnv {
		c.AnthropicAPIKey = ""
	}
	if c.OpenAIKeyFromEnv {
		c.OpenAIAPIKey = ""
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

const OPENAI_API_KEY_ENV_VAR_NAME = "OPENAI_API_KEY"

// Load loads the configuration file ($XDG_CONFIG_HOME/elencode/config.json) from disk
// and unmarshals the contents into a Config, then reads any environment variables to check if they override any of the values
func Load() (Config, error) {
	// Seeded before the file is unmarshalled over it, which is what a bool
	// needs: false is both "off" and "not mentioned", so a default it does not
	// carry into the unmarshal cannot be applied afterwards. A string can say
	// "not mentioned" for itself, and is defaulted below instead.
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

	if val, ok := os.LookupEnv(ANTHROPIC_API_KEY_ENV_VAR_NAME); ok && val != "" {
		cfg.AnthropicAPIKey = Secret(val)
		cfg.AnthropicKeyFromEnv = true
	}
	if val, ok := os.LookupEnv(OPENAI_API_KEY_ENV_VAR_NAME); ok && val != "" {
		cfg.OpenAIAPIKey = Secret(val)
		cfg.OpenAIKeyFromEnv = true
	}

	// One key is enough — it is what decides which providers a session can
	// reach — but with none there is nothing to talk to, and saying so now
	// beats saying it on the first message.
	if cfg.AnthropicAPIKey == "" && cfg.OpenAIAPIKey == "" {
		return cfg, fmt.Errorf("no API key set: provide %q or %q in the environment, or %q or %q in %q",
			ANTHROPIC_API_KEY_ENV_VAR_NAME, OPENAI_API_KEY_ENV_VAR_NAME, "anthropic_api_key", "openai_api_key", configFilePath)
	}

	// Validated against the agent's own vocabulary rather than a copy of it:
	// which levels exist is the agent's to say, and a copy here would be one
	// more place to forget when a level is added.
	if _, ok := agent.ParseEffort(cfg.ThinkingEffort); !ok {
		return cfg, fmt.Errorf("unknown thinking_effort %q in %q (valid: %s)", cfg.ThinkingEffort, configFilePath, effortList())
	}

	return cfg, nil
}
