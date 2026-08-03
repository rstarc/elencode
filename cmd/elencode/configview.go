package main

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/tui/menu"
)

// renderConfig draws the read-only configuration view. The API key is printed
// through Secret.String, so the value cannot reach the screen.
func renderConfig(cfg config.Config, width int) string {
	source := "from config file"
	if cfg.AnthropicKeyFromEnv {
		source = "from " + config.ANTHROPIC_API_KEY_ENV_VAR_NAME
	}

	title := lipgloss.NewStyle().Foreground(menu.NameColor).Render("configuration")
	rows := []string{
		menu.Row(menu.Marker, title, width),
		menu.Row(menu.Marker, "", width),
		configRow("anthropic_api_key", cfg.AnthropicAPIKey.String()+"  ("+source+")", width),
		configRow("model", cfg.Model, width),
		configRow("thinking_enabled", strconv.FormatBool(cfg.ThinkingEnabled), width),
		configRow("config file", cfg.Path, width),
		menu.Row(menu.Marker, "", width),
		menu.Row(menu.Marker, lipgloss.NewStyle().Foreground(menu.DescriptionColor).Render("esc to close"), width),
	}
	return strings.Join(rows, "\n")
}

// configRow renders one name/value pair, with the name padded so the values line up
func configRow(name, value string, width int) string {
	label := lipgloss.NewStyle().Foreground(menu.DescriptionColor).Width(configLabelWidth).Render(name)
	return menu.Row(menu.Marker, label+value, width)
}

// configLabelWidth is the column the values start in, wide enough for the
// longest label the view currently has.
const configLabelWidth = 20
