package menu

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var fixture = []Item{
	{"alpha", "the first one"},
	{"beta", "the second one"},
	{"gamma", "the third one"},
}

func TestRenderShowsEveryItem(t *testing.T) {
	view := Render(fixture, 0, 80, "nothing here")

	for _, item := range fixture {
		if !strings.Contains(view, item.Name) {
			t.Errorf("menu is missing %q:\n%s", item.Name, view)
		}
		if !strings.Contains(view, item.Description) {
			t.Errorf("menu is missing the description of %q:\n%s", item.Name, view)
		}
	}
}

func TestRenderMarksTheHighlightedRow(t *testing.T) {
	const highlighted = 1

	lines := strings.Split(Render(fixture, highlighted, 80, "nothing here"), "\n")
	if len(lines) != len(fixture) {
		t.Fatalf("menu has %d rows, want %d:\n%s", len(lines), len(fixture), strings.Join(lines, "\n"))
	}

	for i, line := range lines {
		marked := strings.Contains(line, MarkerSelected)
		if want := i == highlighted; marked != want {
			t.Errorf("row %d marked = %v, want %v:\n%s", i, marked, want, line)
		}
	}
}

func TestUnhighlightedRowUsesTheDefaultTextColor(t *testing.T) {
	// A colored name on every row makes the highlight hard to pick out, so only
	// the selected row's name is colored.
	want := lipgloss.NewStyle().GetForeground()

	if got := Styles(false).Name.GetForeground(); got != want {
		t.Errorf("unhighlighted name foreground = %v, want the default %v", got, want)
	}
}

func TestHighlightedRowDiffersInBothColumns(t *testing.T) {
	selected, plain := Styles(true), Styles(false)

	if selected.Name.GetForeground() == plain.Name.GetForeground() {
		t.Error("the highlighted name has the same color as an unhighlighted one")
	}
	if selected.Description.GetForeground() == plain.Description.GetForeground() {
		t.Error("the highlighted description has the same color as an unhighlighted one")
	}
}

// TestRenderColorsOnlyTheHighlightedRow checks the styles are actually wired
// into the output, not merely defined.
func TestRenderColorsOnlyTheHighlightedRow(t *testing.T) {
	const highlighted = 1

	lines := strings.Split(Render(fixture, highlighted, 80, "nothing here"), "\n")

	for i, line := range lines {
		colored := strings.Contains(line, stylePrefix(Styles(true).Name))
		if want := i == highlighted; colored != want {
			t.Errorf("row %d carries the highlight color = %v, want %v:\n%q", i, colored, want, line)
		}
	}
}

// stylePrefix returns the escape sequence a style emits before its content, so
// a test can look for that exact styling in a rendered row. Taken from the
// style itself rather than rebuilt from its foreground, since attributes like
// bold share the escape and would otherwise be missed.
func stylePrefix(s lipgloss.Style) string {
	prefix, _, _ := strings.Cut(s.Render("x"), "x")
	return prefix
}

func TestRenderReportsAnEmptyItemSet(t *testing.T) {
	// Silence would be ambiguous: the user cannot tell a live menu with no
	// matches from a menu that never opened.
	if view := Render(nil, 0, 80, "no matching command"); !strings.Contains(view, "no matching command") {
		t.Errorf("menu does not report an empty item set:\n%s", view)
	}
}

func TestRenderFitsNarrowTerminal(t *testing.T) {
	const width = 30

	view := Render(fixture, 0, width, "nothing here")

	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("row %d is %d columns wide, want <= %d:\n%s", i, got, width, line)
		}
	}
}

func TestMoveHighlight(t *testing.T) {
	tests := []struct {
		name              string
		index, delta, len int
		want              int
	}{
		{"down", 0, 1, 3, 1},
		{"up", 2, -1, 3, 1},
		{"clamps at the last row", 2, 1, 3, 2},
		{"clamps at the first row", 0, -1, 3, 0},
		{"empty match set", 0, 1, 0, 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := MoveHighlight(test.index, test.delta, test.len); got != test.want {
				t.Errorf("MoveHighlight(%d, %d, %d) = %d, want %d", test.index, test.delta, test.len, got, test.want)
			}
		})
	}
}

// TestAlignPadsToTheWidest keeps the second column straight: model ids range
// from "claude-opus-5" to "claude-sonnet-4-5-20250929", so unpadded rows leave
// the display names scattered across the menu.
func TestAlignPadsToTheWidest(t *testing.T) {
	got := Align([]Item{{"a", "A"}, {"bbb", "B"}, {"cc", "C"}})

	for i, item := range got {
		if lipgloss.Width(item.Name) != 3 {
			t.Errorf("row %d name = %q, want it padded to the widest (3)", i, item.Name)
		}
	}
}

// TestWindowCapsItsHeight keeps a long list from pushing the transcript off the
// screen: the API offers far more models than the menu has room for.
func TestWindowCapsItsHeight(t *testing.T) {
	start, end := Window(MaxRows*2, 0)

	if got := end - start; got > MaxRows {
		t.Errorf("window is %d rows tall, want at most %d", got, MaxRows)
	}
}

// TestWindowScrollsToTheHighlight covers arrowing past the bottom of the
// window: a selection that is not drawn cannot be seen.
func TestWindowScrollsToTheHighlight(t *testing.T) {
	count := MaxRows * 2
	last := count - 1

	start, end := Window(count, last)

	if last < start || last >= end {
		t.Errorf("window [%d,%d) does not contain the highlight at %d", start, end, last)
	}
}

func TestWindowShowsEverythingThatFits(t *testing.T) {
	start, end := Window(3, 0)

	if start != 0 || end != 3 {
		t.Errorf("window = [%d,%d), want all 3 rows", start, end)
	}
}

func TestRowClipsToWidth(t *testing.T) {
	const width = 20

	row := Row(Marker, strings.Repeat("x", 100), width)

	if got := lipgloss.Width(row); got > width {
		t.Errorf("row is %d columns wide, want <= %d:\n%s", got, width, row)
	}
}

// TestRenderWindowsALongList is the whole point of Window being applied inside
// Render: a caller passing more items than fit must still get a menu that fits.
func TestRenderWindowsALongList(t *testing.T) {
	var many []Item
	for i := range MaxRows * 2 {
		many = append(many, Item{fmt.Sprintf("item-%d", i), "a row"})
	}
	last := len(many) - 1

	view := Render(many, last, 80, "nothing here")

	if got := len(strings.Split(view, "\n")); got > MaxRows {
		t.Errorf("menu is %d rows tall, want at most %d", got, MaxRows)
	}
	if !strings.Contains(view, many[last].Name) {
		t.Errorf("menu does not show the highlighted item %q:\n%s", many[last].Name, view)
	}
}
