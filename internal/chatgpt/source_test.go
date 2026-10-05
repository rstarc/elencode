package chatgpt

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chatgpt.json")
	want := Tokens{AccessToken: "a", RefreshToken: "r", IDToken: "i", AccountID: "acct"}

	if err := SaveTokens(path, want); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	got, err := LoadTokens(path)
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if got != want {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
}

// The file holds a credential, so only its owner may read it, and the
// directory may not exist yet on a first login.
func TestSaveTokensCreatesAnOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "chatgpt.json")

	if err := SaveTokens(path, Tokens{AccessToken: "a"}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
}

// WriteFile only applies the mode when it creates the file.
func TestSaveTokensTightensAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chatgpt.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveTokens(path, Tokens{AccessToken: "a"}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	info, _ := os.Stat(path)
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
}

// Not being signed in is an ordinary state the caller has to recognise.
func TestLoadTokensReportsAMissingFileAsNotExist(t *testing.T) {
	_, err := LoadTokens(filepath.Join(t.TempDir(), "chatgpt.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestLoadTokensNamesTheFileItCouldNotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chatgpt.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadTokens(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want it to name %s", err, path)
	}
}

// A file without what every request needs is a broken login, and saying so
// at startup beats a 401 on the first message.
func TestLoadTokensRejectsAnIncompleteLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chatgpt.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"a","refresh_token":"r"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadTokens(path); err == nil {
		t.Error("LoadTokens accepted a login with no account id")
	}
}

// fixedNow pins the clock a Source compares expiry against.
func fixedNow(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

var now = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func newTestSource(t *testing.T, tokens Tokens, base string) (*Source, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chatgpt.json")
	source := NewSource(path, tokens, OAuth{Issuer: base})
	source.now = fixedNow(now)
	return source, path
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
	source, path := newTestSource(t, Tokens{AccessToken: accessToken(t, now.Add(-time.Minute)), RefreshToken: "old-refresh", AccountID: "acct"}, base)

	if _, _, err := source.Credentials(context.Background()); err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	saved, err := LoadTokens(path)
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if saved.AccessToken != fresh || saved.RefreshToken != "new-refresh" {
		t.Errorf("saved %+v, want the refreshed tokens", saved)
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
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A path under a regular file can be neither created nor written
	source := NewSource(filepath.Join(blocker, "chatgpt.json"), Tokens{AccessToken: accessToken(t, now.Add(-time.Minute)), RefreshToken: "r", AccountID: "acct"}, OAuth{Issuer: base})
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
