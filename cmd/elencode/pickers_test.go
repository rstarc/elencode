package main

import "testing"

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
		{"longer than the name", "/quits", "/quit", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := matchCommand(test.query, test.entry); got != test.want {
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
