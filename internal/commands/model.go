package commands

import tea "charm.land/bubbletea/v2"

// ChooseModelMsg asks the TUI to switch models. ID is what the user named on
// the command line, empty when they asked for the picker instead. Listing the
// models is an API call and needs the agent, which is the TUI's to hold, so the
// command carries the intent and no more.
type ChooseModelMsg struct{ ID string }

func NewModelCommand() Command {
	return Command{
		Name:        "model",
		Description: "choose the model, optionally by id",
		Execute: func(arg string) tea.Cmd {
			return func() tea.Msg { return ChooseModelMsg{ID: arg} }
		},
	}
}
