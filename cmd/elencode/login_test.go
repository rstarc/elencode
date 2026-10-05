package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
)

// issuerStub stands in for auth.openai.com: the token endpoint answers with
// tokenStatus, and the device-code endpoints with deviceStatus for the code
// and an immediate grant for the poll.
func issuerStub(t *testing.T, tokenStatus, deviceStatus int) string {
	t.Helper()
	claims, _ := json.Marshal(map[string]any{
		"email":                       "someone@example.com",
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct", "chatgpt_plan_type": "plus"},
	})
	idToken := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			w.WriteHeader(deviceStatus)
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "dev", "user_code": "ABCD-1234", "interval": "1"})
		case "/api/accounts/deviceauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"authorization_code": "code", "code_challenge": "c", "code_verifier": "v"})
		default:
			w.WriteHeader(tokenStatus)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token":  "access",
				"refresh_token": "refresh",
				"id_token":      idToken,
			})
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// tokenEndpoint is an issuer whose code exchange answers with status.
func tokenEndpoint(t *testing.T, status int) string {
	return issuerStub(t, status, http.StatusOK)
}

// callBackFrom is the user signing in on the authorize page: it calls back to
// the redirect URI with a code and the state it was handed. Asynchronous, as a
// browser is, and so reporting with Errorf.
func callBackFrom(t *testing.T, authorize string) {
	go func() {
		u, err := url.Parse(authorize)
		if err != nil {
			t.Errorf("authorize URL: %v", err)
			return
		}
		query := u.Query()
		callback := query.Get("redirect_uri") + "?" + url.Values{"code": {"code"}, "state": {query.Get("state")}}.Encode()
		resp, err := http.Get(callback)
		if err != nil {
			t.Errorf("calling back: %v", err)
			return
		}
		_ = resp.Body.Close()
	}()
}

// fakeBrowser opens pages by signing in on them, and remembers which.
type fakeBrowser struct {
	t      *testing.T
	fail   bool // cannot be started, as when there is no opener installed
	mu     sync.Mutex
	opened []string
}

func (b *fakeBrowser) open(url string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.opened = append(b.opened, url)
	if b.fail {
		return errors.New("xdg-open: not found")
	}
	callBackFrom(b.t, url)
	return nil
}

func (b *fakeBrowser) pages() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.opened...)
}

// testSignIn signs in against issuer, on a desktop whose browser is b.
func testSignIn(t *testing.T, issuer string, b *fakeBrowser) signIn {
	return signIn{
		oauth:        chatgpt.OAuth{Issuer: issuer},
		callbackAddr: "127.0.0.1:0",
		path:         filepath.Join(t.TempDir(), "elencode", "chatgpt.json"),
		openBrowser:  b.open,
	}
}

// signInWith runs s, collecting what it asked of the user, and signing in on
// any sign-in page it asked them to open when callBack is set.
func signInWith(t *testing.T, s signIn, callBack bool) ([]loginPrompt, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var prompts []loginPrompt
	_, err := s.run(ctx, func(prompt loginPrompt) {
		prompts = append(prompts, prompt)
		if callBack && strings.Contains(prompt.page, "/oauth/authorize") {
			callBackFrom(t, prompt.page)
		}
	})
	return prompts, err
}

// said is everything the prompts said, laid out as plain text.
func said(prompts []loginPrompt) string {
	var b strings.Builder
	for _, prompt := range prompts {
		b.WriteString(prompt.render(false) + "\n")
	}
	return b.String()
}

// On a desktop the browser opens by itself: the user only has to sign in.
func TestLoginOpensTheBrowser(t *testing.T) {
	b := &fakeBrowser{t: t}
	s := testSignIn(t, tokenEndpoint(t, http.StatusOK), b)

	prompts, err := signInWith(t, s, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if pages := b.pages(); len(pages) != 1 || !strings.Contains(pages[0], "/oauth/authorize") {
		t.Errorf("opened %q, want the authorize page", pages)
	}
	if _, err := chatgpt.LoadTokens(s.path); err != nil {
		t.Errorf("the login was not saved: %v", err)
	}
	// The page is shown too, for a browser that did not come up
	if len(prompts) != 1 || prompts[0].page != b.pages()[0] {
		t.Errorf("prompts = %+v, want the page it opened", prompts)
	}
}

func TestLoginReturnsTheAccountItSignedInTo(t *testing.T) {
	s := testSignIn(t, tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tokens, err := s.run(ctx, func(loginPrompt) {})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if tokens.Email() != "someone@example.com" {
		t.Errorf("signed in to %q", tokens.Email())
	}
}

func TestLoginShowsThePageWhenTheBrowserCannotBeOpened(t *testing.T) {
	b := &fakeBrowser{t: t, fail: true}
	s := testSignIn(t, tokenEndpoint(t, http.StatusOK), b)

	prompts, err := signInWith(t, s, true)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0].page, "/oauth/authorize") {
		t.Errorf("prompts = %+v, want the page to open by hand", prompts)
	}
}

// With no browser here, the user enters a code on any other device, and
// nothing is opened.
func TestLoginUsesADeviceCodeWhenHeadless(t *testing.T) {
	b := &fakeBrowser{t: t}
	s := testSignIn(t, issuerStub(t, http.StatusOK, http.StatusOK), b)
	s.headless = true

	prompts, err := signInWith(t, s, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(b.pages()) != 0 {
		t.Errorf("opened %q on a headless machine", b.pages())
	}
	if len(prompts) != 1 || !strings.HasSuffix(prompts[0].page, "/codex/device") || prompts[0].code != "ABCD-1234" {
		t.Errorf("prompts = %+v, want the device page and the code", prompts)
	}
	if _, err := chatgpt.LoadTokens(s.path); err != nil {
		t.Errorf("the login was not saved: %v", err)
	}
}

// --device is for when the guess is wrong: a desktop whose browser is not the
// one to sign in with, or a remote session the guess took for local.
func TestLoginDeviceFlagForcesADeviceCode(t *testing.T) {
	b := &fakeBrowser{t: t}
	s := testSignIn(t, issuerStub(t, http.StatusOK, http.StatusOK), b)
	s.device = true

	prompts, err := signInWith(t, s, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(b.pages()) != 0 {
		t.Errorf("opened %q although --device was asked for", b.pages())
	}
	if len(prompts) != 1 || prompts[0].code == "" {
		t.Errorf("prompts = %+v, want a device code", prompts)
	}
}

// Asked for outright, a device code that is not available is an answer the
// user should hear, not something to quietly route around.
func TestLoginDeviceFlagDoesNotFallBack(t *testing.T) {
	b := &fakeBrowser{t: t}
	s := testSignIn(t, issuerStub(t, http.StatusOK, http.StatusNotFound), b)
	s.device = true

	prompts, err := signInWith(t, s, false)
	if !errors.Is(err, chatgpt.ErrDeviceCodeUnavailable) || !strings.Contains(err.Error(), "without --device") {
		t.Errorf("err = %v, want it to say to sign in without --device", err)
	}
	if len(prompts) != 0 || len(b.pages()) != 0 {
		t.Errorf("prompts = %+v, opened %q: want neither", prompts, b.pages())
	}
}

// An account without device codes can still sign in: through the browser
// login, with the page to open and how to reach this machine's port.
func TestLoginFallsBackToTheBrowserWhenDeviceCodesAreOff(t *testing.T) {
	b := &fakeBrowser{t: t}
	s := testSignIn(t, issuerStub(t, http.StatusOK, http.StatusNotFound), b)
	s.headless = true

	prompts, err := signInWith(t, s, true)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(b.pages()) != 0 {
		t.Errorf("opened %q on a headless machine", b.pages())
	}
	for _, want := range []string{"device code", "ssh -L 1455:127.0.0.1:1455", "/oauth/authorize"} {
		if !strings.Contains(said(prompts), want) {
			t.Errorf("said %q, want it to mention %q", said(prompts), want)
		}
	}
}

// The callback port can be held by another program signing in to ChatGPT, and
// the way around it is a device code, which needs no port.
func TestLoginSuggestsADeviceCodeWhenThePortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	s := testSignIn(t, tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t})
	s.callbackAddr = taken.Addr().String()

	_, err = signInWith(t, s, false)
	if err == nil || !strings.Contains(err.Error(), "--device") {
		t.Errorf("err = %v, want it to suggest --device", err)
	}
}

// A failed login must not leave a file behind that the next start would read
// as being signed in.
func TestLoginWritesNothingWhenItFails(t *testing.T) {
	s := testSignIn(t, tokenEndpoint(t, http.StatusBadRequest), &fakeBrowser{t: t})

	if _, err := signInWith(t, s, false); err == nil {
		t.Fatal("run succeeded although the exchange failed")
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat %s = %v, want no file", s.path, err)
	}
}

// The page and the code each get a line of their own, so either can be
// copied whole.
func TestPromptRenderPutsThePageAndTheCodeOnLinesOfTheirOwn(t *testing.T) {
	prompt := loginPrompt{text: "Do this:", page: "https://example.com/device", code: "ABCD-1234"}

	want := "Do this:\n\n  https://example.com/device\n\n  ABCD-1234"
	if got := prompt.render(false); got != want {
		t.Errorf("render(false) = %q, want %q", got, want)
	}
	if got := prompt.render(true); ansi.Strip(got) != want || !strings.Contains(got, hyperlink(prompt.page)) {
		t.Errorf("render(true) = %q, want the same text with the page as a link", got)
	}
}

func TestHyperlinkMakesThePageClickable(t *testing.T) {
	const page = "https://auth.example/oauth/authorize?client_id=x&state=y"

	got := hyperlink(page)

	if !strings.HasPrefix(got, ansi.SetHyperlink(page)) || !strings.HasSuffix(got, ansi.ResetHyperlink()) {
		t.Errorf("hyperlink = %q, want the page wrapped in an OSC 8 link", got)
	}
	// Still readable where links are not understood, and copyable
	if ansi.Strip(got) != page {
		t.Errorf("hyperlink shows %q, want the page itself", ansi.Strip(got))
	}
	// Styled as a whole: per character, the escapes would outweigh the page
	if len(got) > 2*len(page)+40 {
		t.Errorf("hyperlink is %d bytes for a %d-byte page: %q", len(got), len(page), got)
	}
}

// runTestLogin is `elencode login chatgpt` against s, writing to a buffer as
// if it were a pipe.
func runTestLogin(t *testing.T, ctx context.Context, s signIn) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runLogin(ctx, s, &out, nil)
	return out.String(), err
}

// The user has to know it worked, which account it is, where the login went,
// and how to use it.
func TestLoginCLISaysWhatItSignedInToAndHowToUseIt(t *testing.T) {
	s := testSignIn(t, tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runTestLogin(t, ctx, s)
	if err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	for _, want := range []string{"/oauth/authorize", "someone@example.com", "plus", s.path, "/model chatgpt/"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not mention %q", out, want)
		}
	}
}

// After the page there is nothing on screen to say anything is happening, so
// it says so, and how to stop.
func TestLoginCLISaysItIsWaiting(t *testing.T) {
	s := testSignIn(t, tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runTestLogin(t, ctx, s)
	if err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	if !strings.Contains(out, "Waiting") || !strings.Contains(out, "ctrl+c") {
		t.Errorf("output %q, want it to say it is waiting and how to stop", out)
	}
}

// On a terminal `elencode login` runs the same panel the session shows: the
// page as a link above it, c to copy, esc to cancel. The program runs end to
// end here, with a pipe for the keyboard.
func TestLoginCLIOnATerminalRunsTheLoginPanel(t *testing.T) {
	s := testSignIn(t, tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keys, typing := io.Pipe()
	defer func() { _ = typing.Close() }()
	var out bytes.Buffer

	if err := runLogin(ctx, s, &out, keys); err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	for _, want := range []string{"/oauth/authorize", "Signed in to ChatGPT as someone@example.com"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q does not mention %q", out.String(), want)
		}
	}
}

// waitingProgram is the CLI's login program with a prompt shown.
func waitingProgram(t *testing.T, clip clipboard) (loginProgram, *bool) {
	t.Helper()
	cancelled := false
	p := loginProgram{events: make(chan loginEvent), cancel: func() { cancelled = true }, clipboard: clip}
	next, _ := p.Update(loginPromptMsg{prompt: openByHand("https://auth.example/oauth/authorize?x=1"), events: p.events})
	return next.(loginProgram), &cancelled
}

func TestLoginProgramPrintsThePromptAsTheSessionDoes(t *testing.T) {
	p := loginProgram{events: make(chan loginEvent)}
	prompt := openByHand("https://auth.example/oauth/authorize?x=1")

	next, cmd := p.Update(loginPromptMsg{prompt: prompt, events: p.events})

	if got := printed(t, firstOf(t, cmd)); !strings.Contains(got, prompt.render(true)) {
		t.Errorf("printed %q, want the prompt as rendered for a terminal", got)
	}
	if view := next.View().Content; !strings.Contains(view, "c to copy the link") {
		t.Errorf("view = %q, want the login panel", view)
	}
}

func TestLoginProgramCopiesTheLinkOnC(t *testing.T) {
	clip, file := clipboardFile(t)
	p, _ := waitingProgram(t, clip)

	next, cmd := p.Update(keyC)
	next, _ = next.Update(find[copiedMsg](t, run(t, cmd)))

	if got, _ := os.ReadFile(file); string(got) != "https://auth.example/oauth/authorize?x=1" {
		t.Errorf("clipboard holds %q", got)
	}
	if view := next.View().Content; !strings.Contains(view, "copied to clipboard!") {
		t.Errorf("view = %q, want it to say it copied", view)
	}
}

func TestLoginProgramCancelsOnEscAndCtrlC(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'c', Mod: tea.ModCtrl}} {
		p, cancelled := waitingProgram(t, clipboard{})
		p.Update(key)
		if !*cancelled {
			t.Errorf("%s did not cancel the login", key)
		}
	}
}

// The panel goes once the login has ended: the program's last frame stays on
// screen, and what is left to say is printed after it.
func TestLoginProgramEndsWithTheLogin(t *testing.T) {
	p, _ := waitingProgram(t, clipboard{})

	next, cmd := p.Update(loginDoneMsg{err: errors.New("refused")})

	if !quits(cmd) {
		t.Error("the program did not quit when the login ended")
	}
	if view := next.View().Content; view != "" {
		t.Errorf("view = %q, want the panel gone", view)
	}
	if got := next.(loginProgram).result.err; got == nil || got.Error() != "refused" {
		t.Errorf("result = %v, want the login's own ending", got)
	}
}

// A pipe gets the page as plain text: escape codes there are noise.
func TestLoginCLIPrintsAPlainPageToAPipe(t *testing.T) {
	s := testSignIn(t, tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runTestLogin(t, ctx, s)
	if err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("output %q has escape codes", out)
	}
}

// ctrl+c is the user's choice, not a failure to explain with "context
// canceled".
func TestLoginCLISaysItWasCancelled(t *testing.T) {
	s := testSignIn(t, tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t, fail: true})
	ctx, cancel := context.WithCancel(context.Background())
	s.openBrowser = func(string) error { cancel(); return errors.New("no browser") }

	_, err := runTestLogin(t, ctx, s)
	if err == nil || err.Error() != "login cancelled" {
		t.Errorf("err = %v, want login cancelled", err)
	}
}

func TestParseLoginArgs(t *testing.T) {
	tests := []struct {
		args   []string
		device bool
		ok     bool
	}{
		{[]string{"chatgpt"}, false, true},
		{[]string{"chatgpt", "--device"}, true, true},
		{[]string{"--device", "chatgpt"}, true, true},
		{nil, false, false},
		{[]string{"--device"}, false, false},
		{[]string{"chatgpt", "--browser"}, false, false},
		{[]string{"chatgpt", "openai"}, false, false},
	}
	for _, test := range tests {
		provider, device, err := parseLoginArgs(test.args)
		if (err == nil) != test.ok {
			t.Errorf("parseLoginArgs(%q) err = %v, want ok = %v", test.args, err, test.ok)
			continue
		}
		if test.ok && (provider != agent.ProviderChatGPT || device != test.device) {
			t.Errorf("parseLoginArgs(%q) = %q, %v", test.args, provider, device)
		}
	}
}

// A flag it does not know is named, rather than read as a provider.
func TestParseLoginArgsNamesAnUnknownFlag(t *testing.T) {
	_, _, err := parseLoginArgs([]string{"chatgpt", "--browser"})
	if err == nil || !strings.Contains(err.Error(), "--browser") || strings.Contains(err.Error(), "provider") {
		t.Errorf("err = %v, want it to name the flag", err)
	}
}

func TestSignInProviderAcceptsChatGPT(t *testing.T) {
	provider, err := signInProvider("chatgpt")
	if err != nil || provider != agent.ProviderChatGPT {
		t.Errorf("signInProvider(chatgpt) = %q, %v", provider, err)
	}
}

// Which provider is not implied: with more than one to sign in to, a bare
// login would have to guess, so it lists them instead.
func TestSignInProviderNeedsAProvider(t *testing.T) {
	_, err := signInProvider("")
	if err == nil || !strings.Contains(err.Error(), "chatgpt") {
		t.Errorf("err = %v, want it to list chatgpt", err)
	}
}

// The keyed providers exist, so "unknown provider" would be wrong about them:
// what they lack is a login to have.
func TestSignInProviderPointsAKeyedProviderAtItsKey(t *testing.T) {
	_, err := signInProvider("openai")
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("err = %v, want it to say openai uses an API key", err)
	}
}

func TestSignInProviderRejectsAnUnknownProvider(t *testing.T) {
	_, err := signInProvider("acme")
	if err == nil || !strings.Contains(err.Error(), "acme") {
		t.Errorf("err = %v, want it to name acme", err)
	}
}

func TestLogoutRemovesTheLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chatgpt.json")
	if err := chatgpt.SaveTokens(path, chatgpt.Tokens{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	if err := runLogout(path, &out); err != nil {
		t.Fatalf("runLogout: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat %s = %v, want the login gone", path, err)
	}
	if !strings.Contains(out.String(), "Signed out") {
		t.Errorf("output %q, want it to say so", out.String())
	}
}

// Signing out twice is not a failure: the state asked for is the state there is.
func TestLogoutWithoutALoginSaysSo(t *testing.T) {
	var out bytes.Buffer

	if err := runLogout(filepath.Join(t.TempDir(), "chatgpt.json"), &out); err != nil {
		t.Fatalf("runLogout: %v", err)
	}
	if !strings.Contains(out.String(), "not signed in") {
		t.Errorf("output %q, want it to say there was nothing to sign out of", out.String())
	}
}

func TestLoginCLIRefusesWithoutAProvider(t *testing.T) {
	if err := loginCLI(nil, &bytes.Buffer{}); err == nil {
		t.Error("`elencode login` with no provider started a login")
	}
}

func TestLogoutCLIRefusesWithoutAProvider(t *testing.T) {
	if err := logoutCLI(nil, &bytes.Buffer{}); err == nil {
		t.Error("`elencode logout` with no provider signed something out")
	}
}

func TestLoginProgramBringsTheCopyHintBack(t *testing.T) {
	clip, _ := clipboardFile(t)
	p, _ := waitingProgram(t, clip)
	next, cmd := p.Update(keyC)
	next, _ = next.Update(find[copiedMsg](t, run(t, cmd)))

	next, _ = next.Update(copiedExpiredMsg{copies: next.(loginProgram).panel.copies})

	if view := next.View().Content; strings.Contains(view, "copied") || !strings.Contains(view, "c to copy the link") {
		t.Errorf("view = %q, want the hint back", view)
	}
}
