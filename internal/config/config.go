package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
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
	//
	// Model a session talks to, which is also what says who to talk to: every
	// provider that is connected is loaded, and a model belongs to one of
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
	// Found by Load and never read from the file, so the config view can say
	// where things are.
	Path            string `json:"-"` // config file Load read
	CredentialsPath string `json:"-"` // where credentials are kept, whether or not there are any
	// Credentials are the API keys and the ChatGPT login saved in
	// CredentialsPath. Keys are read through APIKey, which knows when the
	// environment wins over them.
	Credentials Credentials `json:"-"`
	// Warnings are what Load found worth saying but not worth refusing to
	// start over, such as a file of secrets others can read.
	Warnings []string `json:"-"`
	// Env is the environment Load was given, kept for APIKey. Nil reads as an
	// empty one, so a Config built by hand has no keys but its Credentials.
	Env Env `json:"-"`
}

// Env looks up an environment variable: os.LookupEnv in main, a map in tests.
// The only way into the environment for anything that decides which key a
// request carries, so what elencode reads is what is listed here, and not
// whatever a library helps itself to.
type Env func(name string) (string, bool)

// KeySource says where a provider's API key was found, for the views that
// show it: a key from the environment is not the one in the file, and cannot
// be removed by elencode.
type KeySource int

const (
	KeyMissing KeySource = iota
	KeyFromEnv
	KeyFromCredentials
)

// apiKeyEnvVars are the variables that supply a provider's key, which win
// over credentials.json. ChatGPT is signed in to, so nothing stands in for it.
var apiKeyEnvVars = map[agent.ProviderName]string{
	agent.ProviderAnthropic: "ANTHROPIC_API_KEY",
	agent.ProviderOpenAI:    "OPENAI_API_KEY",
}

// APIKeyEnvVar names the variable that supplies provider's key, and is empty
// for a provider that is not reached with one.
func APIKeyEnvVar(provider agent.ProviderName) string {
	return apiKeyEnvVars[provider]
}

// APIKey is provider's key and where it came from. Its variable wins over
// credentials.json, but only when it holds something: an empty one is how a
// key is commonly cleared in a shell, and must not hide the stored one.
func (c Config) APIKey(provider agent.ProviderName) (Secret, KeySource) {
	if name := APIKeyEnvVar(provider); name != "" && c.Env != nil {
		if val, ok := c.Env(name); ok && val != "" {
			return Secret(val), KeyFromEnv
		}
	}
	if key := c.Credentials[provider].APIKey; key != "" {
		return key, KeyFromCredentials
	}
	return "", KeyMissing
}

// Save writes the configuration back to c.Path.
//
// It merges into what is already on disk rather than replacing it, so a file
// holding settings this version does not know about survives a save.
func (c Config) Save() error {
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
	// "provider" used to name the one API a session talked to. A model names
	// its own now, so the setting does nothing — and a setting that does
	// nothing is worse left in the file than removed, since the next reader
	// will believe it.
	delete(settings, "provider")

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

// configFileMode keeps the file readable by its owner only. It holds no secret
// now, but a copy an older version wrote still may, and a save must not widen
// who can read it.
const configFileMode = 0o600

const configDirectoryName = "elencode"

// Load loads the configuration file ($XDG_CONFIG_HOME/elencode/config.json)
// from disk, with the credentials beside it. Keys are
// looked up in env before the credentials file: see APIKey.
//
// Having nothing connected is not a failure: it is how every first start
// begins, and the session is what offers to connect a provider.
func Load(env Env) (Config, error) {
	cfg, err := LoadConnections(env)
	if err != nil {
		return cfg, err
	}
	// Seeded before the file is unmarshalled over it, which is what a bool
	// needs: false is both "off" and "not mentioned", so a default it does not
	// carry into the unmarshal cannot be applied afterwards. A string can say
	// "not mentioned" for itself, and is defaulted below instead.
	cfg.ThinkingEnabled = true

	// A missing file is the defaults, not an error: connecting a provider
	// writes credentials, never this file, and the first save creates it.
	configFileBytes, err := os.ReadFile(cfg.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}
	if err == nil {
		if err := json.Unmarshal(configFileBytes, &cfg); err != nil {
			return cfg, err
		}
	}

	// Validated against the agent's own vocabulary rather than a copy of it:
	// which levels exist is the agent's to say, and a copy here would be one
	// more place to forget when a level is added.
	if _, ok := agent.ParseEffort(cfg.ThinkingEffort); !ok {
		return cfg, fmt.Errorf("unknown thinking_effort %q in %q (valid: %s)", cfg.ThinkingEffort, cfg.Path, effortList())
	}
	return cfg, nil
}

// LoadConnections is the half of Load that says which providers can be
// reached: the credentials and the environment. It does
// not read config.json: connecting a provider needs none of the settings, so
// a mistake in them must not stand in its way.
func LoadConnections(env Env) (Config, error) {
	cfg := Config{Env: env}

	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return cfg, err
	}
	configDir := path.Join(userConfigDir, configDirectoryName)
	// Set before anything is read so a failing load still reports which path was wrong
	cfg.Path = path.Join(configDir, "config.json")
	cfg.CredentialsPath = path.Join(configDir, credentialsFileName)

	cfg.Credentials, err = LoadCredentials(cfg.CredentialsPath)
	if err != nil {
		return cfg, err
	}

	// Warned about rather than refused: the file is the user's to fix. Not on
	// Windows, where the mode bits mean nothing.
	if info, err := os.Stat(cfg.CredentialsPath); err == nil && runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s can be read by others (mode %04o): run chmod 600 %s",
				credentialsFileName, perm, cfg.CredentialsPath))
		}
	}
	return cfg, nil
}
