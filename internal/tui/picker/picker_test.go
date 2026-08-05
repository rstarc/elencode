package picker

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/tui/menu"
)

// entry stands in for whatever a picker picks. The tests care only that a value
// can be drawn as a row, so they do not depend on commands or models.
type entry struct{ name, description string }

func render(e entry) menu.Item { return menu.Item{Name: e.name, Description: e.description} }

var commands = []entry{
	{"/config", "the first one"},
	{"/model", "the second one"},
	{"/quit", "the third one"},
}

var models = []entry{
	{"model-one", "Model One"},
	{"model-two", "Model Two"},
}

// triggered is a picker that opens as the user types, the way the command menu
// does. Its entries carry the trigger, as a command name does.
func triggered() Model[entry] {
	p := New(Config[entry]{Render: render, Trigger: "/", Empty: "no matching command"}, commands...)
	p.SetWidth(80)
	return p
}

// opened is a triggered picker on a command line, the state every key test
// starts from
func opened(query string) Model[entry] { return triggered().SetQuery(query) }

// closedList is a picker whoever owns it opens, the way /model opens the model
// list
func closedList(items ...entry) Model[entry] {
	p := New(Config[entry]{Render: render, Align: true, Empty: "the API offered no models"}, items...)
	p.SetWidth(80)
	return p
}

// shown is that list opened on current, or on its first row when nothing
// matches current
func shown(current string) Model[entry] {
	return closedList().Show(models, func(e entry) bool { return e.name == current })
}

func TestTriggerOpensThePicker(t *testing.T) {
	if p := opened("/"); !p.Open() {
		t.Error("the trigger did not open the picker")
	}
}

func TestPlainQueryLeavesTheTriggeredPickerClosed(t *testing.T) {
	if p := opened("hello"); p.Open() {
		t.Error("picker is open for a query that does not start with the trigger")
	}
}

func TestAListIsClosedUntilItIsShown(t *testing.T) {
	if p := closedList(models...); p.Open() {
		t.Error("a picker with no trigger is open before Show")
	}
}

func TestShowOpensTheList(t *testing.T) {
	if p := shown(""); !p.Open() {
		t.Error("Show did not open the picker")
	}
}

// TestShowStartsOnTheCurrentEntry saves the user from hunting for where they
// already are in a long list.
func TestShowStartsOnTheCurrentEntry(t *testing.T) {
	p := shown("model-two")

	highlighted, ok := p.Highlighted()
	if !ok {
		t.Fatal("nothing is highlighted, want the current entry")
	}
	if highlighted.name != "model-two" {
		t.Errorf("highlighted %q, want %q", highlighted.name, "model-two")
	}
}

func TestShowStartsAtTheTopWhenNothingIsCurrent(t *testing.T) {
	p := shown("model-nine")

	highlighted, _ := p.Highlighted()
	if highlighted.name != "model-one" {
		t.Errorf("highlighted %q, want the first row", highlighted.name)
	}
}

func TestQueryNarrowsTheMatches(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"the trigger alone lists everything", "/", []string{"/config", "/model", "/quit"}},
		{"exact name", "/quit", []string{"/quit"}},
		{"prefix", "/qu", []string{"/quit"}},
		{"subsequence", "/qt", []string{"/quit"}},
		{"case insensitive", "/QUIT", []string{"/quit"}},
		{"a shared letter matches several", "/i", []string{"/config", "/quit"}},
		{"no match", "/zzz", nil},
		{"out of order is not a subsequence", "/tq", nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := names(opened(test.query).Matches())
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("matches = %v, want %v", got, test.want)
			}
		})
	}
}

// TestQueryNarrowsAShownList is the same filtering on a picker that was opened
// rather than typed into: the model list is narrowed by what follows no prefix
// at all.
func TestQueryNarrowsAShownList(t *testing.T) {
	p := shown("").SetQuery("two")

	if got := names(p.Matches()); strings.Join(got, ",") != "model-two" {
		t.Errorf("matches = %v, want only model-two", got)
	}
}

// TestTypingResetsTheHighlight keeps the selection meaningful: the match set
// just changed, so the old index may point at another entry.
func TestTypingResetsTheHighlight(t *testing.T) {
	p := opened("/")
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	p = p.SetQuery("/q")

	if p.index != 0 {
		t.Errorf("index = %d, want it reset to 0 when the matches change", p.index)
	}
}

func TestArrowsMoveTheHighlight(t *testing.T) {
	p := opened("/")

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if p.index != 1 {
		t.Errorf("index = %d after down, want 1", p.index)
	}

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if p.index != 0 {
		t.Errorf("index = %d after up, want 0", p.index)
	}
}

func TestArrowsClampAtTheEnds(t *testing.T) {
	p := opened("/")

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if p.index != 0 {
		t.Errorf("index = %d, want it clamped at the first row", p.index)
	}

	for range len(commands) + 2 {
		p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if want := len(commands) - 1; p.index != want {
		t.Errorf("index = %d, want it clamped at the last row (%d)", p.index, want)
	}
}

// TestArrowsPreviewTheHighlight is what makes a list pickable with the arrow
// keys alone: the input shows what the highlight is on, so Enter has something
// to run.
func TestArrowsPreviewTheHighlight(t *testing.T) {
	p := opened("/")

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if want := "/model"; preview(t, cmd) != want {
		t.Errorf("previewed %q, want %q", preview(t, cmd), want)
	}
}

// TestArrowsDoNotNarrowTheMatches guards the invariant the preview rests on:
// the query stays what the user typed. Filtering by the name under the
// highlight would leave one row and nowhere to move.
func TestArrowsDoNotNarrowTheMatches(t *testing.T) {
	p := opened("/")

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if got := len(p.Matches()); got != len(commands) {
		t.Errorf("%d matches after arrowing, want all %d", got, len(commands))
	}
}

func TestTabPreviewsWithoutMoving(t *testing.T) {
	p := opened("/qu")

	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if want := "/quit"; preview(t, cmd) != want {
		t.Errorf("previewed %q, want %q", preview(t, cmd), want)
	}
	if p.index != 0 {
		t.Errorf("index = %d, want Tab to leave the highlight where it is", p.index)
	}
}

func TestTabWithNoMatchesDoesNothing(t *testing.T) {
	p := opened("/zzz")

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if cmd != nil {
		t.Errorf("Tab with no matches produced %T, want nothing", cmd())
	}
}

func TestEscClosesThePickerAndReportsIt(t *testing.T) {
	p := shown("")

	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if p.Open() {
		t.Error("picker still open after Esc")
	}
	if cmd == nil {
		t.Fatal("Esc produced no command, want it to report the close")
	}
	if _, ok := cmd().(ClosedMsg); !ok {
		t.Errorf("Esc produced %T, want ClosedMsg", cmd())
	}
}

// TestEscClosesATriggeredPicker covers the harder half: a triggered picker is
// open because of what is typed, so closing it has to forget the query too.
func TestEscClosesATriggeredPicker(t *testing.T) {
	p := opened("/q")

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if p.Open() {
		t.Error("triggered picker still open after Esc")
	}
}

// TestClosedPickerIgnoresKeys matters because the parent forwards arrows and
// Tab to the input when no list is open: handling them here too would move a
// highlight nobody can see.
func TestClosedPickerIgnoresKeys(t *testing.T) {
	p := opened("hello")

	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if cmd != nil {
		t.Errorf("a closed picker produced %T, want nothing", cmd())
	}
	if p.index != 0 {
		t.Error("a closed picker moved its highlight")
	}
}

func TestNothingIsHighlightedWithoutAMatch(t *testing.T) {
	if _, ok := opened("/zzz").Highlighted(); ok {
		t.Error("an empty match set has a highlight, want none to choose")
	}
	if _, ok := opened("hello").Highlighted(); ok {
		t.Error("a closed picker has a highlight, want none to choose")
	}
}

func TestClosedPickerRendersNothing(t *testing.T) {
	if view := closedList(models...).View(); view != "" {
		t.Errorf("a closed picker rendered %q, want nothing", view)
	}
}

func TestViewShowsOnlyTheMatches(t *testing.T) {
	view := opened("/qu").View()

	if !strings.Contains(view, "/quit") {
		t.Errorf("view does not show the matching entry:\n%s", view)
	}
	if strings.Contains(view, "/config") {
		t.Errorf("view still shows an entry the query rules out:\n%s", view)
	}
}

func TestViewFitsItsWidth(t *testing.T) {
	const width = 30

	p := New(Config[entry]{Render: render, Trigger: "/"}, commands...)
	p.SetWidth(width)

	for i, line := range strings.Split(p.SetQuery("/").View(), "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("row %d is %d columns wide, want <= %d:\n%s", i, got, width, line)
		}
	}
}

// TestViewCapsItsHeight keeps a long list from pushing the transcript off the
// screen: the API offers far more models than the picker has room for.
func TestViewCapsItsHeight(t *testing.T) {
	var many []entry
	for i := range menu.MaxRows * 2 {
		many = append(many, entry{fmt.Sprintf("model-%d", i), "Model"})
	}
	p := closedList().Show(many, nil)

	if got := len(strings.Split(p.View(), "\n")); got > menu.MaxRows {
		t.Errorf("picker is %d rows tall, want at most %d", got, menu.MaxRows)
	}
}

func TestViewReportsAnEmptyMatchSet(t *testing.T) {
	if view := opened("/zzz").View(); !strings.Contains(view, "no matching command") {
		t.Errorf("view does not say nothing matched:\n%s", view)
	}
	if view := closedList().Show(nil, nil).View(); !strings.Contains(view, "no models") {
		t.Errorf("view does not report an empty list:\n%s", view)
	}
}

// TestViewAlignsWhenConfigured covers the second column starting at one place
// for ids that range from "claude-opus-5" to "claude-sonnet-4-5-20250929".
func TestViewAlignsWhenConfigured(t *testing.T) {
	p := closedList().Show([]entry{{"short", "first"}, {"a-much-longer-one", "second"}}, nil)

	view := p.View()
	if !strings.Contains(view, "short"+strings.Repeat(" ", len("a-much-longer-one")-len("short"))) {
		t.Errorf("view does not pad the shorter name to the widest:\n%s", view)
	}
}

// names reduces a match set to the names shown, so tests can compare them
// without restating descriptions that are free to change.
func names(matches []entry) []string {
	got := make([]string, len(matches))
	for i, match := range matches {
		got[i] = match.name
	}
	return got
}

// preview runs cmd and returns the text it asks the input to show
func preview(t *testing.T, cmd tea.Cmd) string {
	t.Helper()

	if cmd == nil {
		t.Fatal("no command, want a preview of the highlighted entry")
	}
	msg, ok := cmd().(PreviewMsg)
	if !ok {
		t.Fatalf("command produced %T, want PreviewMsg", cmd())
	}
	return msg.Text
}
