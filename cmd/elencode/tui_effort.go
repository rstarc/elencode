package main

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/tui/menu"
	"github.com/rstarc/elencode/internal/tui/transcript"
)

// effortIndicator is the level as a bracketed line, two columns per level,
// filled up to effort. An unset level fills none: it is the API's to pick.
func effortIndicator(effort agent.Effort) string {
	lit := slices.Index(agent.Efforts, effort) + 1
	litStyle := lipgloss.NewStyle().Foreground(menu.NameColor)
	dimStyle := lipgloss.NewStyle().Foreground(menu.DescriptionColor)

	line := dimStyle.Render("[")
	if lit > 0 {
		line += litStyle.Render(strings.Repeat("━", 2*lit))
	}
	if lit < len(agent.Efforts) {
		line += dimStyle.Render(strings.Repeat("─", 2*(len(agent.Efforts)-lit)))
	}
	return line + dimStyle.Render("]")
}

// effortSlider is what /effort opens: a track with a tick per level, moved
// along with the arrow keys. It
// has the keyboard while open, as the key entry does. The zero value is
// closed.
type effortSlider struct {
	open  bool
	index int // into agent.Efforts
}

func newEffortSlider(current agent.Effort) effortSlider {
	index := slices.Index(agent.Efforts, current)
	if index < 0 {
		index = len(agent.Efforts) / 2
	}
	return effortSlider{open: true, index: index}
}

func (s effortSlider) level() agent.Effort { return agent.Efforts[s.index] }

// view draws the track filled up to the selected tick, and each level's name
// centred under its tick, the selected one highlighted.
func (s effortSlider) view() string {
	litStyle := lipgloss.NewStyle().Foreground(menu.NameColor)
	dimStyle := lipgloss.NewStyle().Foreground(menu.DescriptionColor)
	selectedStyle := litStyle.Bold(true)

	track := dimStyle.Render("<")
	width := 1 // columns of the track drawn so far
	labels := ""
	for i, effort := range agent.Efforts {
		segment, tick, style := "─", "│", dimStyle
		if i <= s.index {
			segment, tick, style = "━", "┃", litStyle
		}
		// A short lead-in before the first tick, and room for a name between
		// every two after it
		run := 6
		if i == 0 {
			run = 2
		}
		track += style.Render(strings.Repeat(segment, run) + tick)
		width += run + 1

		name := string(effort)
		labelStyle := dimStyle
		if i == s.index {
			labelStyle = selectedStyle
		}
		start := width - 1 - len(name)/2
		labels += strings.Repeat(" ", max(start-lipgloss.Width(labels), 1)) + labelStyle.Render(name)
	}
	track += dimStyle.Render("──>")

	hint := dimStyle.Render("←/→ adjust · enter set · esc cancel")
	return track + "\n" + labels + "\n\n" + hint
}

// effortApplies reports whether the model in use is sent an effort level at
// all: only one that takes a level is, and only with thinking on.
func (m model) effortApplies() bool {
	inUse, _ := agent.FindModel(m.models, m.config.Model)
	return m.config.ThinkingEnabled && inUse.Thinking == agent.ThinkingEffort
}

// chooseEffort sets the level the user named, or opens the slider when they
// named none.
func (m model) chooseEffort(name string) (model, tea.Cmd) {
	if name == "" {
		m.effortSlider = newEffortSlider(m.effort)
		return m, nil
	}

	effort, ok := agent.ParseEffort(name)
	if !ok {
		var names []string
		for _, effort := range agent.Efforts {
			names = append(names, string(effort))
		}
		return m, m.reportError(fmt.Errorf("unknown effort %q (levels: %s)", name, strings.Join(names, ", ")))
	}
	return m.setEffort(effort)
}

// setEffort applies from the next turn: one in flight keeps its level. It is
// not saved, so the next session starts on thinking_effort again.
func (m model) setEffort(effort agent.Effort) (model, tea.Cmd) {
	m.effort = effort
	m.agent.SetEffort(effort)

	said := "effort set to " + string(effort)
	switch {
	case !m.config.ThinkingEnabled:
		said += " (not used: thinking is off)"
	case !m.effortApplies():
		said += " (not used by the current model)"
	}
	return m, printAbove(transcript.Notice(said, m.width))
}

// pressDuringEffortSlider moves the slider, sets the level it is on, or
// closes it without changing anything.
func (m model) pressDuringEffortSlider(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		m.effortSlider.index = max(m.effortSlider.index-1, 0)
	case "right", "l":
		m.effortSlider.index = min(m.effortSlider.index+1, len(agent.Efforts)-1)
	case "enter":
		chosen := m.effortSlider.level()
		m.effortSlider = effortSlider{}
		return m.setEffort(chosen)
	case "esc", "ctrl+c":
		m.effortSlider = effortSlider{}
	}
	return m, nil
}
