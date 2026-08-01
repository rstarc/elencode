package agent

import (
	"charm.land/lipgloss/v2"
	"encoding/json"
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

var textBlockStyle = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.BrightBlack).Padding(1, 1)

var toolUseStyle = textBlockStyle.BorderForeground(lipgloss.BrightBlue)

var errorStyle = textBlockStyle.BorderForeground(lipgloss.Red)

// blockWidth fits a block to the space available, which lipgloss measures
// including the border and padding. available is 0 before the first
// WindowSizeMsg arrives.
func blockWidth(available int) int {
	return min(max(available, minBlockWidth), maxBlockWidth)
}

func renderBlock(block Block, width int) string {
	render := ""
	switch block := block.(type) {
	case TextBlock:
		render = render + block.Text
		render = textBlockStyle.Width(blockWidth(width)).Render(render)
	case ToolUseBlock:
		render = render + block.Name + string(block.Input)
		render = toolUseStyle.Width(blockWidth(width)).Render(render)
	default:
	}
	return render
}

// RenderStreamingText renders in-progress assistant text
func RenderStreamingText(text string, width int) string {
	return renderBlock(TextBlock{Text: text}, width)
}

// RenderError renders a failed turn. It is boxed like a block so it reads as
// part of the transcript, but red and labelled so it is not mistaken for
// something the assistant said.
func RenderError(err error, width int) string {
	return errorStyle.Width(blockWidth(width)).Render("Error: " + err.Error())
}

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
