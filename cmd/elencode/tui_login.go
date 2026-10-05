package main

import (
	"context"
	"fmt"
	"maps"
	"runtime/debug"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
	"github.com/rstarc/elencode/internal/provider/openai"
	"github.com/rstarc/elencode/internal/tui/transcript"
)

// loginEvent is one thing a login in the background has to report, in the
// order it happened: something to ask of the user, or how it ended. One
// channel for both, so the end cannot overtake the last prompt.
type loginEvent struct {
	prompt loginPrompt
	done   bool
	tokens chatgpt.Tokens
	err    error
}

// loginPromptMsg is something the login asks the user to do. events is where
// the rest of it arrives, which the next wait reads.
type loginPromptMsg struct {
	prompt loginPrompt
	events <-chan loginEvent
}

// loginDoneMsg reports how the login ended, with the login saved to disk when
// err is nil.
type loginDoneMsg struct {
	tokens chatgpt.Tokens
	err    error
}

// login signs in as /login asked, in the background. While it waits on the
// user, the login panel has the keyboard: c copies the link, esc gives up.
func (m model) login(arg string) (model, tea.Cmd) {
	_, device, err := parseLoginArgs(strings.Fields(arg))
	if err != nil {
		return m, m.reportError(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.loginCancel = cancel
	m.loginPanel = loginPanel{}

	// Buffered past the most a login says, so it never waits on a session that
	// has stopped reading — after a cancel or a quit, say.
	events := make(chan loginEvent, 8)
	s := m.signIn
	s.device = device
	go func() {
		tokens, err := s.run(ctx, func(prompt loginPrompt) { events <- loginEvent{prompt: prompt} })
		events <- loginEvent{done: true, tokens: tokens, err: err}
	}()
	return m, waitForLogin(events)
}

// waitForLogin delivers the next thing the login reports.
func waitForLogin(events <-chan loginEvent) tea.Cmd {
	return func() tea.Msg {
		event := <-events
		if event.done {
			return loginDoneMsg{tokens: event.tokens, err: event.err}
		}
		return loginPromptMsg{prompt: event.prompt, events: events}
	}
}

// showLoginPrompt prints what the login asks of the user, with the page as a
// link, and goes back to waiting. A login given up is not waited on.
func (m model) showLoginPrompt(msg loginPromptMsg) (model, tea.Cmd) {
	if m.loginCancel == nil {
		return m, nil
	}
	m.loginPanel = m.loginPanel.show(msg.prompt)
	return m, tea.Sequence(printAbove(msg.prompt.render(true)), waitForLogin(msg.events))
}

// pressDuringLogin hands a key to the login panel while a login waits.
func (m model) pressDuringLogin(msg tea.KeyPressMsg) (model, tea.Cmd) {
	panel, cmd, cancel := m.loginPanel.press(msg, m.clipboard)
	m.loginPanel = panel
	if !cancel {
		return m, cmd
	}
	m.loginCancel()
	m.loginCancel = nil
	return m, printAbove(transcript.Notice("login cancelled", m.width))
}

// finishLogin makes a saved login a provider of this session, without a
// restart. A login given up has already been reported.
func (m model) finishLogin(msg loginDoneMsg) (model, tea.Cmd) {
	if m.loginCancel == nil {
		return m, nil
	}
	m.loginCancel()
	m.loginCancel = nil
	if msg.err != nil {
		return m, m.reportError(fmt.Errorf("signing in to ChatGPT: %w", msg.err))
	}

	provider, err := newChatGPTProvider(m.signIn.path, m.config)
	if err != nil {
		return m, m.reportError(err)
	}
	// A copy: the set the session started with may still be held elsewhere
	providers := maps.Clone(m.providers)
	providers[agent.ProviderChatGPT] = provider
	m.providers = providers
	m.config.ChatGPTLoginPath = m.signIn.path

	said := "signed in to ChatGPT" + account(msg.tokens) + ": /model " + openai.ChatGPTDefault().Qualified() + " to use it"
	return m, printAbove(transcript.Notice(said, m.width))
}

// logout signs out of the provider /logout named. A session on one of its
// models is moved to another provider first: signing out is meant to stop the
// spending, which keeping the client would not.
func (m model) logout(name string) (model, tea.Cmd) {
	provider, err := signInProvider(name)
	if err != nil {
		return m, m.reportError(err)
	}
	remaining := maps.Clone(m.providers)
	delete(remaining, provider)

	current, _ := agent.FindModel(m.models, m.config.Model)
	var fallback agent.Model
	moving := current.Provider == provider
	if moving {
		fallback, err = defaultModel(remaining)
		if err != nil {
			return m, m.reportError(fmt.Errorf("%s is the only provider this session can reach, so signing out would leave it unable to answer: quit and run `elencode logout %s`", provider, provider))
		}
	}

	var out strings.Builder
	if err := runLogout(m.signIn.path, &out); err != nil {
		return m, m.reportError(err)
	}
	m.providers = remaining
	m.config.ChatGPTLoginPath = ""
	said := printAbove(transcript.Notice(strings.TrimSpace(out.String()), m.width))

	if !moving {
		return m, said
	}
	m, switched := m.selectModel(fallback)
	return m, tea.Sequence(said, switched)
}

// showVersion prints the line `elencode version` prints.
func (m model) showVersion() (model, tea.Cmd) {
	bi, ok := debug.ReadBuildInfo()
	return m, printAbove(transcript.Notice(versionLine(version, bi, ok), m.width))
}
