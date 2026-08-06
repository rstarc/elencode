// Package menu draws the list-with-a-highlight the TUI shows in several
// places: the slash-command menu, the model picker, the configuration view.
// It is rendering only and holds no state, so what a menu looks like is decided
// here and what it does is decided by whoever owns it.
package menu

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// MarkerSelected marks the highlighted row; Marker continues the left border
// down the rest, matching how transcript blocks are drawn.
const (
	MarkerSelected = "›"
	Marker         = "│"
)

var (
	MarkerColor = lipgloss.BrightBlue
	NameColor   = lipgloss.BrightBlue
)

// DescriptionColor is the dim gray used for text that is there to be glanced
// at rather than read: descriptions, key hints.
var DescriptionColor = lipgloss.BrightBlack

// The highlighted row is the only colored one. Coloring every row made the
// selection hard to pick out, since the highlight then differed by its marker
// alone; leaving the rest in the terminal's default text color lets the
// selected row stand out on both columns.
var (
	selectedNameColor        = lipgloss.BrightCyan
	selectedDescriptionColor = lipgloss.White
)

// MaxRows caps how tall a menu may grow, leaving the transcript some room
const MaxRows = 10

// Item is one row of a menu: what to type, and what it means
type Item struct{ Name, Description string }

// RowStyles is how one row is painted, given whether it is selected
type RowStyles struct{ Name, Description lipgloss.Style }

func Styles(selected bool) RowStyles {
	if selected {
		return RowStyles{
			Name:        lipgloss.NewStyle().Foreground(selectedNameColor).Bold(true),
			Description: lipgloss.NewStyle().Foreground(selectedDescriptionColor),
		}
	}
	return RowStyles{
		// No foreground: the terminal's own text color
		Name:        lipgloss.NewStyle(),
		Description: lipgloss.NewStyle().Foreground(DescriptionColor),
	}
}

// Render draws one row per item, highlighting the one at index, or emptyMessage
// when there are none. Rows are truncated rather than wrapped, and the list is
// windowed to MaxRows, so a menu's height stays predictable however long the
// list behind it is.
func Render(items []Item, index, width int, emptyMessage string) string {
	if len(items) == 0 {
		return Row(Marker, lipgloss.NewStyle().Foreground(DescriptionColor).Render(emptyMessage), width)
	}

	start, end := Window(len(items), index)
	items, index = items[start:end], index-start

	rows := make([]string, len(items))
	for i, item := range items {
		selected := i == index
		marker := Marker
		if selected {
			marker = MarkerSelected
		}
		styles := Styles(selected)
		rows[i] = Row(marker, styles.Name.Render(item.Name)+"  "+styles.Description.Render(item.Description), width)
	}
	return strings.Join(rows, "\n")
}

// Row prefixes content with a colored marker column and clips it to width
func Row(marker, content string, width int) string {
	prefix := lipgloss.NewStyle().Foreground(MarkerColor).Render(marker) + " "
	inner := max(width-lipgloss.Width(marker)-1, 1)
	return prefix + lipgloss.NewStyle().MaxWidth(inner).Render(content)
}

// Window returns the half-open range of rows to draw for a list of count items
// with index highlighted. The highlight is kept centred rather than scrolled
// minimally, so the window depends only on the current index and no scroll
// position has to be carried around.
func Window(count, index int) (start, end int) {
	if count <= MaxRows {
		return 0, count
	}
	start = min(max(index-MaxRows/2, 0), count-MaxRows)
	return start, start + MaxRows
}

// Align pads every name to the widest, so the second column starts at one place
// instead of following ids that range from "claude-opus-5" to
// "claude-sonnet-4-5-20250929".
func Align(items []Item) []Item {
	widest := 0
	for _, item := range items {
		widest = max(widest, lipgloss.Width(item.Name))
	}

	aligned := make([]Item, len(items))
	for i, item := range items {
		aligned[i] = Item{lipgloss.NewStyle().Width(widest).Render(item.Name), item.Description}
	}
	return aligned
}

// MoveHighlight shifts index by delta within a list of length count, clamping
// at both ends rather than wrapping.
func MoveHighlight(index, delta, count int) int {
	return min(max(index+delta, 0), max(count-1, 0))
}
