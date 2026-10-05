package commands

import tea "charm.land/bubbletea/v2"

// LoginMsg asks the TUI to sign in. Arg is the rest of the command line — the
// provider, and --device if given — read the way `elencode login` reads its
// arguments. The browser round trip and the client it ends in are the TUI's,
// which holds the providers.
type LoginMsg struct{ Arg string }

// LogoutMsg asks the TUI to sign out of Provider, as named on the command line.
type LogoutMsg struct{ Provider string }

func NewLoginCommand() Command {
	return Command{
		Name:        "login",
		Description: "sign in to a provider, as in /login chatgpt (--device to use a code)",
		Execute: func(arg string) tea.Cmd {
			return func() tea.Msg { return LoginMsg{Arg: arg} }
		},
	}
}

func NewLogoutCommand() Command {
	return Command{
		Name:        "logout",
		Description: "sign out of a provider, as in /logout chatgpt",
		Execute: func(arg string) tea.Cmd {
			return func() tea.Msg { return LogoutMsg{Provider: arg} }
		},
	}
}
