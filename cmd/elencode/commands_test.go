package main

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/config"
)

// visibleNames reduces the menu's surviving rows to command names
func visibleNames(menu list.Model) []string {
	got := make([]string, 0, len(menu.VisibleItems()))
	for _, item := range menu.VisibleItems() {
		if c, ok := item.(command); ok {
			got = append(got, c.name)
		}
	}
	return got
}

func TestMenuFiltersOnTheTypedQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty query lists everything", "", []string{"config", "quit"}},
		{"exact name", "quit", []string{"quit"}},
		{"prefix", "qu", []string{"quit"}},
		{"narrows to one command", "co", []string{"config"}},
		{"no match", "zzz", nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			menu := newCommandMenu()
			menu.SetFilterText(test.query)

			if got := visibleNames(menu); strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("query %q leaves %v, want %v", test.query, got, test.want)
			}
		})
	}
}

func TestMenuDrawsOneRowPerCommand(t *testing.T) {
	// list's own delegate reserves a blank line between items and a second line
	// per item for a description, which would make the menu tower over the input
	menu := newCommandMenu()
	menu.SetWidth(80)

	lines := strings.Split(strings.TrimRight(menu.View(), "\n"), "\n")

	if len(lines) != len(commands) {
		t.Errorf("menu is %d rows for %d commands, want one row each:\n%q", len(lines), len(commands), menu.View())
	}
}

func TestMenuShowsNamesAndDescriptions(t *testing.T) {
	menu := newCommandMenu()
	menu.SetWidth(80)

	view := menu.View()
	for _, c := range commands {
		if !strings.Contains(view, commandPrefix+c.name) {
			t.Errorf("menu is missing %q:\n%s", c.name, view)
		}
		if !strings.Contains(view, c.description) {
			t.Errorf("menu is missing the description of %q:\n%s", c.name, view)
		}
	}
}

func TestMenuMarksTheSelectedRow(t *testing.T) {
	menu := newCommandMenu()
	menu.SetWidth(80)
	menu.Select(1)

	lines := strings.Split(strings.TrimRight(menu.View(), "\n"), "\n")
	for i, line := range lines {
		marked := strings.Contains(line, menuMarkerSelected)
		if want := i == 1; marked != want {
			t.Errorf("row %d marked = %v, want %v:\n%q", i, marked, want, line)
		}
	}
}

func TestMenuColorsOnlyTheSelectedRow(t *testing.T) {
	menu := newCommandMenu()
	menu.SetWidth(80)
	menu.Select(1)

	lines := strings.Split(strings.TrimRight(menu.View(), "\n"), "\n")
	for i, line := range lines {
		colored := strings.Contains(line, stylePrefix(menuStyles(true).name))
		if want := i == 1; colored != want {
			t.Errorf("row %d carries the highlight color = %v, want %v:\n%q", i, colored, want, line)
		}
	}
}

func TestMenuReportsAnEmptyMatchSet(t *testing.T) {
	// Silence would be ambiguous: the user cannot tell a live menu with no
	// matches from a menu that never opened. list phrases this line as
	// "No <plural>.", so the wording comes from SetStatusBarItemName.
	menu := newCommandMenu()
	menu.SetWidth(80)
	menu.SetFilterText("zzz")

	view := menu.View()
	if !strings.Contains(view, "matching command") {
		t.Errorf("menu does not report an empty match set:\n%s", view)
	}
	if strings.Contains(view, "No items") {
		t.Errorf("menu shows list's default empty text:\n%s", view)
	}
}

func TestMenuFitsNarrowTerminal(t *testing.T) {
	const width = 30

	menu := newCommandMenu()
	menu.SetWidth(width)

	for i, line := range strings.Split(strings.TrimRight(menu.View(), "\n"), "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("row %d is %d columns wide, want <= %d:\n%s", i, got, width, line)
		}
	}
}

func TestUnhighlightedCommandUsesTheDefaultTextColor(t *testing.T) {
	// A colored name on every row makes the highlight hard to pick out, so only
	// the selected row's name is colored.
	want := lipgloss.NewStyle().GetForeground()

	if got := menuStyles(false).name.GetForeground(); got != want {
		t.Errorf("unhighlighted command foreground = %v, want the default %v", got, want)
	}
}

func TestHighlightedRowDiffersInBothColumns(t *testing.T) {
	selected, plain := menuStyles(true), menuStyles(false)

	if selected.name.GetForeground() == plain.name.GetForeground() {
		t.Error("the highlighted command has the same color as an unhighlighted one")
	}
	if selected.description.GetForeground() == plain.description.GetForeground() {
		t.Error("the highlighted description has the same color as an unhighlighted one")
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

func TestRenderConfigMasksTheAPIKey(t *testing.T) {
	const key = "sk-ant-do-not-print-me"
	cfg := config.Config{AnthropicAPIKey: config.Secret(key), Path: "/tmp/elencode/config.json"}

	view := renderConfig(cfg, 80)

	if strings.Contains(view, key) {
		t.Error("config view contains the raw API key")
	}
	if !strings.Contains(view, cfg.AnthropicAPIKey.String()) {
		t.Errorf("config view does not show the mask:\n%s", view)
	}
}

func TestRenderConfigShowsThePath(t *testing.T) {
	const path = "/home/someone/.config/elencode/config.json"

	view := renderConfig(config.Config{Path: path}, 120)

	if !strings.Contains(view, path) {
		t.Errorf("config view does not show the config file path:\n%s", view)
	}
}

func TestRenderConfigNamesTheSource(t *testing.T) {
	tests := []struct {
		name      string
		fromEnv   bool
		wantMatch string
	}{
		{"environment", true, config.ANTHROPIC_API_KEY_ENV_VAR_NAME},
		{"config file", false, "config file"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{AnthropicAPIKey: "x", APIKeyFromEnv: test.fromEnv, Path: "/tmp/c.json"}

			if view := renderConfig(cfg, 80); !strings.Contains(view, test.wantMatch) {
				t.Errorf("config view does not name %q as the source:\n%s", test.wantMatch, view)
			}
		})
	}
}

func TestRenderConfigSaysHowToClose(t *testing.T) {
	if view := renderConfig(config.Config{}, 80); !strings.Contains(view, "esc") {
		t.Errorf("config view does not say how to close it:\n%s", view)
	}
}

func TestLookupCommandRequiresExactName(t *testing.T) {
	if _, ok := lookupCommand("/quit"); !ok {
		t.Error("lookupCommand(\"/quit\") found nothing, want the quit command")
	}
	// A fuzzy match must not run on Enter, or a typo silently quits the program.
	if _, ok := lookupCommand("/qt"); ok {
		t.Error("lookupCommand(\"/qt\") matched, want no match for a non-exact name")
	}
}
