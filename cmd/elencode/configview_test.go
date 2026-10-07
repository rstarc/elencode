package main

import (
	"strings"
	"testing"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
	"github.com/rstarc/elencode/internal/config"
)

// The view says where a key comes from, never what it is.
func TestRenderConfigNeverShowsTheAPIKey(t *testing.T) {
	const key = "sk-ant-do-not-print-me"
	cfg := config.Config{
		Credentials: config.Credentials{agent.ProviderAnthropic: {APIKey: key}},
		Path:        "/tmp/elencode/config.json",
	}

	view := renderConfig(cfg, 120)

	if strings.Contains(view, key) {
		t.Error("config view contains the raw API key")
	}
	if row := rowFor(view, "anthropic"); !strings.Contains(row, "connected") {
		t.Errorf("anthropic row = %q, want it connected", row)
	}
}

func TestRenderConfigShowsThePath(t *testing.T) {
	const path = "/home/someone/.config/elencode/config.json"

	view := renderConfig(config.Config{Path: path}, 120)

	if !strings.Contains(view, path) {
		t.Errorf("config view does not show the config file path:\n%s", view)
	}
}

// Each provider says where its key came from: a key in the environment is not
// the one in the file, and only the file's can be disconnected by elencode.
func TestRenderConfigNamesTheSourceOfEachKey(t *testing.T) {
	cfg := config.Config{
		Credentials: config.Credentials{agent.ProviderOpenAI: {APIKey: "o"}},
		Env: func(name string) (string, bool) {
			return "a", name == "ANTHROPIC_API_KEY"
		},
		Path: "/tmp/c.json",
	}

	view := renderConfig(cfg, 120)

	if row := rowFor(view, "anthropic"); !strings.Contains(row, "$ANTHROPIC_API_KEY") {
		t.Errorf("anthropic row = %q, want it to name the variable", row)
	}
	if row := rowFor(view, "openai"); !strings.Contains(row, "credentials.json") {
		t.Errorf("openai row = %q, want it to name credentials.json", row)
	}
}

func TestRenderConfigShowsWhereTheCredentialsAreKept(t *testing.T) {
	cfg := config.Config{CredentialsPath: "/home/someone/.config/elencode/credentials.json", Path: "/tmp/c.json"}

	if row := rowFor(renderConfig(cfg, 120), "credentials"); !strings.Contains(row, cfg.CredentialsPath) {
		t.Errorf("credentials row = %q, want it to name the file", row)
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

func TestRenderConfigShowsWhetherChatGPTIsSignedIn(t *testing.T) {
	signedIn := config.Config{
		Credentials: config.Credentials{agent.ProviderChatGPT: {Login: &chatgpt.Tokens{AccessToken: "a"}}},
		Path:        "/tmp/c.json",
	}
	if row := rowFor(renderConfig(signedIn, 120), "chatgpt"); !strings.Contains(row, "signed in") || strings.Contains(row, "not connected") {
		t.Errorf("chatgpt row = %q, want it signed in", row)
	}

	if row := rowFor(renderConfig(config.Config{Path: "/tmp/c.json"}, 120), "chatgpt"); !strings.Contains(row, "not connected") {
		t.Errorf("chatgpt row = %q, want it not connected", row)
	}
}
