package commands

import tea "charm.land/bubbletea/v2"

// ShowConfigMsg asks the TUI to open the read-only configuration view
type ShowConfigMsg struct{}

func NewConfigCommand() Command {
	return Command{
		Name:        "config",
		Description: "show the current configuration",
		Execute: func(arg string) tea.Cmd {
			return func() tea.Msg { return ShowConfigMsg{} }
		},
	}
}
