package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// tokenFileMode keeps the login readable by its owner only, as the config file
// is: it is a credential.
const tokenFileMode = 0o600

// refreshWindow is how long before its expiry a token is renewed, so it cannot
// lapse between being handed out and the request it was for arriving.
const refreshWindow = 5 * time.Minute

// LoadTokens reads a saved login. A missing file comes back as os.ErrNotExist,
// which is simply not being signed in.
func LoadTokens(path string) (Tokens, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Tokens{}, err
	}

	var tokens Tokens
	if err := json.Unmarshal(body, &tokens); err != nil {
		return Tokens{}, fmt.Errorf("reading the ChatGPT login in %s: %w", path, err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.AccountID == "" {
		return Tokens{}, fmt.Errorf("the ChatGPT login in %s is incomplete: run `elencode login chatgpt` again", path)
	}
	return tokens, nil
}

// SaveTokens writes a login to path, creating its directory if this is the
// first one.
func SaveTokens(path string, tokens Tokens) error {
	body, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(body, '\n'), tokenFileMode); err != nil {
		return err
	}
	return os.Chmod(path, tokenFileMode)
}

// Source hands out a current access token, renewing the login when it is
// about to expire and writing the renewal back to where it was loaded from.
// Safe for concurrent use: a turn's goroutine asks for it, and the next turn
// may already be asking.
type Source struct {
	path  string
	oauth OAuth
	now   func() time.Time

	mu     sync.Mutex
	tokens Tokens
}

func NewSource(path string, tokens Tokens, oauth OAuth) *Source {
	return &Source{path: path, oauth: oauth, now: time.Now, tokens: tokens}
}

// Credentials is what a request to the backend needs: the bearer token and the
// account it is for. The lock is held across the refresh, so callers that find
// the same expired token wait for one renewal rather than each spending the
// refresh token, which only works once.
func (s *Source) Credentials(ctx context.Context) (accessToken, accountID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.expiring() {
		return s.tokens.AccessToken, s.tokens.AccountID, nil
	}

	renewed, err := s.oauth.refresh(ctx, s.tokens)
	if err != nil {
		return "", "", err
	}
	// Kept before saving: the old refresh token is spent now, whatever happens
	// to the file.
	s.tokens = renewed
	if err := SaveTokens(s.path, renewed); err != nil {
		return "", "", fmt.Errorf("saving the refreshed ChatGPT login to %s: %w", s.path, err)
	}
	return renewed.AccessToken, renewed.AccountID, nil
}

// expiring reports whether the access token is within refreshWindow of its
// expiry. One that names no expiry is used until the backend turns it down.
func (s *Source) expiring() bool {
	exp, ok := expiry(s.tokens.AccessToken)
	if !ok {
		return false
	}
	return !s.now().Add(refreshWindow).Before(exp)
}
