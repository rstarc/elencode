package chatgpt

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// browser plays the user's side of the login: it is handed the authorize URL
// and calls back to the redirect URI with whatever query visit builds from
// the URL's own parameters.
type browser struct {
	t     *testing.T
	visit func(authorize url.Values) url.Values
	// Filled in once the callback has been answered
	authorize url.Values
	status    int
	page      string
	done      chan struct{}
}

func newBrowser(t *testing.T, visit func(url.Values) url.Values) *browser {
	return &browser{t: t, visit: visit, done: make(chan struct{})}
}

func (b *browser) open(raw string) {
	go func() {
		defer close(b.done)
		u, err := url.Parse(raw)
		if err != nil {
			b.t.Errorf("authorize URL %q: %v", raw, err)
			return
		}
		b.authorize = u.Query()
		callback := b.authorize.Get("redirect_uri") + "?" + b.visit(b.authorize).Encode()
		resp, err := http.Get(callback)
		if err != nil {
			b.t.Errorf("calling back: %v", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		b.status, b.page = resp.StatusCode, string(body)
	}()
}

// wait blocks until the callback has been answered, so the test reads what
// the browser saw without racing it.
func (b *browser) wait() {
	b.t.Helper()
	select {
	case <-b.done:
	case <-time.After(2 * time.Second):
		b.t.Fatal("the browser never got an answer")
	}
}

// approve is the user signing in: the issuer redirects back with a code and
// the state it was given.
func approve(code string) func(url.Values) url.Values {
	return func(authorize url.Values) url.Values {
		return url.Values{"code": {code}, "state": {authorize.Get("state")}}
	}
}

func login(t *testing.T, base string, b *browser) (Tokens, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return OAuth{Issuer: base}.Login(ctx, "127.0.0.1:0", b.open)
}

func TestLoginReturnsTheExchangedTokens(t *testing.T) {
	id := idToken(t, "acct")
	_, base := newIssuer(t, granted("access", "refresh", id))
	b := newBrowser(t, approve("the-code"))

	tokens, err := login(t, base, b)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	want := Tokens{AccessToken: "access", RefreshToken: "refresh", IDToken: id, AccountID: "acct"}
	if tokens != want {
		t.Errorf("tokens = %+v, want %+v", tokens, want)
	}

	b.wait()
	if b.status != http.StatusOK || !strings.Contains(b.page, "signed in") {
		t.Errorf("browser saw %d %q, want a success page", b.status, b.page)
	}
}

func TestLoginOpensTheIssuersAuthorizePage(t *testing.T) {
	_, base := newIssuer(t, granted("access", "refresh", idToken(t, "acct")))
	var opened string
	b := newBrowser(t, approve("code"))

	_, err := OAuth{Issuer: base}.Login(context.Background(), "127.0.0.1:0", func(raw string) {
		opened = raw
		b.open(raw)
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !strings.HasPrefix(opened, base+"/oauth/authorize?") {
		t.Errorf("opened %q, want the issuer's authorize page", opened)
	}
}

// The verifier and redirect URI sent with the code have to be the ones the
// authorize URL was built from, or the issuer refuses the exchange.
func TestLoginExchangesWithTheMatchingVerifierAndRedirect(t *testing.T) {
	iss, base := newIssuer(t, granted("access", "refresh", idToken(t, "acct")))
	b := newBrowser(t, approve("the-code"))

	if _, err := login(t, base, b); err != nil {
		t.Fatalf("Login: %v", err)
	}
	b.wait()

	form := iss.form(0)
	if form.Get("code") != "the-code" {
		t.Errorf("exchanged code %q", form.Get("code"))
	}
	if form.Get("client_id") != ClientID {
		t.Errorf("exchanged as client %q, want it in the form: the client has no secret", form.Get("client_id"))
	}
	if got := s256(form.Get("code_verifier")); got != b.authorize.Get("code_challenge") {
		t.Errorf("verifier hashes to %q, the authorize URL asked for %q", got, b.authorize.Get("code_challenge"))
	}
	if form.Get("redirect_uri") != b.authorize.Get("redirect_uri") {
		t.Errorf("exchanged with redirect %q, authorized %q", form.Get("redirect_uri"), b.authorize.Get("redirect_uri"))
	}
}

// The registered redirect is the loopback address on the callback path; the
// port is whichever one Login listened on.
func TestLoginRedirectsToTheLoopbackCallback(t *testing.T) {
	_, base := newIssuer(t, granted("access", "refresh", idToken(t, "acct")))
	b := newBrowser(t, approve("code"))

	if _, err := login(t, base, b); err != nil {
		t.Fatalf("Login: %v", err)
	}
	b.wait()

	redirect, err := url.Parse(b.authorize.Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	if redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.Path != "/auth/callback" {
		t.Errorf("redirect_uri = %q", redirect)
	}
}

func TestLoginStateIsRandomPerLogin(t *testing.T) {
	var states []string
	for range 2 {
		_, base := newIssuer(t, granted("access", "refresh", idToken(t, "acct")))
		b := newBrowser(t, approve("code"))
		if _, err := login(t, base, b); err != nil {
			t.Fatalf("Login: %v", err)
		}
		b.wait()
		states = append(states, b.authorize.Get("state"))
	}
	if states[0] == "" || states[0] == states[1] {
		t.Errorf("states = %q, want two different non-empty values", states)
	}
}

// A callback carrying someone else's state is not this login's answer, and
// its code must not be spent.
func TestLoginRejectsAMismatchedState(t *testing.T) {
	iss, base := newIssuer(t)
	b := newBrowser(t, func(url.Values) url.Values {
		return url.Values{"code": {"code"}, "state": {"forged"}}
	})

	if _, err := login(t, base, b); err == nil {
		t.Fatal("Login accepted a forged state")
	}
	b.wait()
	if iss.requests() != 0 {
		t.Errorf("exchanged %d codes, want none", iss.requests())
	}
	if b.status == http.StatusOK {
		t.Error("the browser was told the login worked")
	}
}

func TestLoginReportsTheIssuersRefusal(t *testing.T) {
	_, base := newIssuer(t)
	b := newBrowser(t, func(authorize url.Values) url.Values {
		return url.Values{"error": {"access_denied"}, "error_description": {"the user said no"}, "state": {authorize.Get("state")}}
	})

	_, err := login(t, base, b)
	if err == nil || !strings.Contains(err.Error(), "the user said no") {
		t.Errorf("err = %v, want the issuer's description", err)
	}
}

func TestLoginFailsWithoutACode(t *testing.T) {
	_, base := newIssuer(t)
	b := newBrowser(t, func(authorize url.Values) url.Values {
		return url.Values{"state": {authorize.Get("state")}}
	})

	if _, err := login(t, base, b); err == nil {
		t.Error("Login succeeded without a code")
	}
}

func TestLoginTellsTheBrowserWhenTheExchangeFails(t *testing.T) {
	_, base := newIssuer(t, reply{status: http.StatusBadRequest, body: map[string]any{"error": "invalid_grant"}})
	b := newBrowser(t, approve("code"))

	if _, err := login(t, base, b); err == nil {
		t.Fatal("Login succeeded although the exchange failed")
	}
	b.wait()
	if b.status == http.StatusOK {
		t.Errorf("browser saw %d %q, want a failure", b.status, b.page)
	}
}

// Giving up on the login must give the port back, or the next attempt cannot
// listen on it.
func TestLoginStopsListeningWhenCancelled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	opened := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := OAuth{Issuer: "http://unused.invalid"}.Login(ctx, addr, func(string) { close(opened) })
		result <- err
	}()

	<-opened
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Login did not return after cancellation")
	}

	again, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port still held after Login returned: %v", err)
	}
	_ = again.Close()
}

func TestLoginFailsWhenThePortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()

	_, err = OAuth{}.Login(context.Background(), taken.Addr().String(), func(string) {
		t.Error("opened a browser for a login that cannot receive the callback")
	})
	if err == nil || !strings.Contains(err.Error(), taken.Addr().String()) {
		t.Errorf("err = %v, want it to name the address", err)
	}
}

func TestLoginIgnoresRequestsForOtherPaths(t *testing.T) {
	_, base := newIssuer(t, granted("access", "refresh", idToken(t, "acct")))
	b := newBrowser(t, approve("code"))

	tokens, err := OAuth{Issuer: base}.Login(context.Background(), "127.0.0.1:0", func(raw string) {
		u, _ := url.Parse(raw)
		redirect, _ := url.Parse(u.Query().Get("redirect_uri"))
		// A browser asking for a favicon first must not end the login
		resp, err := http.Get("http://" + redirect.Host + "/favicon.ico")
		if err != nil {
			t.Errorf("favicon: %v", err)
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("favicon status = %d, want 404", resp.StatusCode)
			}
		}
		b.open(raw)
	})
	if err != nil || tokens.AccountID != "acct" {
		t.Errorf("Login = %+v, %v", tokens, err)
	}
}

// Given up before the server got going, the port must still be free when
// Login returns: a /login started over listens on it straight away.
func TestLoginGivenUpAtOnceLetsGoOfThePort(t *testing.T) {
	for range 20 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		_ = listener.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := (OAuth{Issuer: "http://unused.invalid"}).Login(ctx, addr, func(string) {}); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}

		again, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("port still held after Login returned: %v", err)
		}
		_ = again.Close()
	}
}
