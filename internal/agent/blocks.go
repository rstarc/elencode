package agent

import (
	"charm.land/lipgloss/v2"
	"encoding/json"
	"image/color"
	"strings"
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
	innerWidth := max(blockWidth(width)-2, 1) // marker + one space
	wrapped := contentStyle.Width(innerWidth).Render(content)
	marker := lipgloss.NewStyle().Foreground(markerColor)

	lines := strings.Split(wrapped, "\n")
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
		return renderBoxedBlock(block.Text, lipgloss.NewStyle(), markerFirst, textBlockColor, width)
	case ThinkingBlock:
		// The same block the assistant's answer gets, dimmed and in italics:
		// reasoning is part of the transcript but is not the answer.
		style := lipgloss.NewStyle().Italic(true).Foreground(thinkingColor)
		return renderBoxedBlock(block.Thinking, style, markerFirst, textBlockColor, width)
	case ToolUseBlock:
		return renderBoxedBlock(block.Name+string(block.Input), lipgloss.NewStyle(), markerFirst, toolUseColor, width)
	default:
		return ""
	}
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
