package commands

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fixture stands in for the real registry, so these tests keep working as
// commands are added or renamed.
func fixture(ran *string) Registry {
	record := func(name string) func(string) tea.Cmd {
		return func(arg string) tea.Cmd {
			*ran = name + " " + arg
			return nil
		}
	}
	return NewRegistry(
		Command{Name: "config", Description: "the first one", Execute: record("config")},
		Command{Name: "model", Description: "the second one", Execute: record("model")},
		Command{Name: "quit", Description: "the third one", Execute: record("quit")},
	)
}

// names reduces a set of commands to their names, so tests can compare them
// without restating descriptions that are free to change.
func names(commands []Command) []string {
	got := make([]string, len(commands))
	for i, c := range commands {
		got[i] = c.Name
	}
	return got
}

// TestCommandsKeepsTheOrderItWasBuiltIn matters because that order is the one
// the menu offers them in, and it is chosen where the registry is assembled.
func TestCommandsKeepsTheOrderItWasBuiltIn(t *testing.T) {
	var ran string

	got := names(fixture(&ran).Commands())

	if want := "config,model,quit"; strings.Join(got, ",") != want {
		t.Errorf("Commands() = %v, want %v", got, want)
	}
}

func TestRunRequiresAnExactName(t *testing.T) {
	var ran string
	registry := fixture(&ran)

	if _, ok := registry.Run("/quit"); !ok {
		t.Error(`Run("/quit") found nothing, want the quit command`)
	}
	// A fuzzy match must not run on Enter, or a typo silently quits the program.
	if _, ok := registry.Run("/qt"); ok {
		t.Error(`Run("/qt") matched, want no match for a non-exact name`)
	}
}

func TestRunExecutesTheNamedCommand(t *testing.T) {
	var ran string
	registry := fixture(&ran)

	if _, ok := registry.Run("/config"); !ok {
		t.Fatal(`Run("/config") found nothing`)
	}
	if ran != "config " {
		t.Errorf("ran = %q, want the config command with no argument", ran)
	}
}

// TestRunPassesTheArgument covers "/model some-id": the argument is the
// command's input, not part of its name.
func TestRunPassesTheArgument(t *testing.T) {
	var ran string
	registry := fixture(&ran)

	if _, ok := registry.Run("/model   claude-opus-4-5  "); !ok {
		t.Fatal("Run found nothing for a command with an argument")
	}
	if want := "model claude-opus-4-5"; ran != want {
		t.Errorf("ran = %q, want %q", ran, want)
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		wantCmd, wantArg string
	}{
		{"no argument", "/model", "/model", ""},
		{"argument", "/model claude-opus-4-5", "/model", "claude-opus-4-5"},
		{"extra spaces", "/model   claude-opus-4-5  ", "/model", "claude-opus-4-5"},
		{"trailing space alone", "/model ", "/model", ""},
		{"plain text", "hello there", "hello", "there"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotCmd, gotArg := split(test.input)
			if gotCmd != test.wantCmd || gotArg != test.wantArg {
				t.Errorf("split(%q) = %q, %q, want %q, %q", test.input, gotCmd, gotArg, test.wantCmd, test.wantArg)
			}
		})
	}
}
