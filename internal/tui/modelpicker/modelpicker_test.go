package modelpicker

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/tui/menu"
)

var testModels = []agent.Model{
	{Provider: agent.ProviderAnthropic, ID: "model-one", DisplayName: "Model One"},
	{Provider: agent.ProviderOpenAI, ID: "model-two", DisplayName: "Model Two"},
}

// shown builds an open picker, the state every key test starts from
func shown(current string) Model {
	p := New()
	p.SetWidth(80)
	return p.Show(testModels, current)
}

func press(p Model, key tea.KeyPressMsg) (Model, tea.Cmd) { return p.Update(key) }

func TestNewPickerIsClosed(t *testing.T) {
	if New().Focused() {
		t.Error("a fresh picker has the keyboard, want it closed until Show")
	}
}

func TestShowOpensThePicker(t *testing.T) {
	p := shown("")

	if !p.Focused() {
		t.Error("picker is not focused after Show")
	}
	view := p.View()
	for _, want := range testModels {
		if !strings.Contains(view, want.ID) {
			t.Errorf("picker does not offer %q:\n%s", want.ID, view)
		}
		// The id is what the user types after /model; the display name is what
		// they recognise. Both are shown.
		if !strings.Contains(view, want.DisplayName) {
			t.Errorf("picker does not show the display name of %q:\n%s", want.ID, view)
		}
	}
}

// The list mixes providers, so a row that showed only the id would leave the
// user guessing which API they are about to switch to.
func TestEveryRowNamesItsProvider(t *testing.T) {
	view := shown("").View()

	for _, want := range testModels {
		if !strings.Contains(view, string(want.Provider)) {
			t.Errorf("picker does not say %q serves %q:\n%s", want.Provider, want.ID, view)
		}
	}
}

// TestShowStartsOnTheCurrentModel saves the user from hunting for where they
// already are in a list of twenty.
func TestShowStartsOnTheCurrentModel(t *testing.T) {
	p := shown("openai/model-two")

	if p.index != 1 {
		t.Errorf("index = %d, want 1 (the model in use)", p.index)
	}
}

func TestShowStartsAtTheTopForAnUnknownModel(t *testing.T) {
	p := shown("anthropic/model-nine")

	if p.index != 0 {
		t.Errorf("index = %d, want 0 when the model in use is not in the list", p.index)
	}
}

func TestClosedPickerRendersNothing(t *testing.T) {
	if view := New().View(); view != "" {
		t.Errorf("closed picker rendered %q, want nothing", view)
	}
}

func TestArrowsMoveTheHighlight(t *testing.T) {
	p := shown("")

	p, _ = press(p, tea.KeyPressMsg{Code: tea.KeyDown})
	if p.index != 1 {
		t.Errorf("index = %d after down, want 1", p.index)
	}

	p, _ = press(p, tea.KeyPressMsg{Code: tea.KeyUp})
	if p.index != 0 {
		t.Errorf("index = %d after up, want 0", p.index)
	}
}

func TestArrowsClampAtTheEnds(t *testing.T) {
	p := shown("")

	p, _ = press(p, tea.KeyPressMsg{Code: tea.KeyUp})
	if p.index != 0 {
		t.Errorf("index = %d, want it clamped at the first row", p.index)
	}

	for range len(testModels) + 2 {
		p, _ = press(p, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if want := len(testModels) - 1; p.index != want {
		t.Errorf("index = %d, want it clamped at the last row (%d)", p.index, want)
	}
}

func TestEnterSelectsTheHighlightedModel(t *testing.T) {
	p := shown("")

	p, _ = press(p, tea.KeyPressMsg{Code: tea.KeyDown})
	p, cmd := press(p, tea.KeyPressMsg{Code: tea.KeyEnter})

	if cmd == nil {
		t.Fatal("Enter produced no command, want a selection")
	}
	msg, ok := cmd().(SelectedMsg)
	if !ok {
		t.Fatalf("Enter produced %T, want SelectedMsg", cmd())
	}
	if msg.Model.ID != "model-two" {
		t.Errorf("selected %q, want %q", msg.Model.ID, "model-two")
	}
	if p.Focused() {
		t.Error("picker still has the keyboard after a choice")
	}
}

func TestEscClosesThePicker(t *testing.T) {
	p := shown("")

	p, cmd := press(p, tea.KeyPressMsg{Code: tea.KeyEscape})

	if p.Focused() {
		t.Error("picker still has the keyboard after Esc")
	}
	if cmd == nil {
		t.Fatal("Esc produced no command, want it to report the close")
	}
	if _, ok := cmd().(ClosedMsg); !ok {
		t.Errorf("Esc produced %T, want ClosedMsg", cmd())
	}
}

// TestSwallowsTypedKeys keeps the picker's keyboard to itself: a keystroke that
// reached the input would open the command menu underneath it.
func TestSwallowsTypedKeys(t *testing.T) {
	p := shown("")

	p, cmd := press(p, tea.KeyPressMsg{Code: '/', Text: "/"})

	if !p.Focused() {
		t.Error("typing closed the picker")
	}
	if cmd != nil {
		t.Errorf("typing produced %T, want the key swallowed", cmd())
	}
}

// TestEnterOnAnEmptyListDoesNothing guards the index: there is no model to
// select, and reading models[0] would panic.
func TestEnterOnAnEmptyListDoesNothing(t *testing.T) {
	p := New()
	p.SetWidth(80)
	p = p.Show(nil, "")

	p, cmd := press(p, tea.KeyPressMsg{Code: tea.KeyEnter})

	if cmd != nil {
		t.Errorf("Enter on an empty list produced %T, want nothing", cmd())
	}
	if !p.Focused() {
		t.Error("picker closed itself on an empty list, want Esc to be the way out")
	}
}

func TestViewFitsItsWidth(t *testing.T) {
	const width = 30

	p := New()
	p.SetWidth(width)
	p = p.Show(testModels, "")

	for i, line := range strings.Split(p.View(), "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("row %d is %d columns wide, want <= %d:\n%s", i, got, width, line)
		}
	}
}

// TestViewCapsItsHeight keeps a long list from pushing the transcript off the
// screen: the API offers far more models than the picker has room for.
func TestViewCapsItsHeight(t *testing.T) {
	var many []agent.Model
	for i := range menu.MaxRows * 2 {
		many = append(many, agent.Model{ID: fmt.Sprintf("model-%d", i), DisplayName: "Model"})
	}

	p := New()
	p.SetWidth(80)
	p = p.Show(many, "")

	if got := len(strings.Split(p.View(), "\n")); got > menu.MaxRows {
		t.Errorf("picker is %d rows tall, want at most %d", got, menu.MaxRows)
	}
}

func TestViewReportsAnEmptyList(t *testing.T) {
	p := New()
	p.SetWidth(80)
	p = p.Show(nil, "")

	if view := p.View(); !strings.Contains(view, "no models") {
		t.Errorf("picker does not report an empty list:\n%s", view)
	}
}
