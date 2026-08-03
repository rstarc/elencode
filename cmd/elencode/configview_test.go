package main

import (
	"strings"
	"testing"

	"github.com/rstarc/elencode/internal/config"
)

func TestRenderConfigMasksTheAPIKey(t *testing.T) {
	const key = "sk-ant-do-not-print-me"
	cfg := config.Config{AnthropicAPIKey: config.Secret(key), Path: "/tmp/elencode/config.json"}

	view := renderConfig(cfg, 80)

	if strings.Contains(view, key) {
		t.Error("config view contains the raw API key")
	}
	if !strings.Contains(view, cfg.AnthropicAPIKey.String()) {
		t.Errorf("config view does not show the mask:\n%s", view)
	}
}

func TestRenderConfigShowsThePath(t *testing.T) {
	const path = "/home/someone/.config/elencode/config.json"

	view := renderConfig(config.Config{Path: path}, 120)

	if !strings.Contains(view, path) {
		t.Errorf("config view does not show the config file path:\n%s", view)
	}
}

func TestRenderConfigNamesTheSource(t *testing.T) {
	tests := []struct {
		name      string
		fromEnv   bool
		wantMatch string
	}{
		{"environment", true, config.ANTHROPIC_API_KEY_ENV_VAR_NAME},
		{"config file", false, "config file"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{AnthropicAPIKey: "x", AnthropicKeyFromEnv: test.fromEnv, Path: "/tmp/c.json"}

			if view := renderConfig(cfg, 80); !strings.Contains(view, test.wantMatch) {
				t.Errorf("config view does not name %q as the source:\n%s", test.wantMatch, view)
			}
		})
	}
}

func TestRenderConfigSaysHowToClose(t *testing.T) {
	if view := renderConfig(config.Config{}, 80); !strings.Contains(view, "esc") {
		t.Errorf("config view does not say how to close it:\n%s", view)
	}
}

func TestRenderConfigShowsTheModel(t *testing.T) {
	cfg := config.Config{Model: "claude-opus-4-5", Path: "/tmp/c.json"}

	if view := renderConfig(cfg, 80); !strings.Contains(view, cfg.Model) {
		t.Errorf("config view does not show the model:\n%s", view)
	}
}

func TestRenderConfigShowsWhetherThinkingIsOn(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		want    string
	}{
		{"on", true, "true"},
		{"off", false, "false"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{ThinkingEnabled: test.enabled, Path: "/tmp/c.json"}

			view := renderConfig(cfg, 80)

			line := rowFor(view, "thinking_enabled")
			if line == "" {
				t.Fatalf("config view has no thinking_enabled row:\n%s", view)
			}
			if !strings.Contains(line, test.want) {
				t.Errorf("thinking row = %q, want it to say %q", line, test.want)
			}
		})
	}
}

// rowFor returns the config view's row for a setting, or "" if it has none
func rowFor(view, name string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	return ""
}
