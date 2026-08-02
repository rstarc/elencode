package commands

import tea "charm.land/bubbletea/v2"

// QuitMsg asks the TUI to exit. Not tea.Quit itself: the TUI has an in-flight
// turn to abandon first, so its goroutine unblocks instead of waiting on a
// channel nobody will read again.
type QuitMsg struct{}

func NewQuitCommand() Command {
	return Command{
		Name:        "quit",
		Description: "exit elencode",
		Execute: func(arg string) tea.Cmd {
			return func() tea.Msg { return QuitMsg{} }
		},
	}
}
