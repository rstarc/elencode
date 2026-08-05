// Package picker is the list the TUI opens over the input: the slash commands
// as one is typed, the models /model offers. It owns the list, the query and
// the highlight, and reports what the user did as a message.
//
// It does not own the input. There is one text input and the picker only
// borrows it, so the query is pushed in with SetQuery and the entry under the
// highlight is pushed back out as a PreviewMsg. Nor does it decide what
// choosing means: whoever opened it reads Highlighted and acts.
package picker

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/tui/menu"
)

// PreviewMsg asks for the input to be replaced with Text, the entry the
// highlight moved onto
type PreviewMsg struct{ Text string }

// ClosedMsg reports that the user left the list without choosing
type ClosedMsg struct{}

// Config decides what a picker is: how an entry is drawn, when it is open, and
// what it says when nothing matches.
type Config[T any] struct {
	// Render draws one entry. Its Name is also what the entry is matched and
	// previewed by, so it is what the user types to reach it.
	Render func(T) menu.Item
	// Match reports whether an entry survives the query. How a list is searched
	// is the list's own business: what reads well over three short command names
	// reads badly over twenty model ids.
	Match func(query, name string) bool
	// Trigger opens the picker for as long as the query starts with it, the way
	// a slash opens the command menu. Empty means the picker opens on Show
	// instead, and stays open until it is closed.
	Trigger string
	// Align pads names into a column, for ids as different in length as
	// "claude-opus-5" and "claude-sonnet-4-5-20250929"
	Align bool
	// Empty is shown in place of the rows when nothing matches
	Empty string
}

type Model[T any] struct {
	cfg   Config[T]
	items []T
	// query is what the user typed, and never what the arrow keys wrote: see
	// preview.
	query string
	index int // highlight within the current match set
	width int
	shown bool // opened by Show; only consulted when there is no trigger
}

func New[T any](cfg Config[T], items ...T) Model[T] {
	return Model[T]{cfg: cfg, items: items}
}

// SetWidth fits the picker to the terminal
func (m *Model[T]) SetWidth(width int) { m.width = width }

// SetQuery follows the input. A triggered picker derives being open from the
// text rather than storing it, so it and the input cannot disagree about
// whether a command is being typed.
func (m Model[T]) SetQuery(query string) Model[T] {
	m.query = query
	// The match set may have changed under the highlight, so it would otherwise
	// point at a different entry than the one the user was looking at.
	m.index = 0
	return m
}

// Show opens the picker on items, highlighting the first one current reports,
// so a list that already has an answer starts on it. A nil current starts at
// the top.
func (m Model[T]) Show(items []T, current func(T) bool) Model[T] {
	m.items = items
	m.query = ""
	m.index = 0
	m.shown = true

	if current == nil {
		return m
	}
	for i, item := range items {
		if current(item) {
			m.index = i
			break
		}
	}
	return m
}

// Close hides the picker and forgets the query, so a triggered picker does not
// reopen itself on text the user has left behind. The caller clears the input
// to match.
func (m Model[T]) Close() Model[T] {
	m.query = ""
	m.index = 0
	m.shown = false
	return m
}

// Open reports whether the picker is showing, and so whether it is taking the
// keys the input would otherwise get
func (m Model[T]) Open() bool {
	if m.cfg.Trigger != "" {
		return strings.HasPrefix(m.query, m.cfg.Trigger)
	}
	return m.shown
}

// Matches are the entries the current query matches, in the order they were
// given. There is no scoring.
func (m Model[T]) Matches() []T {
	if !m.Open() {
		return nil
	}

	var matches []T
	for _, item := range m.items {
		if m.cfg.Match(m.query, m.cfg.Render(item).Name) {
			matches = append(matches, item)
		}
	}
	return matches
}

// Highlighted is the entry the picker is pointing at, and whether there is one
// at all: a closed picker and an empty match set have nothing to choose.
func (m Model[T]) Highlighted() (T, bool) {
	matches := m.Matches()
	if len(matches) == 0 {
		var none T
		return none, false
	}
	return matches[m.index], true
}

func (m Model[T]) Update(msg tea.Msg) (Model[T], tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || !m.Open() {
		return m, nil
	}

	switch key.String() {
	case "esc":
		return m.Close(), func() tea.Msg { return ClosedMsg{} }
	case "up", "down":
		delta := 1
		if key.String() == "up" {
			delta = -1
		}
		moved := menu.MoveHighlight(m.index, delta, len(m.Matches()))
		if moved == m.index {
			// Nowhere to go: at either end, or on a list of one. Previewing anyway
			// would overwrite the line the row was matched by, which on "/model
			// some-id" is the argument.
			return m, nil
		}
		m.index = moved
		return m, m.preview()
	case "tab":
		return m, m.preview()
	}
	return m, nil
}

// preview asks for the input to show the entry under the highlight, so what is
// typed is what choosing would pick. The query is deliberately left alone: a
// list filtered by the name it is highlighting would narrow to that one row,
// and there would be nowhere left to move.
func (m Model[T]) preview() tea.Cmd {
	highlighted, ok := m.Highlighted()
	if !ok {
		return nil
	}

	text := m.cfg.Render(highlighted).Name
	return func() tea.Msg { return PreviewMsg{Text: text} }
}

// View renders the picker, or "" while it is closed
func (m Model[T]) View() string {
	if !m.Open() {
		return ""
	}

	matches := m.Matches()
	items := make([]menu.Item, len(matches))
	for i, match := range matches {
		items[i] = m.cfg.Render(match)
	}
	if m.cfg.Align {
		items = menu.Align(items)
	}
	return menu.Render(items, m.index, m.width, m.cfg.Empty)
}
