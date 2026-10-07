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

// renderConfig draws the read-only configuration view. Every provider is
// shown, since a session reaches all that are connected, and the model row
// says which one it is talking to.
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
// list, so /config and `elencode config` cannot drift apart. A provider's row
// says how it is connected, never with what key.
func configSettings(cfg config.Config) []setting {
	var settings []setting
	for _, status := range providerStatuses(cfg) {
		settings = append(settings, setting{string(status.provider), status.description()})
	}
	return append(settings,
		setting{"model", cfg.Model},
		setting{"thinking_enabled", strconv.FormatBool(cfg.ThinkingEnabled)},
		setting{"thinking_effort", effortValue(cfg.ThinkingEffort)},
		setting{"config file", cfg.Path},
		setting{"credentials", cfg.CredentialsPath},
	)
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
