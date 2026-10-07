package main

import (
	"context"
	"fmt"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
	"github.com/rstarc/elencode/internal/config"
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

// login signs in as /connect chatgpt asked, in the background. While it
// waits on the user, the login panel has the keyboard: c copies the link, esc
// gives up. device asks for a code rather than the browser.
func (m model) login(device bool) (model, tea.Cmd) {
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
	cancelled := printAbove(transcript.Notice("login cancelled", m.width))
	// Back to the choice, rather than to a session with nothing to talk to
	if m.firstStart && len(m.providers) == 0 {
		return m.showProviders(), cancelled
	}
	return m, cancelled
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

	m.config.Credentials = m.config.Credentials.With(agent.ProviderChatGPT, config.Credential{Login: &msg.tokens})
	provider, err := newChatGPTProvider(m.config)
	if err != nil {
		return m, m.reportError(err)
	}
	m = m.replaceClient(agent.ProviderChatGPT, provider)

	said := "signed in to ChatGPT" + account(msg.tokens) + ": /model " + openai.ChatGPTDefault().Qualified() + " to use it"
	m, failed := m.adoptDefaultModel()
	return m, tea.Sequence(printAbove(transcript.Notice(said, m.width)), failed)
}

// showVersion prints the line `elencode version` prints.
func (m model) showVersion() (model, tea.Cmd) {
	bi, ok := debug.ReadBuildInfo()
	return m, printAbove(transcript.Notice(versionLine(version, bi, ok), m.width))
}
