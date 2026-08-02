package main

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/commands"
	"github.com/rstarc/elencode/internal/config"
)

// menuFixture is a stand-in registry, so the menu tests keep working as
// commands are added or renamed.
var menuFixture = []commands.Command{
	{Name: "alpha", Description: "the first one"},
	{Name: "beta", Description: "the second one"},
	{Name: "gamma", Description: "the third one"},
}

func TestRenderMenuShowsEveryMatch(t *testing.T) {
	view := renderMenu(menuFixture, 0, 80)

	for _, c := range menuFixture {
		if !strings.Contains(view, commands.Prefix+c.Name) {
			t.Errorf("menu is missing %q:\n%s", c.Name, view)
		}
		if !strings.Contains(view, c.Description) {
			t.Errorf("menu is missing the description of %q:\n%s", c.Name, view)
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

// modelFixture stands in for what the API returns, so these tests do not depend
// on which models exist today.
var modelFixture = []agent.Model{
	{ID: "model-one", DisplayName: "Model One"},
	{ID: "model-two", DisplayName: "Model Two"},
	{ID: "model-three", DisplayName: "Model Three"},
}

func TestRenderModelMenuShowsEveryModel(t *testing.T) {
	view := renderModelMenu(modelFixture, 0, 80)

	for _, m := range modelFixture {
		if !strings.Contains(view, m.ID) {
			t.Errorf("menu is missing %q:\n%s", m.ID, view)
		}
		// The id is what the user types after /model; the display name is what
		// they recognise. Both are shown.
		if !strings.Contains(view, m.DisplayName) {
			t.Errorf("menu is missing the display name of %q:\n%s", m.ID, view)
		}
	}
}

func TestRenderModelMenuMarksTheHighlightedRow(t *testing.T) {
	const highlighted = 1

	for i, line := range strings.Split(renderModelMenu(modelFixture, highlighted, 80), "\n") {
		marked := strings.Contains(line, menuMarkerSelected)
		if want := i == highlighted; marked != want {
			t.Errorf("row %d marked = %v, want %v:\n%s", i, marked, want, line)
		}
	}
}

// TestRenderModelMenuCapsItsHeight keeps a long list from pushing the transcript
// off the screen: the API offers far more models than the menu has room for.
func TestRenderModelMenuCapsItsHeight(t *testing.T) {
	var many []agent.Model
	for i := range maxMenuRows * 2 {
		many = append(many, agent.Model{ID: fmt.Sprintf("model-%d", i), DisplayName: "Model"})
	}

	view := renderModelMenu(many, 0, 80)

	if got := len(strings.Split(view, "\n")); got > maxMenuRows {
		t.Errorf("menu is %d rows tall, want at most %d", got, maxMenuRows)
	}
}

// TestRenderModelMenuScrollsToTheHighlight covers arrowing past the bottom of
// the window: a selection that is not drawn cannot be seen.
func TestRenderModelMenuScrollsToTheHighlight(t *testing.T) {
	var many []agent.Model
	for i := range maxMenuRows * 2 {
		many = append(many, agent.Model{ID: fmt.Sprintf("model-%d", i), DisplayName: "Model"})
	}
	last := len(many) - 1

	view := renderModelMenu(many, last, 80)

	if !strings.Contains(view, many[last].ID) {
		t.Errorf("menu does not show the highlighted model %q:\n%s", many[last].ID, view)
	}
}

func TestRenderConfigShowsTheModel(t *testing.T) {
	cfg := config.Config{Model: "claude-opus-4-5", Path: "/tmp/c.json"}

	if view := renderConfig(cfg, 80); !strings.Contains(view, cfg.Model) {
		t.Errorf("config view does not show the model:\n%s", view)
	}
}

// TestAlignNamesPadsToTheWidest keeps the second column straight: model ids
// range from "claude-opus-5" to "claude-sonnet-4-5-20250929", so unpadded rows
// leave the display names scattered across the menu.
func TestAlignNamesPadsToTheWidest(t *testing.T) {
	got := alignNames([]menuItem{{"a", "A"}, {"bbb", "B"}, {"cc", "C"}})

	for i, item := range got {
		if lipgloss.Width(item.name) != 3 {
			t.Errorf("row %d name = %q, want it padded to the widest (3)", i, item.name)
		}
	}
}

func TestRenderConfigShowsWhetherThinkingIsOn(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		want    string
	}{
		{"on", true, "true"},
		{"off", false, "false"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{ThinkingEnabled: test.enabled, Path: "/tmp/c.json"}

			view := renderConfig(cfg, 80)

			line := rowFor(view, "thinking_enabled")
			if line == "" {
				t.Fatalf("config view has no thinking_enabled row:\n%s", view)
			}
			if !strings.Contains(line, test.want) {
				t.Errorf("thinking row = %q, want it to say %q", line, test.want)
			}
		})
	}
}

// rowFor returns the config view's row for a setting, or "" if it has none
func rowFor(view, name string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	return ""
}
