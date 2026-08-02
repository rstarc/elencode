// Package transcript draws what a session says: the assistant's messages and
// reasoning, the tool calls, and the errors and notices around them. The agent
// owns the types; how wide they are and what color they come out is decided
// here, so nothing below the TUI has to know a terminal exists.
package transcript

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
)

// minBlockWidth is the narrowest box worth drawing. Below roughly five
// columns lipgloss ignores the requested width and renders at the content's
// natural width, which is exactly the clipping we are avoiding, so anything
// narrower than this is clamped up and allowed to overflow instead.
const minBlockWidth = 20

// markerFirst marks the first line of a block, so its start is easy to spot
// when scanning down the transcript. markerRest continues the left border on
// every following line without repeating the marker.
const (
	markerFirst = "*"
	markerRest  = "│"
)

// UserPromptMarker replaces markerFirst on a user message, matching the
// textinput prompt so typed input is recognizable in the transcript.
const UserPromptMarker = ">"

// The titles naming a block are the API's own type names, so what is on screen
// can be matched against the documentation without a glossary.
const (
	thinkingTitle         = "thinking"
	redactedThinkingTitle = "redacted_thinking"
)

var textBlockColor = lipgloss.BrightBlack

// thinkingColor is the dim gray the UI uses everywhere for text that is there
// to be glanced at rather than read: menu descriptions, key hints.
var thinkingColor = lipgloss.BrightBlack
var toolUseColor = lipgloss.BrightBlue
var errorColor = lipgloss.Red

// userBackground and userForeground give a user message a light gray
// background, like a chat bubble, to set it apart from the assistant's.
var userBackground = lipgloss.White
var userForeground = lipgloss.Black

var noticeColor = lipgloss.BrightBlack
var headerColor = lipgloss.BrightBlue

// thinkingStyle is how reasoning is set apart from the answer it led to
func thinkingStyle() lipgloss.Style {
	return lipgloss.NewStyle().Italic(true).Foreground(thinkingColor)
}

// blockWidth fits a block to the space available, which includes the marker
// column. available is 0 before the first WindowSizeMsg arrives.
func blockWidth(available int) int {
	return max(available, minBlockWidth)
}

// Block renders one block of a message, or "" for one with nothing to show
func Block(block agent.Block, role agent.Role, width int) string {
	switch block := block.(type) {
	case agent.TextBlock:
		if role == agent.RoleUser {
			style := lipgloss.NewStyle().Background(userBackground).Foreground(userForeground)
			return renderBoxedBlock(block.Text, style, UserPromptMarker, userBackground, width)
		}
		return renderBoxedBlock(block.Text, lipgloss.NewStyle(), markerFirst, textBlockColor, width)
	case agent.ThinkingBlock:
		// The same block the assistant's answer gets, dimmed and in italics:
		// reasoning is part of the transcript but is not the answer.
		return renderTitledBlock(thinkingTitle, block.Thinking, thinkingStyle(), markerFirst, textBlockColor, width)
	case agent.RedactedThinkingBlock:
		// Nothing to show but the fact that it happened: the reasoning is
		// encrypted, and only the API can read it back.
		return renderTitledBlock(redactedThinkingTitle, "", thinkingStyle(), markerFirst, textBlockColor, width)
	case agent.ToolUseBlock:
		style := lipgloss.NewStyle().Foreground(textBlockColor)
		name := style.Bold(true).Render(block.Name)
		arguments := style.Render(formatToolArguments(block.Input))
		return renderBoxedBlock(name+arguments, lipgloss.NewStyle(), markerFirst, toolUseColor, width)
	default:
		return ""
	}
}

// Message renders msg's blocks, dropping any that render empty (such as a
// ToolResultBlock, whose raw output is not shown) so they don't leave a blank
// line behind.
func Message(msg agent.Message, width int) string {
	var blocks []string
	for _, block := range msg.Content {
		if rendered := Block(block, msg.Role, width); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	return strings.Join(blocks, "\n")
}

// Error renders a failed turn. It is marked like a block so it reads as part of
// the transcript, but red and labelled so it is not mistaken for something the
// assistant said.
func Error(err error, width int) string {
	return renderBoxedBlock("Error: "+err.Error(), lipgloss.NewStyle(), markerFirst, errorColor, width)
}

// Notice renders something the program did rather than something either side
// said — switching models, say. It is drawn as a rule across the transcript,
// with none of the marker column a message block has, so it reads as a break in
// the conversation instead of a turn in it.
func Notice(text string, width int) string {
	return renderRule(text, width, lipgloss.NewStyle().Foreground(noticeColor))
}

// Header renders the title the session opens with, as the same rule a notice is
// drawn with but colored to read as a title rather than an event.
func Header(text string, width int) string {
	return renderRule(text, width, lipgloss.NewStyle().Foreground(headerColor).Bold(true))
}

// renderBoxedBlock prefixes content, wrapped to width, with a colored marker
// column: firstMarker on the first line, a continuing border rune on the
// rest. No vertical padding, so blocks stay dense in the transcript.
func renderBoxedBlock(content string, contentStyle lipgloss.Style, firstMarker string, markerColor color.Color, width int) string {
	return renderTitledBlock("", content, contentStyle, firstMarker, markerColor, width)
}

// renderTitledBlock is renderBoxedBlock with a title on a line of its own above
// the content, set against the right edge so it labels the block without
// competing with the text for the start of the line.
func renderTitledBlock(title, content string, contentStyle lipgloss.Style, firstMarker string, markerColor color.Color, width int) string {
	innerWidth := max(blockWidth(width)-2, 1) // marker + one space
	marker := lipgloss.NewStyle().Foreground(markerColor)

	var lines []string
	if title != "" {
		lines = append(lines, contentStyle.Width(innerWidth).Align(lipgloss.Right).Render(title))
	}
	// An untitled block still renders empty content, as one empty line: it is
	// the caller's to drop. A titled one has its title to show instead.
	if content != "" || title == "" {
		lines = append(lines, strings.Split(contentStyle.Width(innerWidth).Render(content), "\n")...)
	}

	for i, line := range lines {
		m := markerRest
		if i == 0 {
			m = firstMarker
		}
		lines[i] = marker.Render(m) + " " + line
	}
	return strings.Join(lines, "\n")
}

// renderRule centers label in a line of rule characters, spanning the width it
// is given: a rule that stopped short of the edge would read as a box around
// nothing rather than as a divider.
func renderRule(label string, width int, style lipgloss.Style) string {
	total := max(width, minBlockWidth)
	padded := " " + label + " "

	fill := total - lipgloss.Width(padded)
	if fill < 2 {
		// No room for a rule on both sides. The label alone still reads as a
		// break, since nothing else in the transcript is unmarked.
		return style.MaxWidth(total).Render(padded)
	}

	left := fill / 2
	return style.Render(strings.Repeat(rule, left) + padded + strings.Repeat(rule, fill-left))
}

// rule is the character a divider is drawn with
const rule = "─"
