package chatgpt

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// A login missing any of these cannot make a request, so it is caught when
// it is loaded rather than on the first message.
func TestCompleteRejectsAnIncompleteLogin(t *testing.T) {
	complete := Tokens{AccessToken: "a", RefreshToken: "r", AccountID: "acct"}
	if err := complete.Complete(); err != nil {
		t.Errorf("Complete = %v for a complete login", err)
	}
	for _, missing := range []Tokens{
		{RefreshToken: "r", AccountID: "acct"},
		{AccessToken: "a", AccountID: "acct"},
		{AccessToken: "a", RefreshToken: "r"},
	} {
		if err := missing.Complete(); err == nil || !strings.Contains(err.Error(), "elencode connect chatgpt") {
			t.Errorf("Complete(%+v) = %v, want it to say how to sign in again", missing, err)
		}
	}
}

// fixedNow pins the clock a Source compares expiry against.
func fixedNow(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

var now = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// saves records every login a Source saves, in order.
type saves struct {
	mu     sync.Mutex
	tokens []Tokens
}

func (s *saves) save(tokens Tokens) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = append(s.tokens, tokens)
	return nil
}

func newTestSource(t *testing.T, tokens Tokens, base string) (*Source, *saves) {
	t.Helper()
	saved := &saves{}
	source := NewSource(tokens, OAuth{Issuer: base}, saved.save)
	source.now = fixedNow(now)
	return source, saved
}

func TestCredentialsReturnsAnUnexpiredTokenAsIs(t *testing.T) {
	iss, base := newIssuer(t)
	access := accessToken(t, now.Add(time.Hour))
	source, _ := newTestSource(t, Tokens{AccessToken: access, RefreshToken: "r", AccountID: "acct"}, base)

	token, account, err := source.Credentials(context.Background())
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if token != access || account != "acct" {
		t.Errorf("Credentials = %q, %q", token, account)
	}
	if iss.requests() != 0 {
		t.Errorf("refreshed %d times, want none", iss.requests())
	}
}

func TestCredentialsRefreshesAnExpiredToken(t *testing.T) {
	fresh := accessToken(t, now.Add(time.Hour))
	iss, base := newIssuer(t, granted(fresh, "new-refresh", ""))
	source, _ := newTestSource(t, Tokens{AccessToken: accessToken(t, now.Add(-time.Minute)), RefreshToken: "old-refresh", AccountID: "acct"}, base)

	token, account, err := source.Credentials(context.Background())
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if token != fresh || account != "acct" {
		t.Errorf("Credentials = %q, %q, want the refreshed token", token, account)
	}
	if got := iss.form(0).Get("refresh_token"); got != "old-refresh" {
		t.Errorf("refreshed with %q", got)
	}
}

// Renewing a moment early keeps a token from expiring between this check and
// the request it is for.
func TestCredentialsRefreshesATokenAboutToExpire(t *testing.T) {
	iss, base := newIssuer(t, granted(accessToken(t, now.Add(time.Hour)), "new-refresh", ""))
	source, _ := newTestSource(t, Tokens{AccessToken: accessToken(t, now.Add(time.Minute)), RefreshToken: "r", AccountID: "acct"}, base)

	if _, _, err := source.Credentials(context.Background()); err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if iss.requests() != 1 {
		t.Errorf("refreshed %d times, want 1", iss.requests())
	}
}

// With no expiry to read there is nothing to refresh ahead of: the token is
// used until the backend refuses it.
func TestCredentialsUsesAnOpaqueTokenWithoutRefreshing(t *testing.T) {
	iss, base := newIssuer(t)
	source, _ := newTestSource(t, Tokens{AccessToken: "opaque", RefreshToken: "r", AccountID: "acct"}, base)

	token, _, err := source.Credentials(context.Background())
	if err != nil || token != "opaque" {
		t.Errorf("Credentials = %q, %v", token, err)
	}
	if iss.requests() != 0 {
		t.Errorf("refreshed %d times, want none", iss.requests())
	}
}

// The refresh token rotates, so a refresh that is not written down is one the
// next session cannot repeat.
func TestCredentialsSavesTheRefreshedLogin(t *testing.T) {
	fresh := accessToken(t, now.Add(time.Hour))
	_, base := newIssuer(t, granted(fresh, "new-refresh", ""))
	source, saved := newTestSource(t, Tokens{AccessToken: accessToken(t, now.Add(-time.Minute)), RefreshToken: "old-refresh", AccountID: "acct"}, base)

	if _, _, err := source.Credentials(context.Background()); err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if len(saved.tokens) != 1 {
		t.Fatalf("saved %d times, want once", len(saved.tokens))
	}
	if got := saved.tokens[0]; got.AccessToken != fresh || got.RefreshToken != "new-refresh" {
		t.Errorf("saved %+v, want the refreshed tokens", got)
	}
}

func TestCredentialsRefreshesOnlyOnceAcrossCallers(t *testing.T) {
	iss, base := newIssuer(t, granted(accessToken(t, now.Add(time.Hour)), "new-refresh", ""))
	source, _ := newTestSource(t, Tokens{AccessToken: accessToken(t, now.Add(-time.Minute)), RefreshToken: "r", AccountID: "acct"}, base)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := source.Credentials(context.Background()); err != nil {
				t.Errorf("Credentials: %v", err)
			}
		}()
	}
	wg.Wait()

	if iss.requests() != 1 {
		t.Errorf("refreshed %d times, want 1", iss.requests())
	}
}

// A failed refresh leaves the login as it was, so the next turn tries again
// rather than inheriting a half-updated state.
func TestCredentialsRetriesAfterAFailedRefresh(t *testing.T) {
	fresh := accessToken(t, now.Add(time.Hour))
	_, base := newIssuer(t,
		reply{status: http.StatusBadGateway, body: map[string]any{}},
		granted(fresh, "new-refresh", ""),
	)
	source, _ := newTestSource(t, Tokens{AccessToken: accessToken(t, now.Add(-time.Minute)), RefreshToken: "r", AccountID: "acct"}, base)

	if _, _, err := source.Credentials(context.Background()); err == nil {
		t.Fatal("Credentials succeeded although the refresh failed")
	}
	token, _, err := source.Credentials(context.Background())
	if err != nil || token != fresh {
		t.Errorf("second Credentials = %q, %v, want the refreshed token", token, err)
	}
}

func TestCredentialsPassesASignOutThrough(t *testing.T) {
	_, base := newIssuer(t, reply{status: http.StatusBadRequest, body: map[string]any{"error": "invalid_grant"}})
	source, _ := newTestSource(t, Tokens{AccessToken: accessToken(t, now.Add(-time.Minute)), RefreshToken: "r", AccountID: "acct"}, base)

	if _, _, err := source.Credentials(context.Background()); !errors.Is(err, ErrSignedOut) {
		t.Errorf("err = %v, want ErrSignedOut", err)
	}
}

// A save that fails is reported, since the next session will start from a
// spent refresh token. The refreshed login is still kept in memory: the old
// refresh token is spent either way, so refreshing again could only fail.
func TestCredentialsReportsAFailedSaveButKeepsTheRefresh(t *testing.T) {
	fresh := accessToken(t, now.Add(time.Hour))
	iss, base := newIssuer(t, granted(fresh, "new-refresh", ""))
	failing := func(Tokens) error { return errors.New("disk full") }
	source := NewSource(Tokens{AccessToken: accessToken(t, now.Add(-time.Minute)), RefreshToken: "r", AccountID: "acct"}, OAuth{Issuer: base}, failing)
	source.now = fixedNow(now)

	if _, _, err := source.Credentials(context.Background()); err == nil {
		t.Error("Credentials hid the failed save")
	}
	token, _, err := source.Credentials(context.Background())
	if err != nil || token != fresh {
		t.Errorf("second Credentials = %q, %v, want the refreshed token", token, err)
	}
	if iss.requests() != 1 {
		t.Errorf("refreshed %d times, want 1", iss.requests())
	}
}
