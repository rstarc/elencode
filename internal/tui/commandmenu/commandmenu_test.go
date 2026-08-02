package commandmenu

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/commands"
)

func fixture() Model {
	m := New(commands.NewRegistry(
		commands.Command{Name: "config", Description: "the first one"},
		commands.Command{Name: "model", Description: "the second one"},
		commands.Command{Name: "quit", Description: "the third one"},
	))
	m.SetWidth(80)
	return m
}

// opened is a menu on a command line, the state every key test starts from
func opened(query string) Model { return fixture().SetQuery(query) }

func TestMenuIsHiddenForPlainInput(t *testing.T) {
	m := opened("hello")

	if m.Visible() {
		t.Error("menu is visible for input that is not a command line")
	}
	if view := m.View(); view != "" {
		t.Errorf("hidden menu rendered %q, want nothing", view)
	}
}

func TestSlashOpensTheMenu(t *testing.T) {
	m := opened(commands.Prefix)

	if !m.Visible() {
		t.Error("menu is not visible on a command line")
	}
	if view := m.View(); !strings.Contains(view, "/quit") {
		t.Errorf("menu does not list the commands:\n%s", view)
	}
}

func TestMenuNarrowsAsTheUserTypes(t *testing.T) {
	view := opened("/qu").View()

	if !strings.Contains(view, "/quit") {
		t.Errorf("menu dropped the matching command:\n%s", view)
	}
	if strings.Contains(view, "/config") {
		t.Errorf("menu still lists a command that no longer matches:\n%s", view)
	}
}

func TestMenuReportsAnEmptyMatchSet(t *testing.T) {
	// Silence would be ambiguous: the user cannot tell a live menu with no
	// matches from a menu that never opened.
	if view := opened("/zzz").View(); !strings.Contains(view, "no matching command") {
		t.Errorf("menu does not report an empty match set:\n%s", view)
	}
}

func TestEscDismissesTheMenuForTheRestOfTheLine(t *testing.T) {
	m := opened(commands.Prefix)

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.Visible() {
		t.Error("menu still visible after Esc")
	}

	// Typing on must not revive it, or Esc only hides the menu for one keystroke
	m = m.SetQuery("/q")
	if m.Visible() {
		t.Error("menu came back after typing")
	}
}

func TestMenuReopensOnANewCommandLine(t *testing.T) {
	m := opened(commands.Prefix)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	// Leaving the command line ends the dismissed line; the next one starts fresh
	m = m.SetQuery("")
	m = m.SetQuery(commands.Prefix)

	if !m.Visible() {
		t.Error("menu stayed dismissed on a new command line")
	}
}

func TestArrowsMoveTheHighlight(t *testing.T) {
	m := opened(commands.Prefix)

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.index != 1 {
		t.Errorf("index = %d after down, want 1", m.index)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.index != 0 {
		t.Errorf("index = %d after up, want 0", m.index)
	}
}

func TestArrowsClampAtTheEnds(t *testing.T) {
	m := opened(commands.Prefix)

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.index != 0 {
		t.Errorf("index = %d, want it clamped at the first row", m.index)
	}

	for range 5 {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.index != 2 {
		t.Errorf("index = %d, want it clamped at the last row (2)", m.index)
	}
}

// TestTypingResetsTheHighlight keeps the selection meaningful: the match set
// just changed, so the old index may point at another command.
func TestTypingResetsTheHighlight(t *testing.T) {
	m := opened(commands.Prefix)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	m = m.SetQuery("/q")

	if m.index != 0 {
		t.Errorf("index = %d, want it reset to 0 when the matches change", m.index)
	}
}

func TestTabCompletesTheHighlightedCommand(t *testing.T) {
	m := opened(commands.Prefix)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if cmd == nil {
		t.Fatal("Tab produced no command, want a completion")
	}
	msg, ok := cmd().(CompleteMsg)
	if !ok {
		t.Fatalf("Tab produced %T, want CompleteMsg", cmd())
	}
	if want := commands.Prefix + "model"; msg.Input != want {
		t.Errorf("completed to %q, want %q", msg.Input, want)
	}
}

func TestTabWithNoMatchesDoesNothing(t *testing.T) {
	m := opened("/zzz")

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if cmd != nil {
		t.Errorf("Tab with no matches produced %T, want nothing", cmd())
	}
}

// TestHiddenMenuIgnoresKeys matters because the parent forwards arrows and Tab
// to the input when the menu is not showing: handling them here too would move
// a highlight nobody can see.
func TestHiddenMenuIgnoresKeys(t *testing.T) {
	m := opened("hello")

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if cmd != nil {
		t.Errorf("a hidden menu produced %T, want nothing", cmd())
	}
	if m.index != 0 {
		t.Error("a hidden menu moved its highlight")
	}
}

func TestViewFitsItsWidth(t *testing.T) {
	const width = 30

	m := fixture()
	m.SetWidth(width)
	m = m.SetQuery(commands.Prefix)

	for i, line := range strings.Split(m.View(), "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("row %d is %d columns wide, want <= %d:\n%s", i, got, width, line)
		}
	}
}
