package main

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/tui/menu"
)

// keyEntry is what is shown while connecting a provider waits on its API key:
// a masked input, the page that issues keys, and why the last key was not
// taken. Shared by the session and `elencode connect` on a terminal, so both
// answer the same keys the same way. The zero value is closed.
type keyEntry struct {
	provider agent.ProviderName
	input    textinput.Model
	checking bool   // a key was submitted and the API has not answered yet
	problem  string // why the last key was not taken, until another is typed
}

func newKeyEntry(provider agent.ProviderName) keyEntry {
	input := textinput.New()
	input.Prompt = "> "
	// Masked as it is typed or pasted: a key on screen is a key in a
	// screenshot, a screen share, or the scrollback
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '•'
	input.CharLimit = 0
	input.Focus()
	return keyEntry{provider: provider, input: input}
}

// open reports whether a key is being asked for.
func (e keyEntry) open() bool { return e.provider != "" }

// press handles a key while the entry is open. Enter submits the key for the
// caller to check, esc or ctrl+c gives up, which cancel reports. While a key
// is being checked the input is held, so the key checked is the key shown.
func (e keyEntry) press(msg tea.KeyPressMsg) (next keyEntry, submitted string, cancel bool, cmd tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		return e, "", true, nil
	case "enter":
		key := strings.TrimSpace(e.input.Value())
		if e.checking || key == "" {
			return e, "", false, nil
		}
		e.checking = true
		e.problem = ""
		return e, key, false, nil
	}
	if e.checking {
		return e, "", false, nil
	}
	e.input, cmd = e.input.Update(msg)
	return e, "", false, cmd
}

// paste takes in pasted text, which is how a key usually arrives. Like a key
// press it is held while a key is being checked.
func (e keyEntry) paste(msg tea.PasteMsg) (keyEntry, tea.Cmd) {
	if e.checking {
		return e, nil
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return e, cmd
}

// rejected takes in that the API refused the key: it is cleared, and the
// entry says why, for the next one.
func (e keyEntry) rejected(err error) keyEntry {
	e.checking = false
	e.problem = err.Error()
	e.input.Reset()
	return e
}

var problemColor = lipgloss.Red

func (e keyEntry) view() string {
	dim := lipgloss.NewStyle().Foreground(menu.DescriptionColor)
	lines := []string{"Paste your " + providerTitles[e.provider] + " API key"}
	if page := apiKeyPages[e.provider]; page != "" {
		lines = append(lines, dim.Render("(create one at "+page+")"))
	}
	lines = append(lines, "", e.input.View())
	if e.problem != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(problemColor).Render(e.problem))
	}
	hint := "enter save · esc cancel"
	if e.checking {
		hint = "checking the key..."
	}
	return strings.Join(append(lines, "", dim.Render(hint)), "\n")
}

// keyCheckedMsg is how checking and saving a submitted key came out. cfg
// holds the key when it was saved; err is set when it was not.
type keyCheckedMsg struct {
	provider  agent.ProviderName
	cfg       config.Config
	unchecked error
	err       error
}

// checkKeyCmd checks key and saves it, off the update loop: the check is a
// request to the API.
func checkKeyCmd(ctx context.Context, cfg config.Config, provider agent.ProviderName, key string, check keyCheck) tea.Cmd {
	return func() tea.Msg {
		connected, unchecked, err := connectKey(ctx, cfg, provider, key, check)
		return keyCheckedMsg{provider: provider, cfg: connected, unchecked: unchecked, err: err}
	}
}
