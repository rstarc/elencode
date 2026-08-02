// Package commandmenu is the list of slash commands that opens under the input
// as the user types one. It does not own the input: the text is pushed in with
// SetQuery, so the menu cannot disagree with what is on screen.
package commandmenu

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/commands"
	"github.com/rstarc/elencode/internal/tui/menu"
)

// CompleteMsg asks for the input to be replaced with Input, the command the
// user completed with Tab
type CompleteMsg struct{ Input string }

type Model struct {
	registry  commands.Registry
	query     string // the command line as typed, "" when there is none
	dismissed bool   // Esc hides the menu for the rest of this command line
	index     int    // highlighted row within the current match set
	width     int
}

func New(registry commands.Registry) Model {
	return Model{registry: registry}
}

// SetWidth fits the menu to the terminal
func (m *Model) SetWidth(width int) { m.width = width }

// SetQuery follows the input. Visibility is derived from the text rather than
// stored, so the menu and the input cannot disagree about whether a command is
// being typed.
func (m Model) SetQuery(query string) Model {
	m.query = query
	// The match set may have changed under the highlight, so it would otherwise
	// point at a different command than the one the user was looking at.
	m.index = 0
	// Esc dismisses the menu for one command line only; leaving the line clears it.
	if !strings.HasPrefix(query, commands.Prefix) {
		m.dismissed = false
	}
	return m
}

// Visible reports whether the menu is showing, and so whether it is taking the
// keys the input would otherwise get
func (m Model) Visible() bool {
	return !m.dismissed && strings.HasPrefix(m.query, commands.Prefix)
}

// Matches are the commands the current query matches
func (m Model) Matches() []commands.Command { return m.registry.Match(m.query) }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || !m.Visible() {
		return m, nil
	}

	switch key.String() {
	case "esc":
		m.dismissed = true
	case "up", "down":
		delta := 1
		if key.String() == "up" {
			delta = -1
		}
		m.index = menu.MoveHighlight(m.index, delta, len(m.Matches()))
	case "tab":
		matches := m.Matches()
		if len(matches) == 0 {
			return m, nil
		}
		completed := commands.Prefix + matches[m.index].Name
		m.index = 0
		return m, func() tea.Msg { return CompleteMsg{Input: completed} }
	}
	return m, nil
}

// View renders the menu, or "" while it is hidden
func (m Model) View() string {
	if !m.Visible() {
		return ""
	}

	matches := m.Matches()
	items := make([]menu.Item, len(matches))
	for i, c := range matches {
		items[i] = menu.Item{Name: commands.Prefix + c.Name, Description: c.Description}
	}
	return menu.Render(items, m.index, m.width, "no matching command")
}
