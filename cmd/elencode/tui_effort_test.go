package main

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/tui/menu"
)

var effortModel = agent.Model{Provider: agent.ProviderAnthropic, ID: "thinker", Thinking: agent.ThinkingEffort}

// newEffortModel is a sized session on effortModel with thinking on, which
// is when an effort level is sent at all.
func newEffortModel(t *testing.T, effort string) (model, *recordingProvider) {
	t.Helper()

	provider := &recordingProvider{}
	a := agent.New(nil)
	a.SetModel(effortModel, provider)
	cfg := config.Config{Model: effortModel.Qualified(), ThinkingEnabled: true, ThinkingEffort: effort}
	m := newModel(a, cfg, defaultCommands(), providerSet{agent.ProviderAnthropic: provider}, []agent.Model{effortModel})
	return update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20}), provider
}

// sentEffort runs a turn straight on the agent and returns the level it was
// sent with: what /effort is for, and what the TUI cannot show by itself.
func sentEffort(t *testing.T, m model, provider *recordingProvider) agent.Effort {
	t.Helper()

	for range m.agent.Run(context.Background(), "hi") {
	}
	if len(provider.requests) == 0 {
		t.Fatal("the turn sent no request")
	}
	return provider.requests[len(provider.requests)-1].Effort
}

func TestEffortCommandSetsTheLevel(t *testing.T) {
	m, provider := newEffortModel(t, "")

	m, _ = enter(t, typeText(t, m, "/effort xhigh"))

	if got := sentEffort(t, m, provider); got != agent.EffortXHigh {
		t.Errorf("effort sent = %q, want xhigh", got)
	}
}

func TestEffortCommandRejectsAnUnknownLevel(t *testing.T) {
	m, provider := newEffortModel(t, "low")

	m, cmd := enter(t, typeText(t, m, "/effort turbo"))

	if cmd == nil {
		t.Error("an unknown level reported nothing")
	}
	if got := sentEffort(t, m, provider); got != agent.EffortLow {
		t.Errorf("effort sent = %q, want low unchanged", got)
	}
}

func TestEffortCommandOpensTheSliderOnTheCurrentLevel(t *testing.T) {
	m, _ := newEffortModel(t, "medium")

	m, _ = enter(t, typeText(t, m, "/effort"))

	if !m.effortSlider.open {
		t.Fatal("/effort did not open the slider")
	}
	if got := m.effortSlider.level(); got != agent.EffortMedium {
		t.Errorf("slider starts on %q, want medium", got)
	}
}

// An unset level is the API's to pick, and the slider has to start somewhere:
// the middle is as far as it gets from either end.
func TestSliderStartsInTheMiddleWhenNoLevelIsSet(t *testing.T) {
	m, _ := newEffortModel(t, "")

	m, _ = enter(t, typeText(t, m, "/effort"))

	if got := m.effortSlider.level(); got != agent.EffortHigh {
		t.Errorf("slider starts on %q, want high", got)
	}
}

func TestSliderSetsTheLevelItIsMovedTo(t *testing.T) {
	m, provider := newEffortModel(t, "low")
	m, _ = enter(t, typeText(t, m, "/effort"))

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m, _ = enter(t, m)

	if m.effortSlider.open {
		t.Error("the slider stayed open after enter")
	}
	if got := sentEffort(t, m, provider); got != agent.EffortMedium {
		t.Errorf("effort sent = %q, want medium", got)
	}
}

func TestSliderStopsAtEitherEnd(t *testing.T) {
	m, _ := newEffortModel(t, "low")
	m, _ = enter(t, typeText(t, m, "/effort"))

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := m.effortSlider.level(); got != agent.EffortLow {
		t.Errorf("left of low = %q, want low", got)
	}
	for range len(agent.Efforts) + 1 {
		m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	}
	if got := m.effortSlider.level(); got != agent.EffortMax {
		t.Errorf("right of max = %q, want max", got)
	}
}

func TestEscLeavesTheLevelAlone(t *testing.T) {
	m, provider := newEffortModel(t, "low")
	m, _ = enter(t, typeText(t, m, "/effort"))

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.effortSlider.open {
		t.Error("esc left the slider open")
	}
	if got := sentEffort(t, m, provider); got != agent.EffortLow {
		t.Errorf("effort sent = %q, want low unchanged", got)
	}
}

// The slider has the keyboard, so typing does not reach the hidden input.
func TestSliderStandsInForTheInput(t *testing.T) {
	m, _ := newEffortModel(t, "low")
	m, _ = enter(t, typeText(t, m, "/effort"))

	m = typeText(t, m, "x")

	if m.input.Value() != "" {
		t.Errorf("input = %q, want typing held while the slider is open", m.input.Value())
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "<━━┃──────│") {
		t.Errorf("view does not show the slider:\n%s", view)
	}
}

func TestEffortIndicatorFillsUpToTheLevel(t *testing.T) {
	lit := lipgloss.NewStyle().Foreground(menu.NameColor)
	dim := lipgloss.NewStyle().Foreground(menu.DescriptionColor)

	tests := map[agent.Effort]string{
		agent.EffortNone:   dim.Render("[") + dim.Render("──────────") + dim.Render("]"),
		agent.EffortLow:    dim.Render("[") + lit.Render("━━") + dim.Render("────────") + dim.Render("]"),
		agent.EffortMedium: dim.Render("[") + lit.Render("━━━━") + dim.Render("──────") + dim.Render("]"),
		agent.EffortHigh:   dim.Render("[") + lit.Render("━━━━━━") + dim.Render("────") + dim.Render("]"),
		agent.EffortMax:    dim.Render("[") + lit.Render("━━━━━━━━━━") + dim.Render("]"),
	}
	for effort, want := range tests {
		if got := effortIndicator(effort); got != want {
			t.Errorf("effortIndicator(%q) = %q, want %q", effort, got, want)
		}
	}
}

// The track fills up to the selected tick, and every level is named under
// its own tick.
func TestSliderDrawsATrackWithEveryLevelLabelled(t *testing.T) {
	tests := map[agent.Effort]string{
		agent.EffortLow:  "<━━┃──────│──────│──────│──────│──>",
		agent.EffortHigh: "<━━┃━━━━━━┃━━━━━━┃──────│──────│──>",
		agent.EffortMax:  "<━━┃━━━━━━┃━━━━━━┃━━━━━━┃━━━━━━┃──>",
	}
	for effort, track := range tests {
		lines := strings.Split(ansi.Strip(newEffortSlider(effort).view()), "\n")
		if lines[0] != track {
			t.Errorf("%s: track = %q, want %q", effort, lines[0], track)
		}
		if want := "  low  medium  high   xhigh   max"; lines[1] != want {
			t.Errorf("%s: labels = %q, want %q", effort, lines[1], want)
		}
	}
}

func TestSliderHighlightsTheSelectedLabel(t *testing.T) {
	selected := lipgloss.NewStyle().Foreground(menu.NameColor).Bold(true)

	view := newEffortSlider(agent.EffortHigh).view()

	if !strings.Contains(view, selected.Render("high")) {
		t.Errorf("the selected label is not highlighted:\n%q", view)
	}
	if strings.Contains(view, selected.Render("medium")) {
		t.Errorf("a label other than the selected one is highlighted:\n%q", view)
	}
}

// aboveTheInput is the frame's row that ends in the model in use
func aboveTheInput(t *testing.T, m model) string {
	t.Helper()
	for _, row := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.HasPrefix(row, "─") && strings.Contains(row, "/") {
			return strings.TrimRight(row, " ")
		}
	}
	t.Fatalf("no row names the model:\n%s", ansi.Strip(m.View().Content))
	return ""
}

func TestIndicatorSitsLeftOfTheModel(t *testing.T) {
	m, _ := newEffortModel(t, "high")

	if row := aboveTheInput(t, m); !strings.HasSuffix(row, "─ [━━━━━━────] anthropic/thinker") {
		t.Errorf("row above the input = %q, want the bars before the model", row)
	}
}

func TestIndicatorIsHiddenWhenNoLevelIsSent(t *testing.T) {
	t.Run("thinking off", func(t *testing.T) {
		m, _ := newEffortModel(t, "high")
		m.config.ThinkingEnabled = false

		if row := aboveTheInput(t, m); strings.Contains(row, "[") {
			t.Errorf("row above the input = %q, want no bars", row)
		}
	})
	t.Run("model takes no level", func(t *testing.T) {
		m, _ := newEffortModel(t, "high")
		m.models = []agent.Model{{Provider: agent.ProviderAnthropic, ID: "thinker", Thinking: agent.ThinkingAdaptive}}

		if row := aboveTheInput(t, m); strings.Contains(row, "[") {
			t.Errorf("row above the input = %q, want no bars", row)
		}
	})
}

// inputRow is the frame's row holding the input, prompt included
func inputRow(t *testing.T, m model) string {
	t.Helper()
	for _, row := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.HasPrefix(row, inputPrompt+" ") {
			return strings.TrimRight(row, " ")
		}
	}
	t.Fatalf("no input row:\n%s", ansi.Strip(m.View().Content))
	return ""
}

// The levels are suggested in the input, after the command, and the menu row
// keeps describing what the command does.
func TestInputSuggestsTheLevels(t *testing.T) {
	for _, line := range []string{"/effort", "/effort "} {
		t.Run(line, func(t *testing.T) {
			m, _ := newEffortModel(t, "")

			m = typeText(t, m, line)

			if got, want := inputRow(t, m), inputPrompt+" /effort [low|medium|high|xhigh|max]"; got != want {
				t.Errorf("input row = %q, want %q", got, want)
			}
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "/effort  set how hard the model thinks, optionally by level") {
				t.Errorf("the menu row lost its description:\n%s", view)
			}
		})
	}
}

func TestTabCompletesAPartlyTypedLevel(t *testing.T) {
	m, _ := newEffortModel(t, "")
	m = typeText(t, m, "/effort x")

	if got, want := inputRow(t, m), inputPrompt+" /effort xhigh"; got != want {
		t.Errorf("input row = %q, want the rest of xhigh suggested", got)
	}

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	if got := m.input.Value(); got != "/effort xhigh" {
		t.Errorf("input after tab = %q, want /effort xhigh", got)
	}
}

// Tab on the bare command still completes the command, rather than typing the
// list of levels into the input.
func TestTabDoesNotTypeTheListOfLevels(t *testing.T) {
	m, _ := newEffortModel(t, "")
	m = typeText(t, m, "/effort")

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	if got := m.input.Value(); got != "/effort" {
		t.Errorf("input after tab = %q, want /effort", got)
	}
}

func TestOnlyACommandWithLevelsSuggestsAnything(t *testing.T) {
	m, _ := newEffortModel(t, "")

	m = typeText(t, m, "/model ")

	if got, want := inputRow(t, m), inputPrompt+" /model"; got != want {
		t.Errorf("input row = %q, want %q", got, want)
	}
}
