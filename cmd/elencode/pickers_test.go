package main

import (
	"strings"
	"testing"

	"github.com/rstarc/elencode/internal/commands"
)

// TestMatchCommand pins prefix matching: the command names are few and short,
// so a looser match would only make the highlighted row harder to predict.
func TestMatchCommand(t *testing.T) {
	tests := []struct {
		name  string
		query string
		entry string
		want  bool
	}{
		{"the slash alone lists everything", "/", "/quit", true},
		{"prefix", "/qu", "/quit", true},
		{"the whole name", "/quit", "/quit", true},
		{"case insensitive", "/QUIT", "/quit", true},
		{"a typo is not a prefix", "/qut", "/quit", false},
		{"a subsequence is not a prefix", "/qt", "/quit", false},
		{"another command", "/qu", "/config", false},
		{"an argument still names its command", "/model some-id", "/model", true},
		{"an argument does not make other commands match", "/model some-id", "/quit", false},
		{"longer than the name", "/quits", "/quit", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := matchCommand(test.query, commands.Command{Name: strings.TrimPrefix(test.entry, commands.Prefix)}); got != test.want {
				t.Errorf("matchCommand(%q, %q) = %v, want %v", test.query, test.entry, got, test.want)
			}
		})
	}
}

// TestMatchModel pins substring matching: an id is remembered by its middle,
// so "opus" has to find it without "claude-" being typed first.
func TestMatchModel(t *testing.T) {
	tests := []struct {
		name  string
		query string
		entry string
		want  bool
	}{
		{"nothing typed lists everything", "", "claude-opus-5", true},
		{"the middle of an id", "opus", "claude-opus-5", true},
		{"the start of an id", "claude", "claude-opus-5", true},
		{"the whole id", "claude-opus-5", "claude-opus-5", true},
		{"case insensitive", "OPUS", "claude-opus-5", true},
		{"not in the id", "sonnet", "claude-opus-5", false},
		{"out of order is not a substring", "supo", "claude-opus-5", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := matchModel(test.query, test.entry); got != test.want {
				t.Errorf("matchModel(%q, %q) = %v, want %v", test.query, test.entry, got, test.want)
			}
		})
	}
}

// An alias is another way to type the same command, so the menu finds the
// command by it as well.
func TestMatchCommandMatchesAnAlias(t *testing.T) {
	connect := commands.Command{Name: "connect", Aliases: []string{"login"}}
	tests := []struct {
		query string
		want  bool
	}{
		{"/con", true},
		{"/log", true},
		{"/login chatgpt", true},
		{"/logout", false},
		{"/x", false},
	}
	for _, test := range tests {
		if got := matchCommand(test.query, connect); got != test.want {
			t.Errorf("matchCommand(%q, connect) = %v, want %v", test.query, got, test.want)
		}
	}
}

// The menu lists a command once, under its name, and says what else it
// answers to: otherwise /log would highlight a row named /connect for no
// visible reason.
func TestCommandMenuNamesTheAlias(t *testing.T) {
	menu := newCommandMenu(commands.NewRegistry(commands.Command{
		Name:        "connect",
		Description: "connect a provider",
		Aliases:     []string{"login"},
	}))
	menu.SetWidth(80)

	view := menu.SetQuery("/log").View()

	if !strings.Contains(view, "/connect") || !strings.Contains(view, "also /login") {
		t.Errorf("menu = %q, want /connect, naming /login", view)
	}
	if strings.Count(view, "/login") != 1 {
		t.Errorf("menu = %q, want /login named once, not listed as a row of its own", view)
	}
}
