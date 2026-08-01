package main

import (
	"strings"

	"charm.land/lipgloss/v2"
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
	{"quit", "exit elencode"},
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

// renderMenu draws one row per match, highlighting the one at index. Rows are
// truncated rather than wrapped: a command and its description are one line
// each, so the menu's height stays predictable as the user types.
func renderMenu(matches []command, index, width int) string {
	if len(matches) == 0 {
		return menuRow(menuMarker, lipgloss.NewStyle().Foreground(menuDescriptionColor).Render("no matching command"), width)
	}

	nameStyle := lipgloss.NewStyle().Foreground(menuNameColor)
	descriptionStyle := lipgloss.NewStyle().Foreground(menuDescriptionColor)

	rows := make([]string, len(matches))
	for i, c := range matches {
		marker := menuMarker
		if i == index {
			marker = menuMarkerSelected
		}
		name := nameStyle.Render(commandPrefix + c.name)
		rows[i] = menuRow(marker, name+"  "+descriptionStyle.Render(c.description), width)
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
	name, ok := strings.CutPrefix(input, commandPrefix)
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
