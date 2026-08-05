// Package commands holds the slash commands the user can run from the input
// field. A Command is shaped like an agent.Tool: metadata plus one closure. It
// reports what it did as a tea.Msg rather than touching the TUI, so a command
// can be run and asserted on without one.
package commands

import tea "charm.land/bubbletea/v2"

// Prefix opens the command menu when the input starts with it
const Prefix = "/"

// Command is a slash command the user can run from the input field. Execute
// receives whatever followed the name on the command line, empty when nothing
// did.
type Command struct {
	Name        string // without the leading slash
	Description string
	Execute     func(arg string) tea.Cmd
}

// Registry is the set of commands a session knows. Built by the caller, so what
// exists is decided in one place rather than by whichever files are linked in.
type Registry struct{ commands []Command }

func NewRegistry(commands ...Command) Registry {
	return Registry{commands: commands}
}

// Commands is every command the registry knows, in the order it was built.
// Which ones a half-typed line matches is the menu's business, not the
// registry's.
func (r Registry) Commands() []Command { return r.commands }
