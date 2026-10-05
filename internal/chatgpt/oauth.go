// Package chatgpt signs in with a ChatGPT account, so the OpenAI models can be
// used on a ChatGPT plan's Codex allowance rather than on API credits. The
// issuer has one public client for this, and no registration for other
// programs.
//
// The OAuth itself is golang.org/x/oauth2's. Nothing here knows about a
// provider: the OpenAI provider asks a Source for a bearer token and an
// account id, and that is the whole boundary.
package chatgpt

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// ClientID is the issuer's public client for signing in to Codex. PKCE-only
// and secret-free, which is what lets any program run the flow.
const ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// Issuer is where the login and every token exchange happen.
const Issuer = "https://auth.openai.com"

// Originator names the client to the issuer and the backend, which has only
// ever been seen accepting the clients it knows.
const Originator = "codex_cli_rs"

// authClaim is the namespaced claim the issuer puts the ChatGPT account in.
const authClaim = "https://api.openai.com/auth"

// OAuth is the client at the issuer. The zero value talks to the real one;
// tests point Issuer at a stub.
type OAuth struct {
	Issuer string
}

func (o OAuth) issuer() string {
	if o.Issuer == "" {
		return Issuer
	}
	return strings.TrimSuffix(o.Issuer, "/")
}

// config is the client as x/oauth2 sees it, sending the browser back to
// redirectURI. It is public, so its id goes in the form rather than in a basic
// auth header.
func (o OAuth) config(redirectURI string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: ClientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:   o.issuer() + "/oauth/authorize",
			TokenURL:  o.issuer() + "/oauth/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
		RedirectURL: redirectURI,
		// offline_access, without which there is no refresh token and the login
		// would last as long as one access token does
		Scopes: []string{"openid", "profile", "email", "offline_access"},
	}
}

// authorizeURL is the page the user signs in on. The last three parameters
// are the issuer's own: the first puts the account into the id token, the
// second skips the API-organisation setup an API login needs.
func (o OAuth) authorizeURL(redirectURI, verifier, state string) string {
	return o.config(redirectURI).AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("id_token_add_organizations", "true"),
		oauth2.SetAuthURLParam("codex_cli_simplified_flow", "true"),
		oauth2.SetAuthURLParam("originator", Originator),
	)
}

// claims is the part of a token's payload this package reads.
type claims struct {
	Exp   int64  `json:"exp"`
	Email string `json:"email"`
	Auth  struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
		ChatGPTPlanType  string `json:"chatgpt_plan_type"`
	} `json:"https://api.openai.com/auth"`
	Profile struct {
		Email string `json:"email"`
	} `json:"https://api.openai.com/profile"`
}

// decodeClaims reads a JWT's payload without checking its signature. Nothing
// here is a trust decision: the tokens came straight from the issuer over TLS,
// and the backend is the one that verifies them.
func decodeClaims(token string) (claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims{}, errors.New("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims{}, fmt.Errorf("decoding JWT payload: %w", err)
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return claims{}, fmt.Errorf("decoding JWT claims: %w", err)
	}
	return c, nil
}

// accountID is the ChatGPT account an id_token belongs to, which every request
// to the backend has to name.
func accountID(idToken string) (string, error) {
	c, err := decodeClaims(idToken)
	if err != nil {
		return "", fmt.Errorf("reading the id token: %w", err)
	}
	if c.Auth.ChatGPTAccountID == "" {
		return "", fmt.Errorf("the id token names no ChatGPT account (no %s.chatgpt_account_id claim)", authClaim)
	}
	return c.Auth.ChatGPTAccountID, nil
}

// expiry is when an access token stops being accepted, if it says.
func expiry(accessToken string) (time.Time, bool) {
	c, err := decodeClaims(accessToken)
	if err != nil || c.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(c.Exp, 0), true
}
