package main

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/config"
)

// names reduces a match set to command names, so tests can compare them without
// restating descriptions that are free to change.
func names(matches []command) []string {
	got := make([]string, len(matches))
	for i, c := range matches {
		got[i] = c.name
	}
	return got
}

func TestMatchCommands(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"slash alone lists everything", "/", []string{"config", "quit"}},
		{"exact name", "/quit", []string{"quit"}},
		{"prefix", "/qu", []string{"quit"}},
		{"subsequence", "/qt", []string{"quit"}},
		{"case insensitive", "/QUIT", []string{"quit"}},
		{"narrows to one command", "/co", []string{"config"}},
		{"a shared letter matches both", "/i", []string{"config", "quit"}},
		{"no match", "/zzz", nil},
		{"out of order is not a subsequence", "/tq", nil},
		{"missing slash", "quit", nil},
		{"empty", "", nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := names(matchCommands(test.input))
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("matchCommands(%q) = %v, want %v", test.input, got, test.want)
			}
		})
	}
}

// menuFixture is a stand-in registry, so the menu tests keep working as
// commands are added or renamed.
var menuFixture = []command{
	{"alpha", "the first one"},
	{"beta", "the second one"},
	{"gamma", "the third one"},
}

func TestRenderMenuShowsEveryMatch(t *testing.T) {
	view := renderMenu(menuFixture, 0, 80)

	for _, c := range menuFixture {
		if !strings.Contains(view, commandPrefix+c.name) {
			t.Errorf("menu is missing %q:\n%s", c.name, view)
		}
		if !strings.Contains(view, c.description) {
			t.Errorf("menu is missing the description of %q:\n%s", c.name, view)
		}
	}
}

func TestRenderMenuMarksTheHighlightedRow(t *testing.T) {
	const highlighted = 1

	lines := strings.Split(renderMenu(menuFixture, highlighted, 80), "\n")
	if len(lines) != len(menuFixture) {
		t.Fatalf("menu has %d rows, want %d:\n%s", len(lines), len(menuFixture), strings.Join(lines, "\n"))
	}

	for i, line := range lines {
		marked := strings.Contains(line, menuMarkerSelected)
		if want := i == highlighted; marked != want {
			t.Errorf("row %d marked = %v, want %v:\n%s", i, marked, want, line)
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

// TestRenderMenuColorsOnlyTheHighlightedRow checks the styles are actually
// wired into the output, not merely defined.
func TestRenderMenuColorsOnlyTheHighlightedRow(t *testing.T) {
	const highlighted = 1

	lines := strings.Split(renderMenu(menuFixture, highlighted, 80), "\n")

	for i, line := range lines {
		colored := strings.Contains(line, stylePrefix(menuStyles(true).name))
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

func TestRenderMenuReportsAnEmptyMatchSet(t *testing.T) {
	// Silence would be ambiguous: the user cannot tell a live menu with no
	// matches from a menu that never opened.
	if view := renderMenu(nil, 0, 80); !strings.Contains(view, "no matching command") {
		t.Errorf("menu does not report an empty match set:\n%s", view)
	}
}

func TestRenderMenuFitsNarrowTerminal(t *testing.T) {
	const width = 30

	view := renderMenu(menuFixture, 0, width)

	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("row %d is %d columns wide, want <= %d:\n%s", i, got, width, line)
		}
	}
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
			if got := moveHighlight(test.index, test.delta, test.len); got != test.want {
				t.Errorf("moveHighlight(%d, %d, %d) = %d, want %d", test.index, test.delta, test.len, got, test.want)
			}
		})
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
