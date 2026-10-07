package main

import (
	"strings"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/commands"
	"github.com/rstarc/elencode/internal/tui/menu"
	"github.com/rstarc/elencode/internal/tui/picker"
)

// newCommandMenu is the list of slash commands that opens under the input as
// the user types one. The slash is part of the name rather than stripped off,
// so a command is matched and completed as it is typed.
func newCommandMenu(registry commands.Registry) picker.Model[commands.Command] {
	return picker.New(picker.Config[commands.Command]{
		Render: func(c commands.Command) menu.Item {
			// A row found by an alias says why it was found
			description := c.Description
			if len(c.Aliases) > 0 {
				description += " (also " + commands.Prefix + strings.Join(c.Aliases, ", "+commands.Prefix) + ")"
			}
			return menu.Item{Name: commands.Prefix + c.Name, Description: description}
		},
		Match:   matchCommand,
		Trigger: commands.Prefix,
		Empty:   "no matching command",
	}, registry.Commands()...)
}

// newModelList is the list of models /model opens. The id is shown first
// because it is what the user types after /model and what narrows the list;
// the provider and display name are there to recognise it by, and the list
// mixes providers, so which one a model belongs to has to be on the row.
func newModelList() picker.Model[agent.Model] {
	return picker.New(picker.Config[agent.Model]{
		Render: func(model agent.Model) menu.Item {
			return menu.Item{Name: model.ID, Description: string(model.Provider) + " · " + model.DisplayName}
		},
		Match: func(query string, model agent.Model) bool { return matchModel(query, model.ID) },
		Align: true,
		Empty: "no matching model",
	})
}

// newProviderList is the list /connect opens: every provider, whether it is
// connected and with what. Choosing one connects it, so it is also how a key
// is replaced.
func newProviderList() picker.Model[providerStatus] {
	return picker.New(picker.Config[providerStatus]{
		Render: func(status providerStatus) menu.Item {
			return menu.Item{Name: string(status.provider), Description: status.description()}
		},
		Match: func(query string, status providerStatus) bool { return matchModel(query, string(status.provider)) },
		Align: true,
		Empty: "no matching provider",
	})
}

// matchCommand narrows the menu to the commands the line could still become,
// on the command word alone: "/model some-id" is still the /model command line,
// and a menu that said nothing matched would be lying about it.
//
// Prefix rather than fuzzy: there are few names and they are short, so a looser
// match buys nothing and makes the highlighted row harder to predict — and the
// highlight is what Enter runs.
//
// An alias matches as the name does, so a command can be reached by either.
func matchCommand(query string, c commands.Command) bool {
	word, _, _ := strings.Cut(query, " ")
	for _, name := range append([]string{c.Name}, c.Aliases...) {
		if strings.HasPrefix(strings.ToLower(commands.Prefix+name), strings.ToLower(word)) {
			return true
		}
	}
	return false
}

// matchModel narrows on any part of the id, which is how a model is
// remembered: "opus", not "claude-opus".
func matchModel(query, name string) bool {
	return strings.Contains(strings.ToLower(name), strings.ToLower(query))
}
