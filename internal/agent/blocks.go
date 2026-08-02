package agent

import (
	"bytes"
	"encoding/json"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
)

// Block is a part of a message
// By definining it as an interface with an unexported function, we emulate a sum type in Go
type Block interface{ block() }

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

var textBlockColor = lipgloss.BrightBlack

// thinkingStyle is how reasoning is set apart from the answer it led to
func thinkingStyle() lipgloss.Style {
	return lipgloss.NewStyle().Italic(true).Foreground(thinkingColor)
}

// The titles naming a block are the API's own type names, so what is on screen
// can be matched against the documentation without a glossary.
const (
	thinkingTitle         = "thinking"
	redactedThinkingTitle = "redacted_thinking"
)

// thinkingColor is the dim gray the UI uses everywhere for text that is there
// to be glanced at rather than read: menu descriptions, key hints.
var thinkingColor = lipgloss.BrightBlack
var toolUseColor = lipgloss.BrightBlue
var errorColor = lipgloss.Red

// userBackground and userForeground give a user message a light gray
// background, like a chat bubble, to set it apart from the assistant's.
var userBackground = lipgloss.White
var userForeground = lipgloss.Black

// blockWidth fits a block to the space available, which includes the marker
// column. available is 0 before the first WindowSizeMsg arrives.
func blockWidth(available int) int {
	return max(available, minBlockWidth)
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

func RenderBlock(block Block, role Role, width int) string {
	switch block := block.(type) {
	case TextBlock:
		if role == RoleUser {
			style := lipgloss.NewStyle().Background(userBackground).Foreground(userForeground)
			return renderBoxedBlock(block.Text, style, UserPromptMarker, userBackground, width)
		}
		return renderBoxedBlock(renderMarkdown(block.Text, width), lipgloss.NewStyle(), markerFirst, textBlockColor, width)
	case ThinkingBlock:
		// The same block the assistant's answer gets, dimmed and in italics:
		// reasoning is part of the transcript but is not the answer.
		return renderTitledBlock(thinkingTitle, block.Thinking, thinkingStyle(), markerFirst, textBlockColor, width)
	case RedactedThinkingBlock:
		// Nothing to show but the fact that it happened: the reasoning is
		// encrypted, and only the API can read it back.
		return renderTitledBlock(redactedThinkingTitle, "", thinkingStyle(), markerFirst, textBlockColor, width)
	case ToolUseBlock:
		style := lipgloss.NewStyle().Foreground(textBlockColor)
		name := style.Bold(true).Render(block.Name)
		arguments := style.Render(formatToolArguments(block.Input))
		return renderBoxedBlock(name+arguments, lipgloss.NewStyle(), markerFirst, toolUseColor, width)
	default:
		return ""
	}
}

func renderMarkdown(text string, width int) string {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		// Glamour's dark style reserves two columns of document margin on
		// either side. Those margins are removed below before the block adds
		// its own marker column.
		glamour.WithWordWrap(max(blockWidth(width)+1, 1)),
	)
	if err != nil {
		return text
	}

	rendered, err := renderer.Render(text)
	if err != nil {
		return text
	}
	rendered = strings.ReplaceAll(rendered, "\x1b[38;5;203", markdownCodeColorPrefix())
	lines := strings.Split(rendered, "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[0])) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = trimMarkdownMargin(line)
	}
	return strings.Join(lines, "\n")
}

func markdownCodeColorPrefix() string {
	prefix, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(headerColor).Render("x"), "x")
	return strings.TrimSuffix(prefix, "m")
}

func trimMarkdownMargin(line string) string {
	var result strings.Builder
	trimmed := 0
	atStart := true
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			start := i
			i++
			if i < len(line) && line[i] == '[' {
				i++
				for i < len(line) && (line[i] < '@' || line[i] > '~') {
					i++
				}
				if i < len(line) {
					i++
				}
			}
			result.WriteString(line[start:i])
			continue
		}
		if atStart && line[i] == ' ' && trimmed < 2 {
			trimmed++
			i++
			continue
		}
		atStart = false
		result.WriteString(line[i:])
		break
	}
	return result.String()
}

// formatToolArguments makes the first argument compact and positional, while
// naming the rest so optional arguments remain understandable in the transcript.
func formatToolArguments(input json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(input))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "(" + strings.TrimSpace(string(input)) + ")"
	}

	var arguments []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "(" + strings.TrimSpace(string(input)) + ")"
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "(" + strings.TrimSpace(string(input)) + ")"
		}
		keyName, ok := key.(string)
		if !ok {
			return "(" + strings.TrimSpace(string(input)) + ")"
		}
		formatted := formatToolArgument(value)
		if len(arguments) > 0 {
			formatted = keyName + "=" + formatted
		}
		arguments = append(arguments, formatted)
	}

	return "(" + strings.Join(arguments, ",") + ")"
}

func formatToolArgument(value json.RawMessage) string {
	var text string
	if len(value) > 0 && value[0] == '"' && json.Unmarshal(value, &text) == nil {
		return text
	}

	var compact bytes.Buffer
	if json.Compact(&compact, value) == nil {
		return compact.String()
	}
	return strings.TrimSpace(string(value))
}

// RenderStreamingText renders in-progress assistant text
func RenderStreamingText(text string, width int) string {
	return RenderBlock(TextBlock{Text: text}, RoleAssistant, width)
}

// RenderError renders a failed turn. It is marked like a block so it reads as
// part of the transcript, but red and labelled so it is not mistaken for
// something the assistant said.
func RenderError(err error, width int) string {
	return renderBoxedBlock("Error: "+err.Error(), lipgloss.NewStyle(), markerFirst, errorColor, width)
}

// RenderNotice renders something the program did rather than something either
// side said — switching models, say. It is drawn as a rule across the
// transcript, with none of the marker column a message block has, so it reads
// as a break in the conversation instead of a turn in it.
func RenderNotice(text string, width int) string {
	return renderRule(text, width, lipgloss.NewStyle().Foreground(noticeColor))
}

// RenderHeader renders the title the session opens with, as the same rule a
// notice is drawn with but colored to read as a title rather than an event.
func RenderHeader(text string, width int) string {
	return renderRule(text, width, lipgloss.NewStyle().Foreground(headerColor).Bold(true))
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

var noticeColor = lipgloss.BrightBlack
var headerColor = lipgloss.BrightBlue

type TextBlock struct{ Text string }

func (b TextBlock) block() {}

// ThinkingBlock is the model's reasoning. Signature is opaque and is carried
// only so the block can be sent back unaltered: the API rejects reasoning it
// cannot verify it produced.
type ThinkingBlock struct {
	Thinking  string
	Signature string
}

func (b ThinkingBlock) block() {}

// RedactedThinkingBlock is reasoning the API encrypted rather than returned.
// Data is opaque and carried only so the block can be sent back.
type RedactedThinkingBlock struct{ Data string }

func (b RedactedThinkingBlock) block() {}

type ToolUseBlock struct {
	ID    string // opaque, provider specific. never change this
	Name  string // Name of the Tool to use TODO: Use a custom type?
	Input json.RawMessage
}

func (b ToolUseBlock) block() {}

type ToolResultBlock struct {
	ToolUseID string // ID of ToolUseBlock
	Content   string // Tool output
	IsError   bool   // Whether the ToolUse failed
}

func (b ToolResultBlock) block() {}

func NewToolResultBlock(id string, content string, isError bool) ToolResultBlock {
	return ToolResultBlock{
		ToolUseID: id,
		Content:   content,
		IsError:   isError,
	}
}
