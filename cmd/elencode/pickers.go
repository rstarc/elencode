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
			return menu.Item{Name: commands.Prefix + c.Name, Description: c.Description}
		},
		Match:   matchCommand,
		Trigger: commands.Prefix,
		Empty:   "no matching command",
	}, registry.Commands()...)
}

// newModelList is the list of models /model opens. The id is shown first
// because it is what the user types after /model and what narrows the list;
// the display name is only there to recognise it by.
func newModelList() picker.Model[agent.Model] {
	return picker.New(picker.Config[agent.Model]{
		Render: func(model agent.Model) menu.Item {
			return menu.Item{Name: model.ID, Description: model.DisplayName}
		},
		Match: matchModel,
		Align: true,
		Empty: "no matching model",
	})
}

// matchCommand narrows the menu to the commands the line could still become,
// on the command word alone: "/model some-id" is still the /model command line,
// and a menu that said nothing matched would be lying about it.
//
// Prefix rather than fuzzy: there are few names and they are short, so a looser
// match buys nothing and makes the highlighted row harder to predict — and the
// highlight is what Enter runs.
func matchCommand(query, name string) bool {
	word, _, _ := strings.Cut(query, " ")
	return strings.HasPrefix(strings.ToLower(name), strings.ToLower(word))
}

// matchModel narrows on any part of the id, which is how a model is
// remembered: "opus", not "claude-opus".
func matchModel(query, name string) bool {
	return strings.Contains(strings.ToLower(name), strings.ToLower(query))
}
