package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/commands"
	"github.com/rstarc/elencode/internal/config"
)

// newConnectModel is a session keyed for providers, whose keys are saved in a
// fresh credentials file and checked by check.
func newConnectModel(t *testing.T, providers providerSet, check *fakeCheck) model {
	t.Helper()
	m := newPickerModel(t, providers, testModels)
	m.config.CredentialsPath = filepath.Join(t.TempDir(), "credentials.json")
	m.checkKey = check.check
	return m
}

// submitKey types key into the open key entry and presses enter, returning
// the session and the command that checks the key.
func submitKey(t *testing.T, m model, key string) (model, tea.Cmd) {
	t.Helper()
	m = typeText(t, m, key)
	return updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestConnectCommandAsksForTheKey(t *testing.T) {
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), &fakeCheck{})

	m = update(t, m, commands.ConnectMsg{Arg: "openai"})
	m = typeText(t, m, "sk-secret")

	view := m.View().Content
	for _, want := range []string{"Paste your OpenAI API key", "platform.openai.com/api-keys", "esc cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("view does not mention %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "sk-secret") {
		t.Errorf("view shows the key as typed:\n%s", view)
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want the key typed into the entry instead", m.input.Value())
	}
}

// The old name still works, from the command line as typed.
func TestLoginAliasAsksForTheKey(t *testing.T) {
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), &fakeCheck{})

	m, _ = enter(t, typeText(t, m, "/login openai"))

	if !m.keyEntry.open() || m.keyEntry.provider != agent.ProviderOpenAI {
		t.Errorf("key entry = %+v, want it asking for openai's key", m.keyEntry)
	}
}

// Connected means usable now, without a restart: the client is there, the key
// is saved, and the provider's models are offered to switch to.
func TestConnectingAKeyMakesTheProviderAvailable(t *testing.T) {
	check := &fakeCheck{}
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), check)
	m = update(t, m, commands.ConnectMsg{Arg: "openai"})

	m, cmd := submitKey(t, m, "sk-oai")
	m, printed := updateCmd(t, m, find[keyCheckedMsg](t, run(t, cmd)))

	if len(check.asked) != 1 || check.asked[0] != "sk-oai" {
		t.Errorf("checked %q, want the typed key", check.asked)
	}
	if m.keyEntry.open() {
		t.Error("the key entry is still open")
	}
	if _, ok := m.providers[agent.ProviderOpenAI]; !ok {
		t.Error("no openai client after connecting it")
	}
	saved, err := config.LoadCredentials(m.config.CredentialsPath)
	if err != nil || saved[agent.ProviderOpenAI].APIKey != "sk-oai" {
		t.Errorf("saved = %v, %v, want the key", saved, err)
	}
	if got := text(run(t, printed)); !strings.Contains(got, "Connected openai") {
		t.Errorf("printed %q", got)
	}
	if !m.modelList.Open() {
		t.Fatal("the model list did not open")
	}
	for _, offered := range m.modelList.Matches() {
		if offered.Provider != agent.ProviderOpenAI {
			t.Errorf("offered %s, want only openai's models", offered.Qualified())
		}
	}
}

// A rejected key is cleared and asked for again, with the reason, rather than
// ending the connect: a typo is the likeliest cause.
func TestARejectedKeyIsAskedForAgain(t *testing.T) {
	check := &fakeCheck{answers: map[string]error{"sk-typo": errRejected}}
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), check)
	m = update(t, m, commands.ConnectMsg{Arg: "openai"})

	m, cmd := submitKey(t, m, "sk-typo")
	m = update(t, m, find[keyCheckedMsg](t, run(t, cmd)))

	if !m.keyEntry.open() {
		t.Fatal("the key entry closed on a rejected key")
	}
	if view := m.View().Content; !strings.Contains(view, "rejected") {
		t.Errorf("view does not say the key was rejected:\n%s", view)
	}
	if m.keyEntry.input.Value() != "" {
		t.Error("the rejected key is still in the input")
	}
	if _, err := os.Stat(m.config.CredentialsPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat = %v, want nothing saved", err)
	}
	if _, ok := m.providers[agent.ProviderOpenAI]; ok {
		t.Error("a rejected key became a provider")
	}
}

func TestEscCancelsConnecting(t *testing.T) {
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), &fakeCheck{})
	m = update(t, m, commands.ConnectMsg{Arg: "openai"})

	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.keyEntry.open() {
		t.Error("the key entry is still open")
	}
	if got := text(run(t, cmd)); !strings.Contains(got, "cancelled") {
		t.Errorf("printed %q", got)
	}
}

// A check that returns after the entry was given up on must not connect
// anything behind the user's back.
func TestACheckFinishingAfterCancelIsIgnored(t *testing.T) {
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), &fakeCheck{})
	m = update(t, m, commands.ConnectMsg{Arg: "openai"})
	m, cmd := submitKey(t, m, "sk-oai")
	checked := find[keyCheckedMsg](t, run(t, cmd))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	m = update(t, m, checked)

	if _, ok := m.providers[agent.ProviderOpenAI]; ok {
		t.Error("a cancelled connect added the provider")
	}
}

func TestConnectSaysWhenTheEnvironmentWins(t *testing.T) {
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), &fakeCheck{})
	m.config.Env = func(name string) (string, bool) { return "sk-env", name == "OPENAI_API_KEY" }

	_, cmd := updateCmd(t, m, commands.ConnectMsg{Arg: "openai"})

	if got := text(run(t, cmd)); !strings.Contains(got, "$OPENAI_API_KEY is set") {
		t.Errorf("printed %q", got)
	}
}

// /connect on its own is where the providers are seen: each, whether it is
// connected, and with what.
func TestConnectWithoutAProviderListsThem(t *testing.T) {
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), &fakeCheck{})
	m.config.Credentials = config.Credentials{agent.ProviderAnthropic: {APIKey: "sk-ant"}}

	m = update(t, m, commands.ConnectMsg{})

	if !m.providerList.Open() {
		t.Fatal("the provider list did not open")
	}
	view := m.View().Content
	for _, want := range []string{"anthropic", "connected · API key in credentials.json", "openai", "not connected · API key", "chatgpt"} {
		if !strings.Contains(view, want) {
			t.Errorf("view does not mention %q:\n%s", want, view)
		}
	}
}

func TestChoosingFromTheProviderListConnectsIt(t *testing.T) {
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), &fakeCheck{})
	m = update(t, m, commands.ConnectMsg{})

	m = typeText(t, m, "open")
	m, _ = updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.providerList.Open() {
		t.Error("the provider list is still open")
	}
	if !m.keyEntry.open() || m.keyEntry.provider != agent.ProviderOpenAI {
		t.Errorf("key entry = %+v, want it asking for openai's key", m.keyEntry)
	}
}

// newFirstStart is a session started with nothing connected, before the
// terminal has said how wide it is.
func newFirstStart(t *testing.T, check *fakeCheck) model {
	t.Helper()
	m := newConnectModel(t, providerSet{}, check)
	m.firstStart = true
	return m.showProviders()
}

func TestAFirstStartOpensOnTheProviderList(t *testing.T) {
	m := update(t, newFirstStart(t, &fakeCheck{}), tea.WindowSizeMsg{Width: 80, Height: 20})

	if !m.providerList.Open() {
		t.Error("the provider list is not open")
	}
	if got := m.intro(80); !strings.Contains(got, "No provider is connected yet") {
		t.Errorf("intro is %q, want a welcome", got)
	}
}

// With nothing connected there is nothing to talk to, so leaving the list is
// leaving.
func TestEscOnAFirstStartQuits(t *testing.T) {
	m := update(t, newFirstStart(t, &fakeCheck{}), tea.WindowSizeMsg{Width: 80, Height: 20})

	_, cmd := press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if !quits(cmd) {
		t.Error("esc on a first start did not quit")
	}
}

// Connecting the first provider gives the session a model to talk to, even
// if the list of its models is closed without a choice.
func TestTheFirstProviderConnectedGivesTheSessionAModel(t *testing.T) {
	m := update(t, newFirstStart(t, &fakeCheck{}), tea.WindowSizeMsg{Width: 80, Height: 20})
	m, _ = updateCmd(t, typeText(t, m, "anthro"), tea.KeyPressMsg{Code: tea.KeyEnter})
	m, cmd := submitKey(t, m, "sk-ant")
	m = update(t, m, find[keyCheckedMsg](t, run(t, cmd)))

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if !strings.HasPrefix(m.config.Model, "anthropic/") {
		t.Errorf("model = %q, want one of anthropic's", m.config.Model)
	}
}

// Nothing is connected until /connect connects something, and a message
// typed before then, with the list put away, has nobody to go to.
func TestATurnWithNothingConnectedSaysToConnect(t *testing.T) {
	m := update(t, newFirstStart(t, &fakeCheck{}), tea.WindowSizeMsg{Width: 80, Height: 20})
	m.providerList = m.providerList.Close()

	m = typeText(t, m, "hello")
	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.state != uiStateIdle {
		t.Error("a turn started with nothing to talk to")
	}
	if got := text(run(t, cmd)); !strings.Contains(got, "/connect") {
		t.Errorf("printed %q, want it to point at /connect", got)
	}
}

// Giving up on a key during a first start goes back to the list: an empty
// session with nothing connected would leave the user nowhere to go.
func TestCancellingTheKeyOnAFirstStartGoesBackToTheList(t *testing.T) {
	m := update(t, newFirstStart(t, &fakeCheck{}), tea.WindowSizeMsg{Width: 80, Height: 20})
	m, _ = updateCmd(t, typeText(t, m, "open"), tea.KeyPressMsg{Code: tea.KeyEnter})

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if !m.providerList.Open() {
		t.Error("the provider list did not come back")
	}
}

// Replacing the key of the provider in use takes effect on the next turn,
// without a /model to pick the new client up and without losing the
// conversation.
func TestReplacingTheKeyInUseTakesEffectAtOnce(t *testing.T) {
	old, renewed := &recordingProvider{}, &recordingProvider{}
	m := newConnectModel(t, providerSet{agent.ProviderAnthropic: old}, &fakeCheck{})
	m.agent.SetModel(testModels[0], old)
	m.config.Model = testModels[0].Qualified()
	m.newKeyed = func(agent.ProviderName, string, config.Config) agent.Provider { return renewed }

	m = update(t, m, commands.ConnectMsg{Arg: "anthropic"})
	m, cmd := submitKey(t, m, "sk-new")
	m = update(t, m, find[keyCheckedMsg](t, run(t, cmd)))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}) // keep the model in use
	m, cmd = updateCmd(t, typeText(t, m, "hello"), tea.KeyPressMsg{Code: tea.KeyEnter})
	run(t, cmd)

	if len(old.requests) != 0 || len(renewed.requests) != 1 {
		t.Errorf("old client served %d turns, new one %d, want the new one to", len(old.requests), len(renewed.requests))
	}
}

// A key is pasted far more often than typed, and a terminal delivers a paste
// as one message of its own rather than as key presses.
func TestAPastedKeyIsConnected(t *testing.T) {
	check := &fakeCheck{}
	m := newConnectModel(t, keyed(agent.ProviderAnthropic), check)
	m = update(t, m, commands.ConnectMsg{Arg: "openai"})

	m = update(t, m, tea.PasteMsg{Content: "sk-pasted\n"})
	_, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	find[keyCheckedMsg](t, run(t, cmd))

	if got := strings.Join(check.asked, ","); got != "sk-pasted" {
		t.Errorf("checked %q, want the pasted key", got)
	}
}
