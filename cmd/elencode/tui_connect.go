package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/tui/transcript"
)

// connect connects the provider /connect named: a sign-in for ChatGPT, an API
// key for the rest.
func (m model) connect(arg string) (model, tea.Cmd) {
	if strings.TrimSpace(arg) == "" {
		return m.showProviders(), nil
	}
	provider, device, err := parseConnectArgs(strings.Fields(arg))
	if err != nil {
		return m, m.reportError(err)
	}
	if provider == agent.ProviderChatGPT {
		return m.login(device)
	}

	// The input it replaces belongs to what it replaces, as with the model list
	m.input.Reset()
	m.menu = m.menu.SetQuery("")
	m.keyEntry = newKeyEntry(provider)
	if note := environmentWins(m.config, provider); note != "" {
		return m, tea.Sequence(printAbove(transcript.Notice(note, m.width)), textinput.Blink)
	}
	return m, textinput.Blink
}

// showProviders opens the list of providers, at the one in use.
func (m model) showProviders() model {
	// The list borrows the input to filter with, so it starts on an empty one
	m.input.Reset()
	m.menu = m.menu.SetQuery("")
	current, _ := agent.FindModel(m.models, m.config.Model)
	m.providerList = m.providerList.Show(providerStatuses(m.config), func(status providerStatus) bool {
		return status.provider == current.Provider
	})
	return m
}

// pickProvider connects the provider the list is pointing at, or does nothing
// when the filter leaves nothing to point at.
func (m model) pickProvider() (model, tea.Cmd) {
	chosen, ok := m.providerList.Highlighted()
	if !ok {
		return m, nil
	}
	m.providerList = m.providerList.Close()
	m.input.Reset()
	return m.connect(string(chosen.provider))
}

// adoptDefaultModel gives a session that had nothing to talk to the default
// model of what it now can, saved as the model to start on next time. A
// session already on a model keeps it.
func (m model) adoptDefaultModel() (model, tea.Cmd) {
	if m.config.Model != "" {
		return m, nil
	}
	chosen, err := defaultModel(m.providers)
	if err != nil {
		return m, nil
	}
	m.agent.SetModel(chosen, m.providers[chosen.Provider])
	m.config.Model = chosen.Qualified()
	if err := m.config.Save(); err != nil {
		return m, m.reportError(fmt.Errorf("saving the model to %s: %w", m.config.Path, err))
	}
	return m, nil
}

// pressDuringKeyEntry hands a key to the key entry while it is open.
func (m model) pressDuringKeyEntry(msg tea.KeyPressMsg) (model, tea.Cmd) {
	entry, key, cancel, cmd := m.keyEntry.press(msg)
	m.keyEntry = entry
	if cancel {
		m.keyEntry = keyEntry{}
		cancelled := printAbove(transcript.Notice("connect cancelled", m.width))
		// Back to the choice, rather than to a session with nothing to talk to
		if m.firstStart && len(m.providers) == 0 {
			return m.showProviders(), cancelled
		}
		return m, cancelled
	}
	if key != "" {
		return m, checkKeyCmd(context.Background(), m.config, entry.provider, key, m.checkKey)
	}
	return m, cmd
}

// finishConnectingKey makes a saved key a provider of this session, without a
// restart, and offers the provider's models. A rejected key is asked for
// again; an entry given up on has already been reported.
func (m model) finishConnectingKey(msg keyCheckedMsg) (model, tea.Cmd) {
	if !m.keyEntry.open() {
		return m, nil
	}
	if errors.Is(msg.err, agent.ErrKeyRejected) {
		m.keyEntry = m.keyEntry.rejected(msg.err)
		return m, nil
	}
	m.keyEntry = keyEntry{}
	if msg.err != nil {
		return m, m.reportError(fmt.Errorf("connecting %s: %w", msg.provider, msg.err))
	}

	// Only the credentials: the rest of the config is the session's
	m.config.Credentials = msg.cfg.Credentials
	// Resolved as at startup, so the session uses the key the next one will:
	// the environment's, when it supplies one, over the key just saved
	key, _ := m.config.APIKey(msg.provider)
	m = m.replaceClient(msg.provider, m.newKeyed(msg.provider, key.Reveal(), m.config))

	var prints []tea.Cmd
	for _, note := range connectedNotes(m.config, msg.provider, msg.unchecked) {
		prints = append(prints, printAbove(transcript.Notice(note, m.width)))
	}
	m, failed := m.adoptDefaultModel()
	prints = append(prints, failed)

	// Its models are what the user connected it for
	var served []agent.Model
	for _, candidate := range m.availableModels() {
		if candidate.Provider == msg.provider {
			served = append(served, candidate)
		}
	}
	m.modelList = m.modelList.Show(served, func(candidate agent.Model) bool {
		return candidate.Qualified() == m.config.Model
	})
	return m, tea.Sequence(prints...)
}

// replaceClient makes client the one that serves provider, from the next turn
// on. A session on one of its models switches to it straight away, keeping the
// conversation: otherwise a replaced key would stay in use until the next
// /model.
func (m model) replaceClient(provider agent.ProviderName, client agent.Provider) model {
	// A copy: the set the session started with may still be held elsewhere
	providers := maps.Clone(m.providers)
	providers[provider] = client
	m.providers = providers

	if current, ok := agent.FindModel(m.models, m.config.Model); ok && current.Provider == provider {
		m.agent.SetProvider(client)
	}
	return m
}

// disconnect forgets the credential of the provider /disconnect named. A
// session on one of its models is moved to another provider first:
// disconnecting is meant to stop the spending, which keeping the client would
// not.
func (m model) disconnect(name string) (model, tea.Cmd) {
	provider, err := connectProvider(name)
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
			return m, m.reportError(fmt.Errorf("%s is the only provider this session can reach, so disconnecting it would leave it unable to answer: quit and run `elencode disconnect %s`", provider, provider))
		}
	}

	disconnected, said, err := disconnectProvider(m.config, provider)
	if err != nil {
		return m, m.reportError(err)
	}
	m.config.Credentials = disconnected.Credentials
	m.providers = remaining
	notice := printAbove(transcript.Notice(said, m.width))

	if !moving {
		return m, notice
	}
	m, switched := m.selectModel(fallback)
	return m, tea.Sequence(notice, switched)
}
