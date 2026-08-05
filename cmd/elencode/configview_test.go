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

// TestRenderConfigNamesTheSourceOfEachKey: the two keys have their own
// provenance, and showing one key's source against the other's value would say
// something untrue about where the session's key came from.
func TestRenderConfigNamesTheSourceOfEachKey(t *testing.T) {
	cfg := config.Config{
		Provider:            config.ProviderOpenAI,
		AnthropicAPIKey:     "a",
		OpenAIAPIKey:        "o",
		AnthropicKeyFromEnv: true,
		Path:                "/tmp/c.json",
	}

	view := renderConfig(cfg, 120)

	anthropic := rowFor(view, "anthropic_api_key")
	if !strings.Contains(anthropic, config.ANTHROPIC_API_KEY_ENV_VAR_NAME) {
		t.Errorf("anthropic key row = %q, want it to name the environment", anthropic)
	}
	openai := rowFor(view, "openai_api_key")
	if openai == "" || !strings.Contains(openai, "config file") {
		t.Errorf("openai key row = %q, want it to name the config file", openai)
	}
}

func TestRenderConfigShowsTheProvider(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderOpenAI, Path: "/tmp/c.json"}

	if row := rowFor(renderConfig(cfg, 80), "provider"); !strings.Contains(row, config.ProviderOpenAI) {
		t.Errorf("provider row = %q, want it to say openai", row)
	}
}

func TestRenderConfigShowsTheThinkingEffort(t *testing.T) {
	cfg := config.Config{ThinkingEffort: "xhigh", Path: "/tmp/c.json"}

	if row := rowFor(renderConfig(cfg, 80), "thinking_effort"); !strings.Contains(row, "xhigh") {
		t.Errorf("thinking_effort row = %q, want it to say xhigh", row)
	}

	// An unset effort is a real setting — the API picks the level — so the row
	// has to say that rather than showing nothing.
	row := rowFor(renderConfig(config.Config{Path: "/tmp/c.json"}, 80), "thinking_effort")
	if !strings.Contains(row, "default") {
		t.Errorf("unset thinking_effort row = %q, want it to say the API decides", row)
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
