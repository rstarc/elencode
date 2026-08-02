package main

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
)

// commandPrefix opens the command menu when the input starts with it
const commandPrefix = "/"

// command is a slash command the user can run from the input field
type command struct {
	name        string // without the leading slash
	description string
}

var commands = []command{
	{"config", "show the current configuration"},
	{"model", "choose the model, optionally by id"},
	{"quit", "exit elencode"},
}

// splitCommand separates a command line into the command and its argument, so
// "/model some-id" runs /model with some-id rather than being looked up whole.
func splitCommand(input string) (name, arg string) {
	name, arg, _ = strings.Cut(strings.TrimSpace(input), " ")
	return name, strings.TrimSpace(arg)
}

// renderConfig draws the read-only configuration view. The API key is printed
// through Secret.String, so the value cannot reach the screen.
func renderConfig(cfg config.Config, width int) string {
	source := "from config file"
	if cfg.APIKeyFromEnv {
		source = "from " + config.ANTHROPIC_API_KEY_ENV_VAR_NAME
	}

	title := lipgloss.NewStyle().Foreground(menuNameColor).Render("configuration")
	rows := []string{
		menuRow(menuMarker, title, width),
		menuRow(menuMarker, "", width),
		configRow("anthropic_api_key", cfg.AnthropicAPIKey.String()+"  ("+source+")", width),
		configRow("model", cfg.Model, width),
		configRow("thinking_enabled", strconv.FormatBool(cfg.ThinkingEnabled), width),
		configRow("config file", cfg.Path, width),
		menuRow(menuMarker, "", width),
		menuRow(menuMarker, lipgloss.NewStyle().Foreground(menuDescriptionColor).Render("esc to close"), width),
	}
	return strings.Join(rows, "\n")
}

// configRow renders one name/value pair, with the name padded so the values line up
func configRow(name, value string, width int) string {
	label := lipgloss.NewStyle().Foreground(menuDescriptionColor).Width(configLabelWidth).Render(name)
	return menuRow(menuMarker, label+value, width)
}

// configLabelWidth is the column the values start in, wide enough for the
// longest label the view currently has.
const configLabelWidth = 20

// menuMarkerSelected marks the highlighted row; menuMarker continues the left
// border down the rest, matching how transcript blocks are drawn.
const (
	menuMarkerSelected = "›"
	menuMarker         = "│"
)

var menuMarkerColor = lipgloss.BrightBlue
var menuNameColor = lipgloss.BrightBlue
var menuDescriptionColor = lipgloss.BrightBlack

// The highlighted row is the only colored one. Coloring every command made the
// selection hard to pick out, since the highlight then differed by its marker
// alone; leaving the rest in the terminal's default text color lets the
// selected row stand out on both columns.
var menuSelectedNameColor = lipgloss.BrightCyan
var menuSelectedDescriptionColor = lipgloss.White

// menuRowStyles is how one menu row is painted, given whether it is selected
type menuRowStyles struct{ name, description lipgloss.Style }

func menuStyles(selected bool) menuRowStyles {
	if selected {
		return menuRowStyles{
			name:        lipgloss.NewStyle().Foreground(menuSelectedNameColor).Bold(true),
			description: lipgloss.NewStyle().Foreground(menuSelectedDescriptionColor),
		}
	}
	return menuRowStyles{
		// No foreground: the terminal's own text color
		name:        lipgloss.NewStyle(),
		description: lipgloss.NewStyle().Foreground(menuDescriptionColor),
	}
}

// menuItem is one row of a menu: what to type, and what it means
type menuItem struct{ name, description string }

// renderMenu draws one row per match, highlighting the one at index. Rows are
// truncated rather than wrapped: a command and its description are one line
// each, so the menu's height stays predictable as the user types.
func renderMenu(matches []command, index, width int) string {
	items := make([]menuItem, len(matches))
	for i, c := range matches {
		items[i] = menuItem{commandPrefix + c.name, c.description}
	}
	return renderItems(items, index, width, "no matching command")
}

// renderModelMenu draws the model picker. The id is shown first because it is
// what the user types after /model; the display name is only there to recognise
// it by.
func renderModelMenu(models []agent.Model, index, width int) string {
	items := make([]menuItem, len(models))
	for i, m := range models {
		items[i] = menuItem{m.ID, m.DisplayName}
	}
	// Unlike the command list, this one comes from the API and is far longer
	// than the menu has room for, so only a window around the highlight is drawn.
	start, end := menuWindow(len(items), index)
	return renderItems(alignNames(items[start:end]), index-start, width, "the API offered no models")
}

// alignNames pads every name to the widest, so the second column starts at one
// place instead of following ids that range from "claude-opus-5" to
// "claude-sonnet-4-5-20250929".
func alignNames(items []menuItem) []menuItem {
	widest := 0
	for _, item := range items {
		widest = max(widest, lipgloss.Width(item.name))
	}

	aligned := make([]menuItem, len(items))
	for i, item := range items {
		aligned[i] = menuItem{lipgloss.NewStyle().Width(widest).Render(item.name), item.description}
	}
	return aligned
}

// maxMenuRows caps how tall a menu may grow, leaving the transcript some room
const maxMenuRows = 10

// menuWindow returns the half-open range of rows to draw for a list of count
// items with index highlighted. The highlight is kept centred rather than
// scrolled minimally, so the window depends only on the current index and no
// scroll position has to be carried around.
func menuWindow(count, index int) (start, end int) {
	if count <= maxMenuRows {
		return 0, count
	}
	start = min(max(index-maxMenuRows/2, 0), count-maxMenuRows)
	return start, start + maxMenuRows
}

// renderItems draws the rows of a menu, or emptyMessage when there are none
func renderItems(items []menuItem, index, width int, emptyMessage string) string {
	if len(items) == 0 {
		return menuRow(menuMarker, lipgloss.NewStyle().Foreground(menuDescriptionColor).Render(emptyMessage), width)
	}

	rows := make([]string, len(items))
	for i, item := range items {
		selected := i == index
		marker := menuMarker
		if selected {
			marker = menuMarkerSelected
		}
		styles := menuStyles(selected)
		rows[i] = menuRow(marker, styles.name.Render(item.name)+"  "+styles.description.Render(item.description), width)
	}
	return strings.Join(rows, "\n")
}

// menuRow prefixes content with a colored marker column and clips it to width
func menuRow(marker, content string, width int) string {
	prefix := lipgloss.NewStyle().Foreground(menuMarkerColor).Render(marker) + " "
	inner := max(width-lipgloss.Width(marker)-1, 1)
	return prefix + lipgloss.NewStyle().MaxWidth(inner).Render(content)
}

// moveHighlight shifts index by delta within a match set of length count,
// clamping at both ends rather than wrapping.
func moveHighlight(index, delta, count int) int {
	return min(max(index+delta, 0), max(count-1, 0))
}

// matchCommands returns the commands whose names fuzzy-match input, which must
// start with a slash. Registry order is kept; there is no scoring.
func matchCommands(input string) []command {
	query, ok := strings.CutPrefix(input, commandPrefix)
	if !ok {
		return nil
	}

	var matches []command
	for _, c := range commands {
		if isSubsequence(strings.ToLower(query), c.name) {
			matches = append(matches, c)
		}
	}
	return matches
}

// isSubsequence reports whether every rune of query appears in name, in order
// but not necessarily adjacent, so "/qt" finds "quit".
func isSubsequence(query, name string) bool {
	rest := name
	for _, r := range query {
		i := strings.IndexRune(rest, r)
		if i < 0 {
			return false
		}
		rest = rest[i+len(string(r)):]
	}
	return true
}

// lookupCommand finds the command input names exactly. Enter uses this rather
// than matchCommands, so a typo cannot run a command the user did not spell out.
func lookupCommand(input string) (command, bool) {
	head, _ := splitCommand(input)
	name, ok := strings.CutPrefix(head, commandPrefix)
	if !ok {
		return command{}, false
	}
	for _, c := range commands {
		if strings.EqualFold(name, c.name) {
			return c, true
		}
	}
	return command{}, false
}
