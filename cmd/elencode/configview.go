package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/tui/menu"
)

// renderConfig draws the read-only configuration view. The API keys are printed
// through Secret.String, so the values cannot reach the screen.
//
// Both keys are shown: both are live, since a session reaches whichever
// providers were keyed, and the model row says which one it is talking to.
func renderConfig(cfg config.Config, width int) string {
	title := lipgloss.NewStyle().Foreground(menu.NameColor).Render("configuration")
	rows := []string{
		menu.Row(menu.Marker, title, width),
		menu.Row(menu.Marker, "", width),
	}
	for _, setting := range configSettings(cfg) {
		rows = append(rows, configRow(setting.name, setting.value, width))
	}
	rows = append(rows,
		menu.Row(menu.Marker, "", width),
		menu.Row(menu.Marker, lipgloss.NewStyle().Foreground(menu.DescriptionColor).Render("esc to close"), width),
	)
	return strings.Join(rows, "\n")
}

// printConfig writes what /config shows as plain text, for `elencode config`.
func printConfig(cfg config.Config, out io.Writer) {
	for _, setting := range configSettings(cfg) {
		_, _ = fmt.Fprintf(out, "%-*s%s\n", configLabelWidth, setting.name, setting.value)
	}
}

// setting is one name/value row of the configuration.
type setting struct{ name, value string }

// configSettings is every row the configuration is shown as, in order: one
// list, so /config and `elencode config` cannot drift apart.
func configSettings(cfg config.Config) []setting {
	return []setting{
		{"anthropic_api_key", keyValue(cfg.AnthropicAPIKey, cfg.AnthropicKeyFromEnv, config.ANTHROPIC_API_KEY_ENV_VAR_NAME)},
		{"openai_api_key", keyValue(cfg.OpenAIAPIKey, cfg.OpenAIKeyFromEnv, config.OPENAI_API_KEY_ENV_VAR_NAME)},
		{"chatgpt_login", loginValue(cfg.ChatGPTLoginPath)},
		{"model", cfg.Model},
		{"thinking_enabled", strconv.FormatBool(cfg.ThinkingEnabled)},
		{"thinking_effort", effortValue(cfg.ThinkingEffort)},
		{"config file", cfg.Path},
	}
}

// keyValue masks a key and says where it came from, which is the whole point of
// the view: a key from the environment is not the one in the file.
func keyValue(key config.Secret, fromEnv bool, envVar string) string {
	source := "from config file"
	if fromEnv {
		source = "from " + envVar
	}
	return key.String() + "  (" + source + ")"
}

// loginValue says where the ChatGPT login is saved, or how to get one: there
// is no value to show, only whether it is there.
func loginValue(path string) string {
	if path == "" {
		return "(not signed in: /login chatgpt, or `elencode login chatgpt`)"
	}
	return "signed in  (" + path + ")"
}

// effortValue names what an unset effort means, rather than leaving the row
// blank as if the setting did nothing.
func effortValue(effort string) string {
	if effort == "" {
		return "(the API's default)"
	}
	return effort
}

// configRow renders one name/value pair, with the name padded so the values line up
func configRow(name, value string, width int) string {
	label := lipgloss.NewStyle().Foreground(menu.DescriptionColor).Width(configLabelWidth).Render(name)
	return menu.Row(menu.Marker, label+value, width)
}

// configLabelWidth is the column the values start in, wide enough for the
// longest label the view currently has.
const configLabelWidth = 20
