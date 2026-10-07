package commands

import tea "charm.land/bubbletea/v2"

// ConnectMsg asks the TUI to connect a provider. Arg is the rest of the
// command line — the provider, and --device if given — read the way
// `elencode connect` reads its arguments. What connecting takes and the
// client it ends in are the TUI's, which holds the providers.
type ConnectMsg struct{ Arg string }

// DisconnectMsg asks the TUI to disconnect Provider, as named on the command
// line.
type DisconnectMsg struct{ Provider string }

func NewConnectCommand() Command {
	return Command{
		Name:        "connect",
		Description: "connect a provider, as in /connect chatgpt (--device to use a code)",
		Aliases:     []string{"login"},
		Execute: func(arg string) tea.Cmd {
			return func() tea.Msg { return ConnectMsg{Arg: arg} }
		},
	}
}

func NewDisconnectCommand() Command {
	return Command{
		Name:        "disconnect",
		Description: "disconnect a provider, as in /disconnect chatgpt",
		Aliases:     []string{"logout"},
		Execute: func(arg string) tea.Cmd {
			return func() tea.Msg { return DisconnectMsg{Provider: arg} }
		},
	}
}
