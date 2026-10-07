package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
)

// Credentials are the secrets for each provider, kept apart from the settings
// so config.json holds nothing worth stealing.
type Credentials map[agent.ProviderName]Credential

// Credential is what one provider is reached with: an API key, or for
// ChatGPT the login signing in left, renewed while a session runs.
type Credential struct {
	APIKey Secret          `json:"api_key,omitempty"`
	Login  *chatgpt.Tokens `json:"login,omitempty"`
}

// credentialsFileMode keeps the file readable by its owner only: it holds keys
const credentialsFileMode = 0o600

const credentialsFileName = "credentials.json"

// CredentialsPath is where the credentials are kept: beside the config file.
func CredentialsPath() (string, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return path.Join(userConfigDir, configDirectoryName, credentialsFileName), nil
}

// LoadCredentials reads the credentials saved at path. A missing file is no
// credentials, not an error: nothing has been connected yet.
func LoadCredentials(path string) (Credentials, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, nil
	}
	if err != nil {
		return nil, err
	}
	creds := Credentials{}
	if err := json.Unmarshal(body, &creds); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return creds, nil
}

// Save writes the credentials to path, replacing what was there: only
// elencode writes the file, so there is nothing in it to merge with.
//
// It writes a temporary file beside it and renames that over the old one, so
// a crash halfway leaves the old file whole rather than half a file of keys.
// The temporary file is created readable by its owner only, so the keys are
// never on disk with wider permissions, not even for a moment.
func (c Credentials) Save(path string) error {
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, credentialsFileName+".*")
	if err != nil {
		return err
	}
	// A no-op once the rename has moved it
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(credentialsFileMode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// SaveCredential sets provider's credential in the file at path, and returns
// what the file now holds. The rest is kept as it is in the file now, not as
// any session loaded it: another elencode may have connected a provider since,
// and writing an older copy back would undo that.
func SaveCredential(path string, provider agent.ProviderName, cred Credential) (Credentials, error) {
	current, err := LoadCredentials(path)
	if err != nil {
		return nil, err
	}
	creds := current.With(provider, cred)
	if err := creds.Save(path); err != nil {
		return nil, err
	}
	return creds, nil
}

// RemoveCredential takes provider's credential out of the file at path, the
// rest kept as SaveCredential keeps it. removed reports whether there was one.
func RemoveCredential(path string, provider agent.ProviderName) (left Credentials, removed bool, err error) {
	current, err := LoadCredentials(path)
	if err != nil {
		return nil, false, err
	}
	if _, ok := current[provider]; !ok {
		return current, false, nil
	}
	creds := current.Without(provider)
	if err := creds.Save(path); err != nil {
		return nil, false, err
	}
	return creds, true, nil
}

// With is a copy of c with provider's credential set to cred.
func (c Credentials) With(provider agent.ProviderName, cred Credential) Credentials {
	changed := maps.Clone(c)
	if changed == nil {
		changed = Credentials{}
	}
	changed[provider] = cred
	return changed
}

// Without is a copy of c with provider's credential gone.
func (c Credentials) Without(provider agent.ProviderName) Credentials {
	changed := maps.Clone(c)
	delete(changed, provider)
	return changed
}
