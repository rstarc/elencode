package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// issuer stands in for auth.openai.com's token endpoint. It records every form
// it was posted and answers each with the next scripted reply.
type issuer struct {
	t       *testing.T
	mu      sync.Mutex
	replies []reply
	forms   []url.Values
}

type reply struct {
	status int
	body   any
}

func newIssuer(t *testing.T, replies ...reply) (*issuer, string) {
	i := &issuer{t: t, replies: replies}
	server := httptest.NewServer(i)
	t.Cleanup(server.Close)
	return i, server.URL
}

func (i *issuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if r.Method != http.MethodPost || r.URL.Path != "/oauth/token" {
		i.t.Errorf("request to %s %s, want POST /oauth/token", r.Method, r.URL.Path)
	}
	if err := r.ParseForm(); err != nil {
		i.t.Errorf("parse form: %v", err)
	}
	i.forms = append(i.forms, r.PostForm)

	if len(i.replies) == 0 {
		http.Error(w, "unscripted request", http.StatusInternalServerError)
		return
	}
	next := i.replies[0]
	i.replies = i.replies[1:]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(next.status)
	_ = json.NewEncoder(w).Encode(next.body)
}

func (i *issuer) form(n int) url.Values {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.forms[n]
}

func (i *issuer) requests() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.forms)
}

// granted is a successful token response.
func granted(access, refresh, id string) reply {
	return reply{status: http.StatusOK, body: map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"id_token":      id,
		"token_type":    "Bearer",
	}}
}

func TestExchangeReturnsTheTokensAndTheAccount(t *testing.T) {
	id := idToken(t, "acct-42")
	_, base := newIssuer(t, granted("access", "refresh", id))

	tokens, err := OAuth{Issuer: base}.exchange(context.Background(), "code", "verifier", "redirect")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	want := Tokens{AccessToken: "access", RefreshToken: "refresh", IDToken: id, AccountID: "acct-42"}
	if tokens != want {
		t.Errorf("tokens = %+v, want %+v", tokens, want)
	}
}

// Without a refresh token the login dies with its first access token, which
// is worth refusing now rather than discovering in a few days.
func TestExchangeFailsWithoutARefreshToken(t *testing.T) {
	_, base := newIssuer(t, granted("access", "", idToken(t, "acct")))

	if _, err := (OAuth{Issuer: base}).exchange(context.Background(), "code", "verifier", "redirect"); err == nil {
		t.Error("exchange succeeded without a refresh token")
	}
}

func TestExchangeFailsWithoutAnAccount(t *testing.T) {
	_, base := newIssuer(t, granted("access", "refresh", fakeJWT(t, map[string]any{})))

	if _, err := (OAuth{Issuer: base}).exchange(context.Background(), "code", "verifier", "redirect"); err == nil {
		t.Error("exchange succeeded with an id token naming no account")
	}
}

// The issuer's own words are the useful part of a rejection.
func TestExchangeReportsTheIssuersError(t *testing.T) {
	_, base := newIssuer(t, reply{status: http.StatusBadRequest, body: map[string]any{
		"error":             "invalid_grant",
		"error_description": "code expired",
	}})

	_, err := OAuth{Issuer: base}.exchange(context.Background(), "code", "verifier", "redirect")
	if err == nil {
		t.Fatal("exchange succeeded on a 400")
	}
	for _, want := range []string{"invalid_grant", "code expired"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// Refresh tokens rotate: the old one is spent once used, so the new one has
// to be what is kept.
func TestRefreshKeepsTheRotatedTokens(t *testing.T) {
	id := idToken(t, "acct")
	_, base := newIssuer(t, granted("new-access", "new-refresh", id))

	tokens, err := OAuth{Issuer: base}.refresh(context.Background(), Tokens{AccessToken: "old-access", RefreshToken: "old-refresh", IDToken: "old-id", AccountID: "acct"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	want := Tokens{AccessToken: "new-access", RefreshToken: "new-refresh", IDToken: id, AccountID: "acct"}
	if tokens != want {
		t.Errorf("tokens = %+v, want %+v", tokens, want)
	}
}

// A refresh response may leave out what did not change.
func TestRefreshKeepsWhatTheResponseLeftOut(t *testing.T) {
	_, base := newIssuer(t, reply{status: http.StatusOK, body: map[string]any{"access_token": "new-access"}})

	old := Tokens{AccessToken: "old-access", RefreshToken: "old-refresh", IDToken: "old-id", AccountID: "acct"}
	tokens, err := OAuth{Issuer: base}.refresh(context.Background(), old)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	want := Tokens{AccessToken: "new-access", RefreshToken: "old-refresh", IDToken: "old-id", AccountID: "acct"}
	if tokens != want {
		t.Errorf("tokens = %+v, want %+v", tokens, want)
	}
}

// A refresh token the issuer turned down will be turned down again: the only
// way out is to sign in again, which is what the error has to say.
func TestRefreshRejectionSaysToSignInAgain(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		_, base := newIssuer(t, reply{status: status, body: map[string]any{"error": "invalid_grant"}})

		_, err := OAuth{Issuer: base}.refresh(context.Background(), Tokens{RefreshToken: "old", AccountID: "acct"})
		if !errors.Is(err, ErrSignedOut) {
			t.Errorf("status %d: err = %v, want ErrSignedOut", status, err)
		}
	}
}

// A server error says nothing about the refresh token, so it must not tell
// the user their login is gone.
func TestRefreshServerErrorIsNotASignOut(t *testing.T) {
	_, base := newIssuer(t, reply{status: http.StatusBadGateway, body: map[string]any{}})

	_, err := OAuth{Issuer: base}.refresh(context.Background(), Tokens{RefreshToken: "old", AccountID: "acct"})
	if err == nil {
		t.Fatal("refresh succeeded on a 502")
	}
	if errors.Is(err, ErrSignedOut) {
		t.Errorf("err = %v, a 502 is not a sign-out", err)
	}
}

// Signing in again is no answer to a login that is still in progress.
func TestExchangeRejectionIsNotASignOut(t *testing.T) {
	_, base := newIssuer(t, reply{status: http.StatusBadRequest, body: map[string]any{"error": "invalid_grant"}})

	_, err := OAuth{Issuer: base}.exchange(context.Background(), "code", "verifier", "redirect")
	if errors.Is(err, ErrSignedOut) {
		t.Errorf("err = %v, want a plain rejection", err)
	}
}
