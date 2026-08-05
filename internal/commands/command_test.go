package commands

import (
	"strings"
	"testing"
)

// TestCommandsKeepsTheOrderItWasBuiltIn matters because that order is the one
// the menu offers them in, and it is chosen where the registry is assembled.
// What running a command does is the TUI's to test: the registry only holds
// them.
func TestCommandsKeepsTheOrderItWasBuiltIn(t *testing.T) {
	registry := NewRegistry(
		Command{Name: "config", Description: "the first one"},
		Command{Name: "model", Description: "the second one"},
		Command{Name: "quit", Description: "the third one"},
	)

	got := make([]string, 0, 3)
	for _, c := range registry.Commands() {
		got = append(got, c.Name)
	}

	if want := "config,model,quit"; strings.Join(got, ",") != want {
		t.Errorf("Commands() = %v, want %v", got, want)
	}
}
