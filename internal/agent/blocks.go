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

const (
	// maxBlockWidth caps how wide a block grows on a large terminal, since long
	// lines are hard to read.
	maxBlockWidth = 90
	// minBlockWidth is the narrowest box worth drawing. Below roughly five
	// columns lipgloss ignores the requested width and renders at the content's
	// natural width, which is exactly the clipping we are avoiding, so anything
	// narrower than this is clamped up and allowed to overflow instead.
	minBlockWidth = 20
)

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
var toolUseColor = lipgloss.BrightBlue
var errorColor = lipgloss.Red

// userBackground and userForeground give a user message a light gray
// background, like a chat bubble, to set it apart from the assistant's.
var userBackground = lipgloss.White
var userForeground = lipgloss.Black

// blockWidth fits a block to the space available, which includes the marker
// column. available is 0 before the first WindowSizeMsg arrives.
func blockWidth(available int) int {
	return min(max(available, minBlockWidth), maxBlockWidth)
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
	style := lipgloss.NewStyle().Foreground(noticeColor)

	total := blockWidth(width)
	label := " " + text + " "

	fill := total - lipgloss.Width(label)
	if fill < 2 {
		// No room for a rule on both sides; the text alone still reads as a
		// notice, since nothing else in the transcript is unmarked.
		return style.MaxWidth(total).Render(label)
	}

	left := fill / 2
	return style.Render(strings.Repeat(noticeRule, left) + label + strings.Repeat(noticeRule, fill-left))
}

// noticeRule is the character the notice line is drawn with
const noticeRule = "─"

var noticeColor = lipgloss.BrightBlack

type TextBlock struct{ Text string }

func (b TextBlock) block() {}

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
