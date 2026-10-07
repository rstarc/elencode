package chatgpt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// refreshWindow is how long before its expiry a token is renewed, so it cannot
// lapse between being handed out and the request it was for arriving.
const refreshWindow = 5 * time.Minute

// Complete reports what a login lacks to make a request, if anything: a saved
// one is checked when it is loaded rather than on the first message.
func (t Tokens) Complete() error {
	if t.AccessToken == "" || t.RefreshToken == "" || t.AccountID == "" {
		return errors.New("the ChatGPT login is incomplete: run `elencode connect chatgpt` again")
	}
	return nil
}

// Source hands out a current access token, renewing the login when it is
// about to expire and handing the renewal to save, which keeps it wherever the
// login came from.
// Safe for concurrent use: a turn's goroutine asks for it, and the next turn
// may already be asking.
type Source struct {
	oauth OAuth
	save  func(Tokens) error
	now   func() time.Time

	mu     sync.Mutex
	tokens Tokens
}

func NewSource(tokens Tokens, oauth OAuth, save func(Tokens) error) *Source {
	return &Source{oauth: oauth, save: save, now: time.Now, tokens: tokens}
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
	if err := s.save(renewed); err != nil {
		return "", "", fmt.Errorf("saving the refreshed ChatGPT login: %w", err)
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
