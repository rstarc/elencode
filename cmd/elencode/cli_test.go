package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
)

func cliNames() []string {
	var names []string
	for _, c := range cliCommands() {
		names = append(names, c.Name)
	}
	return names
}

func slashNames() []string {
	var names []string
	for _, c := range defaultCommands().Commands() {
		names = append(names, c.Name)
	}
	return names
}

// Whatever can be done from one side can be done from the other, so neither
// is a second-class way in. slashOnly is the list of reasons it cannot.
func TestEveryCLICommandIsASlashCommand(t *testing.T) {
	for _, name := range cliNames() {
		if !slices.Contains(slashNames(), name) {
			t.Errorf("`elencode %s` has no /%s", name, name)
		}
	}
}

func TestEverySlashCommandIsACLICommand(t *testing.T) {
	for _, name := range slashNames() {
		if slices.Contains(slashOnly, name) {
			continue
		}
		if !slices.Contains(cliNames(), name) {
			t.Errorf("/%s has no `elencode %s`; add one, or add it to slashOnly with the reason", name, name)
		}
	}
}

// An exemption for a command that no longer exists, or that has since gained
// a CLI equivalent, would hide the next one that should not be exempt.
func TestSlashOnlyListsOnlySlashCommandsWithoutACLIEquivalent(t *testing.T) {
	for _, name := range slashOnly {
		if !slices.Contains(slashNames(), name) {
			t.Errorf("slashOnly names %q, which is not a slash command", name)
		}
		if slices.Contains(cliNames(), name) {
			t.Errorf("slashOnly names %q, which is also a CLI command", name)
		}
	}
}

func TestRunCLIStartsTheSessionWithoutArguments(t *testing.T) {
	handled, err := runCLI(nil, &bytes.Buffer{})
	if handled || err != nil {
		t.Errorf("runCLI(nil) = %v, %v, want it left to the session", handled, err)
	}
}

// A mistyped command must not start a session as if nothing was asked for.
func TestRunCLIRejectsAnUnknownCommand(t *testing.T) {
	handled, err := runCLI([]string{"modle"}, &bytes.Buffer{})
	if !handled || err == nil {
		t.Fatalf("runCLI(modle) = %v, %v, want an error", handled, err)
	}
	for _, want := range []string{"modle", "model", "login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}

func TestRunCLIRunsTheNamedCommandWithTheRestOfTheArguments(t *testing.T) {
	var out bytes.Buffer
	handled, err := runCLI([]string{"version"}, &out)
	if !handled || err != nil {
		t.Fatalf("runCLI(version) = %v, %v", handled, err)
	}
	if !strings.HasPrefix(out.String(), "elencode ") {
		t.Errorf("version printed %q", out.String())
	}
}

func TestPrintConfigIsPlainTextWithTheKeyMasked(t *testing.T) {
	const key = "sk-ant-do-not-print-me"
	cfg := config.Config{AnthropicAPIKey: config.Secret(key), Model: "anthropic/x", Path: "/tmp/elencode/config.json"}
	var out bytes.Buffer

	printConfig(cfg, &out)

	got := out.String()
	if strings.Contains(got, key) {
		t.Error("printed the raw API key")
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("printed escape codes, want plain text for a pipe:\n%s", got)
	}
	// The same rows /config shows, and nothing about closing a view
	for _, want := range []string{"anthropic_api_key", cfg.AnthropicAPIKey.String(), "chatgpt_login", "anthropic/x", cfg.Path} {
		if !strings.Contains(got, want) {
			t.Errorf("printed config does not mention %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "esc") {
		t.Errorf("printed the view's key hint:\n%s", got)
	}
}

// listModels is runModelCLI with no argument against a session keyed for
// providers and on current.
func listModels(t *testing.T, providers providerSet, current agent.Model) string {
	t.Helper()
	var out bytes.Buffer
	if err := runModelCLI(config.Config{}, providers, testModels, current, "", &out); err != nil {
		t.Fatalf("runModelCLI: %v", err)
	}
	return out.String()
}

func TestModelCLIListsTheReachableModelsAndMarksTheCurrentOne(t *testing.T) {
	got := listModels(t, keyed(agent.ProviderAnthropic), testModels[0])

	if !strings.Contains(got, "* anthropic/model-one") {
		t.Errorf("listing does not mark the current model:\n%s", got)
	}
	if strings.Contains(got, "model-two") {
		t.Errorf("listing offers a model whose provider has no key:\n%s", got)
	}
}

// setModel is runModelCLI naming a model, saving to a fresh config file.
func setModel(t *testing.T, providers providerSet, name string) (string, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(`{"anthropic_api_key":"key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runModelCLI(config.Config{Path: file}, providers, testModels, testModels[0], name, &bytes.Buffer{})
	body, _ := os.ReadFile(file)
	return string(body), err
}

// The CLI has no session to switch, so naming a model is choosing the one the
// next session starts on — what /model saves as well.
func TestModelCLISavesTheNamedModel(t *testing.T) {
	saved, err := setModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), "model-two")
	if err != nil {
		t.Fatalf("runModelCLI: %v", err)
	}
	if !strings.Contains(saved, `"openai/model-two"`) {
		t.Errorf("config file = %s, want the qualified model", saved)
	}
	if !strings.Contains(saved, `"anthropic_api_key"`) {
		t.Errorf("config file = %s, want the rest of it kept", saved)
	}
}

func TestModelCLIRefusesAnUnknownModel(t *testing.T) {
	saved, err := setModel(t, keyed(agent.ProviderAnthropic), "no-such-model")
	if err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("err = %v, want unknown model", err)
	}
	if strings.Contains(saved, "no-such-model") {
		t.Errorf("config file = %s, saved anyway", saved)
	}
}

func TestModelCLIRefusesAModelItCannotReach(t *testing.T) {
	_, err := setModel(t, keyed(agent.ProviderAnthropic), "model-two")
	if err == nil || !strings.Contains(err.Error(), "no API key for openai") {
		t.Errorf("err = %v, want it to name the missing key", err)
	}
}
