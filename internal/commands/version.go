package commands

import tea "charm.land/bubbletea/v2"

// ShowVersionMsg asks the TUI to print the version line. The version is
// stamped into the main package, so the line is the TUI's to build.
type ShowVersionMsg struct{}

func NewVersionCommand() Command {
	return Command{
		Name:        "version",
		Description: "show the elencode version",
		Execute: func(arg string) tea.Cmd {
			return func() tea.Msg { return ShowVersionMsg{} }
		},
	}
}
