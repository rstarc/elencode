// Package modelpicker is the list of models /model opens. It holds the keyboard
// while it is open and reports what the user chose as a message, leaving the
// consequences of a switch — clearing the context window, saving the choice —
// to whoever opened it.
package modelpicker

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/tui/menu"
)

// SelectedMsg reports the model the user chose
type SelectedMsg struct{ Model agent.Model }

// ClosedMsg reports that the user dismissed the picker without choosing
type ClosedMsg struct{}

type Model struct {
	models  []agent.Model
	index   int  // highlighted row within models
	visible bool // whether the picker is open, and so has the keyboard
	width   int
}

func New() Model { return Model{} }

// SetWidth fits the picker to the terminal
func (m *Model) SetWidth(width int) { m.width = width }

// Focused reports whether the picker has the keyboard. Every key but the ones
// the caller reserves for itself belongs to the picker while it is open: one
// reaching the input underneath would open the command menu behind it.
func (m Model) Focused() bool { return m.visible }

// Show opens the picker on models, highlighting current so the user starts
// where they already are rather than having to find it. current is the
// qualified name, since the list mixes providers and two of them could offer
// the same id.
func (m Model) Show(models []agent.Model, current string) Model {
	m.models = models
	m.visible = true
	m.index = 0
	for i, candidate := range models {
		if candidate.Qualified() == current {
			m.index = i
		}
	}
	return m
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || !m.visible {
		return m, nil
	}

	switch key.String() {
	case "esc":
		m = m.close()
		return m, func() tea.Msg { return ClosedMsg{} }
	case "up", "down":
		delta := 1
		if key.String() == "up" {
			delta = -1
		}
		m.index = menu.MoveHighlight(m.index, delta, len(m.models))
	case "enter":
		if len(m.models) == 0 {
			// Nothing to choose. Esc remains the way out, so the user is not
			// stuck in a picker that ignores them entirely.
			return m, nil
		}
		chosen := m.models[m.index]
		m = m.close()
		return m, func() tea.Msg { return SelectedMsg{Model: chosen} }
	}
	// Anything else is swallowed rather than forwarded
	return m, nil
}

// close hides the picker and forgets the list, which is refetched next time
func (m Model) close() Model {
	m.models = nil
	m.visible = false
	m.index = 0
	return m
}

// View renders the picker, or "" while it is closed. The id is shown first
// because it is what the user types after /model; the provider and display
// name are there to recognise it by, and the list mixes providers, so which
// one a model belongs to has to be on the row.
func (m Model) View() string {
	if !m.visible {
		return ""
	}

	items := make([]menu.Item, len(m.models))
	for i, model := range m.models {
		items[i] = menu.Item{Name: model.ID, Description: string(model.Provider) + " · " + model.DisplayName}
	}
	return menu.Render(menu.Align(items), m.index, m.width, "no models available")
}
