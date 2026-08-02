// Package commands holds the slash commands the user can run from the input
// field. A Command is shaped like an agent.Tool: metadata plus one closure. It
// reports what it did as a tea.Msg rather than touching the TUI, so a command
// can be run and asserted on without one.
package commands

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

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

// Match returns the commands whose names fuzzy-match input, which must start
// with the prefix. Registry order is kept; there is no scoring.
func (r Registry) Match(input string) []Command {
	query, ok := strings.CutPrefix(input, Prefix)
	if !ok {
		return nil
	}

	var matches []Command
	for _, c := range r.commands {
		if isSubsequence(strings.ToLower(query), c.Name) {
			matches = append(matches, c)
		}
	}
	return matches
}

// Run executes the command input names exactly, passing it the rest of the
// line, and reports whether there was one. Exact rather than fuzzy: a typo must
// not run a command the user did not spell out.
func (r Registry) Run(input string) (tea.Cmd, bool) {
	head, arg := split(input)
	name, ok := strings.CutPrefix(head, Prefix)
	if !ok {
		return nil, false
	}
	for _, c := range r.commands {
		if strings.EqualFold(name, c.Name) {
			return c.Execute(arg), true
		}
	}
	return nil, false
}

// split separates a command line into the command and its argument, so
// "/model some-id" runs /model with some-id rather than being looked up whole.
func split(input string) (name, arg string) {
	name, arg, _ = strings.Cut(strings.TrimSpace(input), " ")
	return name, strings.TrimSpace(arg)
}

// isSubsequence reports whether every rune of query appears in name, in order
// but not necessarily adjacent, so "/qt" finds "quit".
func isSubsequence(query, name string) bool {
	rest := name
	for _, r := range query {
		i := strings.IndexRune(rest, r)
		if i < 0 {
			return false
		}
		rest = rest[i+len(string(r)):]
	}
	return true
}
