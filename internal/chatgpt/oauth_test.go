package chatgpt

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

// fakeJWT builds an unsigned token carrying claims. Only the payload is ever
// read, so the header and signature just have to be there.
func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// idToken is an id_token naming accountID the way auth.openai.com does.
func idToken(t *testing.T, accountID string) string {
	return fakeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID},
	})
}

// s256 is the PKCE challenge for verifier, worked out here rather than taken
// from the library that makes it.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// accessToken is an access token expiring at exp.
func accessToken(t *testing.T, exp time.Time) string {
	return fakeJWT(t, map[string]any{"exp": exp.Unix()})
}

func TestAuthorizeURLCarriesWhatTheIssuerExpects(t *testing.T) {
	raw := OAuth{Issuer: "https://auth.example"}.authorizeURL("http://127.0.0.1:1455/auth/callback", "the-verifier", "the-state")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "https://auth.example/oauth/authorize" {
		t.Errorf("endpoint = %q", got)
	}

	want := map[string]string{
		"response_type":              "code",
		"client_id":                  ClientID,
		"redirect_uri":               "http://127.0.0.1:1455/auth/callback",
		"scope":                      "openid profile email offline_access",
		"code_challenge":             s256("the-verifier"),
		"code_challenge_method":      "S256",
		"state":                      "the-state",
		"id_token_add_organizations": "true",
		"codex_cli_simplified_flow":  "true",
		"originator":                 Originator,
	}
	query := u.Query()
	for key, value := range want {
		if got := query.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
}

func TestAccountIDReadsTheOpenAIAuthClaim(t *testing.T) {
	got, err := accountID(idToken(t, "acct-123"))
	if err != nil {
		t.Fatalf("accountID: %v", err)
	}
	if got != "acct-123" {
		t.Errorf("accountID = %q, want acct-123", got)
	}
}

// Every request needs the account id, so a token without one is a failed
// login rather than a session that fails on its first message.
func TestAccountIDFailsWhenTheClaimIsMissing(t *testing.T) {
	if _, err := accountID(fakeJWT(t, map[string]any{"email": "a@b.c"})); err == nil {
		t.Error("accountID succeeded without a chatgpt_account_id claim")
	}
}

func TestAccountIDFailsOnSomethingThatIsNotAJWT(t *testing.T) {
	for _, token := range []string{"", "opaque", "a.b", "a.!!!.c", "a." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".c"} {
		if _, err := accountID(token); err == nil {
			t.Errorf("accountID(%q) succeeded", token)
		}
	}
}

func TestExpiryReadsTheExpClaim(t *testing.T) {
	exp := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	got, ok := expiry(accessToken(t, exp))
	if !ok {
		t.Fatal("expiry reported no expiry")
	}
	if !got.Equal(exp) {
		t.Errorf("expiry = %v, want %v", got, exp)
	}
}

// An access token that is opaque, or names no expiry, is not an error: it is
// used until the API turns it down.
func TestExpiryIsUnknownForAnOpaqueToken(t *testing.T) {
	for _, token := range []string{"opaque", fakeJWT(t, map[string]any{"sub": "x"})} {
		if _, ok := expiry(token); ok {
			t.Errorf("expiry(%q) reported an expiry", token)
		}
	}
}

// Which account a login is for is worth saying at the end of it: one person
// can have several.
func TestTokensNameTheAccountsEmailAndPlan(t *testing.T) {
	tokens := Tokens{IDToken: fakeJWT(t, map[string]any{
		"email":   "someone@example.com",
		authClaim: map[string]any{"chatgpt_account_id": "acct", "chatgpt_plan_type": "plus"},
	})}

	if got := tokens.Email(); got != "someone@example.com" {
		t.Errorf("Email = %q", got)
	}
	if got := tokens.Plan(); got != "plus" {
		t.Errorf("Plan = %q", got)
	}
}

// The issuer has also put the email under its own profile claim.
func TestTokensFindTheEmailInTheProfileClaim(t *testing.T) {
	tokens := Tokens{IDToken: fakeJWT(t, map[string]any{
		"https://api.openai.com/profile": map[string]any{"email": "someone@example.com"},
	})}

	if got := tokens.Email(); got != "someone@example.com" {
		t.Errorf("Email = %q", got)
	}
}

// Naming the account is a nicety: a token that does not say leaves it unsaid.
func TestTokensWithoutAnAccountNameNone(t *testing.T) {
	for _, tokens := range []Tokens{{}, {IDToken: "opaque"}, {IDToken: fakeJWT(t, map[string]any{})}} {
		if tokens.Email() != "" || tokens.Plan() != "" {
			t.Errorf("%+v names %q, %q", tokens, tokens.Email(), tokens.Plan())
		}
	}
}
