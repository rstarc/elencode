package commands

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rstarc/elencode/internal/agent"
)

// ChooseEffortMsg asks the TUI to set how hard the model reasons. Level is
// what the user named on the command line, empty when they asked for the
// slider instead.
type ChooseEffortMsg struct{ Level string }

func NewEffortCommand() Command {
	var levels []string
	for _, effort := range agent.Efforts {
		levels = append(levels, string(effort))
	}
	return Command{
		Name:        "effort",
		Description: "set how hard the model thinks, optionally by level",
		Args:        levels,
		Execute: func(arg string) tea.Cmd {
			return func() tea.Msg { return ChooseEffortMsg{Level: arg} }
		},
	}
}
