package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
	"github.com/rstarc/elencode/internal/commands"
	"github.com/rstarc/elencode/internal/provider/openai"
)

// run executes cmd off the test goroutine and returns every message it
// produced, unpacking tea.Sequence and tea.Batch into the commands they hold.
// A login waits on a browser, so a command that never returns has to fail the
// test rather than hang it.
func run(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("command did not return")
	}

	// Sequence and Batch hand back their commands as an unexported slice type
	value := reflect.ValueOf(msg)
	if value.Kind() == reflect.Slice && value.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
		var msgs []tea.Msg
		for i := range value.Len() {
			msgs = append(msgs, run(t, value.Index(i).Interface().(tea.Cmd))...)
		}
		return msgs
	}
	return []tea.Msg{msg}
}

// text is everything msgs would print, for asserting on with Contains.
func text(msgs []tea.Msg) string {
	var b strings.Builder
	for _, msg := range msgs {
		fmt.Fprintf(&b, "%v\n", msg)
	}
	return b.String()
}

// firstOf is the first command a tea.Sequence was built from.
func firstOf(t *testing.T, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	value := reflect.ValueOf(cmd())
	if value.Kind() != reflect.Slice || value.Len() == 0 {
		t.Fatalf("%T is not a sequence", value.Interface())
	}
	return value.Index(0).Interface().(tea.Cmd)
}

// find returns the first message of type T among msgs.
func find[T any](t *testing.T, msgs []tea.Msg) T {
	t.Helper()
	for _, msg := range msgs {
		if found, ok := msg.(T); ok {
			return found
		}
	}
	var zero T
	t.Fatalf("no %T among %v", zero, msgs)
	return zero
}

// newLoginModel is a session keyed for providers, whose /login talks to
// issuer, opens pages in b and saves to a temporary file.
func newLoginModel(t *testing.T, providers providerSet, issuer string, b *fakeBrowser) model {
	t.Helper()
	m := newPickerModel(t, providers, testModels)
	m.signIn = testSignIn(t, issuer, b)
	return m
}

// startLogin runs /login chatgpt and returns the session with the login under
// way, the first thing it told the user, and the command waiting on the rest.
func startLogin(t *testing.T, m model) (model, loginPromptMsg, tea.Cmd) {
	t.Helper()
	m, cmd := updateCmd(t, m, commands.LoginMsg{Arg: "chatgpt"})
	prompt := find[loginPromptMsg](t, run(t, cmd))

	m, cmd = updateCmd(t, m, prompt)
	return m, prompt, cmd
}

func TestLoginCommandSignsInWithoutARestart(t *testing.T) {
	b := &fakeBrowser{t: t}
	m := newLoginModel(t, keyed(agent.ProviderAnthropic), tokenEndpoint(t, http.StatusOK), b)

	m, prompt, wait := startLogin(t, m)
	msgs := run(t, wait)
	if pages := b.pages(); len(pages) != 1 || prompt.prompt.page != pages[0] {
		t.Errorf("prompt = %+v, want the page the browser was opened on (%q)", prompt.prompt, pages)
	}

	m, cmd := updateCmd(t, m, find[loginDoneMsg](t, msgs))

	if _, ok := m.providers[agent.ProviderChatGPT].(*openai.Client); !ok {
		t.Errorf("chatgpt provider = %T, want a client", m.providers[agent.ProviderChatGPT])
	}
	if m.config.ChatGPTLoginPath != m.signIn.path {
		t.Errorf("ChatGPTLoginPath = %q, want %q", m.config.ChatGPTLoginPath, m.signIn.path)
	}
	if _, err := chatgpt.LoadTokens(m.signIn.path); err != nil {
		t.Errorf("the login was not saved: %v", err)
	}
	got := text(run(t, cmd))
	for _, want := range []string{"someone@example.com", "/model chatgpt/"} {
		if !strings.Contains(got, want) {
			t.Errorf("printed %q, want it to mention %q", got, want)
		}
	}
}

// The page is a link: in a terminal that follows OSC 8 links it opens with a
// click, wherever the line wraps.
func TestLoginCommandPrintsThePageAsALink(t *testing.T) {
	b := &fakeBrowser{t: t, fail: true}
	m := newLoginModel(t, keyed(agent.ProviderAnthropic), tokenEndpoint(t, http.StatusOK), b)

	m, cmd := updateCmd(t, m, commands.LoginMsg{Arg: "chatgpt"})
	prompt := find[loginPromptMsg](t, run(t, cmd))
	t.Cleanup(m.loginCancel)

	_, cmd = updateCmd(t, m, prompt)
	// The print comes first in the sequence, and the wait behind it blocks.
	// What is printed is the rendering the CLI uses on a terminal.
	if got := printed(t, firstOf(t, cmd)); !strings.Contains(got, prompt.prompt.render(true)) {
		t.Errorf("printed %q, want the prompt as rendered for a terminal", got)
	}
}

func TestLoginCommandTakesTheDeviceFlag(t *testing.T) {
	b := &fakeBrowser{t: t}
	m := newLoginModel(t, keyed(agent.ProviderAnthropic), issuerStub(t, http.StatusOK, http.StatusOK), b)

	m, cmd := updateCmd(t, m, commands.LoginMsg{Arg: "chatgpt --device"})
	prompt := find[loginPromptMsg](t, run(t, cmd))
	t.Cleanup(m.loginCancel)

	if prompt.prompt.code == "" || len(b.pages()) != 0 {
		t.Errorf("prompt = %+v, opened %q: want a code and no browser", prompt.prompt, b.pages())
	}
}

// Until the browser comes back nothing else says a login is under way, and
// it would be easy to forget one is holding the port.
func TestViewShowsAPendingLogin(t *testing.T) {
	m := newLoginModel(t, keyed(agent.ProviderAnthropic), tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t})

	m, _, wait := startLogin(t, m)
	view := m.View().Content
	for _, want := range []string{"waiting for the ChatGPT sign-in", "c to copy the link", "esc to cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("view does not show %q:\n%s", want, view)
		}
	}

	m, _ = updateCmd(t, m, find[loginDoneMsg](t, run(t, wait)))
	if view := m.View().Content; strings.Contains(view, "waiting for the ChatGPT sign-in") {
		t.Errorf("view still shows a login that has finished:\n%s", view)
	}
}

// The models it serves are offered as soon as the login lands.
func TestLoginCommandMakesTheChatGPTModelsAvailable(t *testing.T) {
	m := newLoginModel(t, keyed(agent.ProviderAnthropic), tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t})
	m.models = append(m.models, openai.ChatGPTCatalog()...)

	m, _, wait := startLogin(t, m)
	m, _ = updateCmd(t, m, find[loginDoneMsg](t, run(t, wait)))

	for _, available := range m.availableModels() {
		if available.Provider == agent.ProviderChatGPT {
			return
		}
	}
	t.Error("no chatgpt model is offered after signing in")
}

func TestLoginCommandReportsAFailedLogin(t *testing.T) {
	providers := keyed(agent.ProviderAnthropic)
	m := newLoginModel(t, providers, tokenEndpoint(t, http.StatusBadRequest), &fakeBrowser{t: t})

	m, _, wait := startLogin(t, m)
	m, cmd := updateCmd(t, m, find[loginDoneMsg](t, run(t, wait)))

	if got := text(run(t, cmd)); !strings.Contains(got, "400") {
		t.Errorf("printed %q, want the issuer's refusal", got)
	}
	if _, ok := m.providers[agent.ProviderChatGPT]; ok {
		t.Error("a failed login added a provider")
	}
	if m.loginCancel != nil {
		t.Error("a finished login is still marked pending")
	}
}

func TestLoginCommandRefusesAKeyedProvider(t *testing.T) {
	m := newLoginModel(t, keyed(agent.ProviderAnthropic), "http://unused.invalid", &fakeBrowser{t: t})

	m, cmd := updateCmd(t, m, commands.LoginMsg{Arg: "openai"})

	if got := text(run(t, cmd)); !strings.Contains(got, "API key") {
		t.Errorf("printed %q, want it to say openai uses an API key", got)
	}
	if m.loginCancel != nil {
		t.Error("a refused login is marked pending")
	}
}

// Quitting gives up on the login, which lets go of the callback port.
func TestQuitAbandonsAPendingLogin(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	m := newLoginModel(t, keyed(agent.ProviderAnthropic), "http://unused.invalid", &fakeBrowser{t: t, fail: true})
	m.signIn.callbackAddr = addr
	m, _, wait := startLogin(t, m)

	_, cmd := updateCmd(t, m, commands.QuitMsg{})
	if !quits(cmd) {
		t.Fatal("/quit did not quit")
	}
	done := find[loginDoneMsg](t, run(t, wait))
	if done.err == nil {
		t.Error("the abandoned login reported success")
	}
	again, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the callback port is still held: %v", err)
	}
	_ = again.Close()
}

// signedIn is a session with a saved ChatGPT login, keyed for the given
// providers besides.
func signedIn(t *testing.T, providers providerSet) model {
	t.Helper()
	m := newLoginModel(t, providers, "http://unused.invalid", &fakeBrowser{t: t})
	if err := chatgpt.SaveTokens(m.signIn.path, chatgpt.Tokens{AccessToken: "a", RefreshToken: "r", AccountID: "acct"}); err != nil {
		t.Fatal(err)
	}
	m.providers[agent.ProviderChatGPT] = &recordingProvider{}
	m.config.ChatGPTLoginPath = m.signIn.path
	return m
}

func TestLogoutCommandForgetsTheLogin(t *testing.T) {
	m := signedIn(t, keyed(agent.ProviderAnthropic))
	m.config.Model = "anthropic/model-one"

	m, cmd := updateCmd(t, m, commands.LogoutMsg{Provider: "chatgpt"})

	if _, err := os.Stat(m.signIn.path); !os.IsNotExist(err) {
		t.Errorf("stat = %v, want the login removed", err)
	}
	if _, ok := m.providers[agent.ProviderChatGPT]; ok {
		t.Error("the chatgpt provider is still there")
	}
	if m.config.ChatGPTLoginPath != "" {
		t.Errorf("ChatGPTLoginPath = %q, want empty", m.config.ChatGPTLoginPath)
	}
	if got := text(run(t, cmd)); !strings.Contains(got, "Signed out") {
		t.Errorf("printed %q", got)
	}
}

// Signing out has to stop the spending, so a session on a ChatGPT model moves
// to another provider rather than keep using the login it just gave up.
func TestLogoutCommandMovesOffAChatGPTModel(t *testing.T) {
	m := signedIn(t, keyed(agent.ProviderAnthropic))
	m.config.Model = "chatgpt/gpt-6-sol"

	m, cmd := updateCmd(t, m, commands.LogoutMsg{Provider: "chatgpt"})

	if strings.HasPrefix(m.config.Model, "chatgpt/") {
		t.Errorf("model = %q, still on chatgpt", m.config.Model)
	}
	if got := text(run(t, cmd)); !strings.Contains(got, "switched to") {
		t.Errorf("printed %q, want the switch announced", got)
	}
}

// With nothing to move to, signing out would leave the session unable to
// answer, so it is refused in the session and left to the CLI.
func TestLogoutCommandRefusesToStrandTheSession(t *testing.T) {
	m := signedIn(t, providerSet{})
	m.config.Model = "chatgpt/gpt-6-sol"

	m, cmd := updateCmd(t, m, commands.LogoutMsg{Provider: "chatgpt"})

	if _, err := os.Stat(m.signIn.path); err != nil {
		t.Errorf("stat = %v, want the login kept", err)
	}
	if got := text(run(t, cmd)); !strings.Contains(got, "elencode logout chatgpt") {
		t.Errorf("printed %q, want it to point at the CLI", got)
	}
}

func TestLogoutCommandRefusesAKeyedProvider(t *testing.T) {
	m := signedIn(t, keyed(agent.ProviderAnthropic))

	m, cmd := updateCmd(t, m, commands.LogoutMsg{Provider: "anthropic"})

	if got := text(run(t, cmd)); !strings.Contains(got, "API key") {
		t.Errorf("printed %q", got)
	}
	if _, ok := m.providers[agent.ProviderAnthropic]; !ok {
		t.Error("refusing to log out of anthropic dropped it anyway")
	}
}

func TestVersionCommandPrintsTheVersionLine(t *testing.T) {
	_, cmd := updateCmd(t, newSizedModel(t), commands.ShowVersionMsg{})

	bi, ok := debug.ReadBuildInfo()
	if got := text(run(t, cmd)); !strings.Contains(got, versionLine(version, bi, ok)) {
		t.Errorf("printed %q, want the version line", got)
	}
}

// clipboardFile is a clipboard that writes to a file the test reads.
func clipboardFile(t *testing.T) (clipboard, string) {
	file := filepath.Join(t.TempDir(), "clipboard")
	write := func(text string) error { return os.WriteFile(file, []byte(text), 0o600) }
	return clipboard{write: write}, file
}

// waitingLogin is a session whose login waits on the user: the browser could
// not be opened, so nothing comes back until they act.
func waitingLogin(t *testing.T) (model, loginPromptMsg) {
	t.Helper()
	m := newLoginModel(t, keyed(agent.ProviderAnthropic), tokenEndpoint(t, http.StatusOK), &fakeBrowser{t: t, fail: true})
	m, prompt, _ := startLogin(t, m)
	t.Cleanup(func() {
		if m.loginCancel != nil {
			m.loginCancel()
		}
	})
	return m, prompt
}

var keyC = tea.KeyPressMsg{Code: 'c', Text: "c"}

// One key: c copies the page, and says so.
func TestCCopiesTheLinkWhileALoginWaits(t *testing.T) {
	m, prompt := waitingLogin(t)
	clip, file := clipboardFile(t)
	m.clipboard = clip

	m, _ = press(t, m, keyC)

	if got, _ := os.ReadFile(file); string(got) != prompt.prompt.page {
		t.Errorf("clipboard holds %q, want the page %q", got, prompt.prompt.page)
	}
	if view := m.View().Content; !strings.Contains(view, "copied to clipboard!") {
		t.Errorf("view does not say it copied:\n%s", view)
	}
}

// While a login waits it has the keyboard, so c can mean copy: a message
// typed meanwhile would otherwise start with a copy.
func TestALoginWaitingTakesTheKeyboard(t *testing.T) {
	m, _ := waitingLogin(t)
	m.clipboard, _ = clipboardFile(t)

	m = typeText(t, m, "hello")

	if m.input.Value() != "" {
		t.Errorf("input = %q, want the keys kept from it", m.input.Value())
	}
	if m.loginCancel == nil {
		t.Error("typing ended the login")
	}
}

func TestEscCancelsAWaitingLogin(t *testing.T) {
	m, _ := waitingLogin(t)

	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.loginCancel != nil {
		t.Error("the login is still waiting")
	}
	if got := text(run(t, cmd)); !strings.Contains(got, "login cancelled") {
		t.Errorf("printed %q, want it to say the login was cancelled", got)
	}
	if view := m.View().Content; strings.Contains(view, "c to copy") {
		t.Errorf("view still offers to copy:\n%s", view)
	}
}

// A cancelled login still reports how it ended, which is old news by then.
func TestACancelledLoginEndsQuietly(t *testing.T) {
	m, prompt := waitingLogin(t)
	m, _ = updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	done := find[loginDoneMsg](t, run(t, waitForLogin(prompt.events)))
	_, cmd := updateCmd(t, m, done)

	if cmd != nil {
		t.Errorf("the cancelled login's end printed %q", text(run(t, cmd)))
	}
}

// ctrl+c gives the login up, as it does in `elencode login`, rather than
// starting to quit the session.
func TestCtrlCCancelsAWaitingLoginWithoutArmingQuit(t *testing.T) {
	m, _ := waitingLogin(t)

	m, _ = updateCmd(t, m, ctrlC)

	if m.loginCancel != nil {
		t.Error("the login is still waiting")
	}
	if m.quitArmed {
		t.Error("ctrl+c armed quit while it was cancelling the login")
	}
}

// A new prompt is a new page: what was copied before is not it.
func TestANewPromptForgetsTheEarlierCopy(t *testing.T) {
	m, _ := waitingLogin(t)
	m.clipboard, _ = clipboardFile(t)
	m, _ = press(t, m, keyC)

	m, _ = updateCmd(t, m, loginPromptMsg{prompt: loginPrompt{text: "again", page: "https://example.com/other"}, events: make(chan loginEvent)})

	if view := m.View().Content; strings.Contains(view, "copied") || !strings.Contains(view, "c to copy the link") {
		t.Errorf("view after a new page:\n%s", view)
	}
}

// The confirmation goes back down in the session too, leaving c to press
// again.
func TestCopyHintComesBackInTheSession(t *testing.T) {
	m, _ := waitingLogin(t)
	m.clipboard, _ = clipboardFile(t)
	m, _ = press(t, m, keyC)

	m, _ = updateCmd(t, m, copiedExpiredMsg{copies: m.loginPanel.copies})

	if view := m.View().Content; strings.Contains(view, "copied") || !strings.Contains(view, "c to copy the link") {
		t.Errorf("view = %q, want the hint back", view)
	}
}
