package agent

import (
	"charm.land/lipgloss/v2"
	"encoding/json"
)

// Block is a part of a message
// By definining it as an interface with an unexported function, we emulate a sum type in Go
type Block interface{ block() }

var textBlockStyle = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.BrightBlack).Padding(1, 1).Width(90)

var toolUseStyle = textBlockStyle.BorderForeground(lipgloss.BrightBlue)

func renderBlock(block Block) string {
	render := ""
	switch block := block.(type) {
	case TextBlock:
		render = render + block.Text
		render = textBlockStyle.Render(render)
	case ToolUseBlock:
		render = render + block.Name + string(block.Input)
		render = toolUseStyle.Render(render)
	default:
	}
	return render
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
