package main

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderShellPrompt(t *testing.T) {
	tests := []struct {
		name   string
		dir    string
		branch string
		want   string
	}{
		{"home is shortened", "/Users/rstarc/projects/elencode", "main", "rstarc@host: ~/projects/elencode (main)"},
		{"no branch outside a repository", "/Users/rstarc", "", "rstarc@host: ~"},
		{"only a whole home is shortened", "/Users/rstarcx", "", "rstarc@host: /Users/rstarcx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ansi.Strip(renderShellPrompt("rstarc", "host", "/Users/rstarc", tt.dir, tt.branch))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
