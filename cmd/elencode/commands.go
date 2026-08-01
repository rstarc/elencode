package main

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
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

// menuRow prefixes content with a colored marker column and clips it to width
func menuRow(marker, content string, width int) string {
	prefix := lipgloss.NewStyle().Foreground(menuMarkerColor).Render(marker) + " "
	inner := max(width-lipgloss.Width(marker)-1, 1)
	return prefix + lipgloss.NewStyle().MaxWidth(inner).Render(content)
}

// commandDelegate draws one command per row. list's own delegate reserves a
// blank line between items and a second line for a description, which would
// make a two entry menu six rows tall above the input.
type commandDelegate struct{}

func (commandDelegate) Height() int  { return 1 }
func (commandDelegate) Spacing() int { return 0 }

func (commandDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (commandDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	cmd, ok := item.(command)
	if !ok {
		return
	}

	selected := index == m.Index()
	marker := menuMarker
	if selected {
		marker = menuMarkerSelected
	}
	styles := menuStyles(selected)
	name := styles.name.Render(commandPrefix + cmd.name)
	fmt.Fprint(w, menuRow(marker, name+"  "+styles.description.Render(cmd.description), m.Width()))
}

// FilterValue is what list matches the typed query against, making command a
// list.Item. Only the name is offered: matching descriptions would make "show"
// select /config, which reads as a bug from the prompt.
func (c command) FilterValue() string { return c.name }

// newCommandMenu builds the menu list with every piece of list's chrome turned
// off. What is left is the rows themselves, since the menu sits directly above
// the input rather than owning the screen.
func newCommandMenu() list.Model {
	items := make([]list.Item, len(commands))
	for i, c := range commands {
		items[i] = c
	}

	menu := list.New(items, commandDelegate{}, 0, len(items))
	menu.SetShowTitle(false)
	menu.SetShowStatusBar(false)
	menu.SetShowPagination(false)
	menu.SetShowHelp(false)
	// Filtering stays enabled, but its input is ours: the user types in the
	// prompt, and SetFilterText feeds that query in. Showing list's own filter
	// field would put a second, competing input on screen.
	menu.SetShowFilter(false)
	// list builds its empty-state line as "No <plural>.", so naming the item
	// here is the only way to say "no matching commands" rather than "No items."
	menu.SetStatusBarItemName("matching command", "matching commands")
	menu.Styles = compactListStyles()
	return menu
}

// compactListStyles drops the padding list puts around its content, which would
// otherwise indent the menu away from the transcript's left edge, and gives the
// empty-state line the same marker column as a real row.
func compactListStyles() list.Styles {
	styles := list.DefaultStyles(true)
	marker := lipgloss.NewStyle().Foreground(menuMarkerColor).Render(menuMarker)
	styles.NoItems = lipgloss.NewStyle().Foreground(menuDescriptionColor).SetString(marker)
	return styles
}

// menuQuery strips the leading slash to get the term the menu filters on
func menuQuery(input string) string {
	query, _ := strings.CutPrefix(input, commandPrefix)
	return query
}

// lookupCommand finds the command input names exactly. Enter uses this rather
// than the menu's selection, so a typo cannot run a command the user did not
// spell out.
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
