package agent

import (
	"encoding/json"
	"fmt"
)

// Block is a part of a message
// By definining it as an interface with an unexported function, we emulate a sum type in Go
type Block interface{ block() }

func renderBlock(block Block) string {
	render := ""
	switch block := block.(type) {
	case TextBlock:
		render = render + fmt.Sprintln(block.Text)
	case ToolUseBlock:
		render = render + fmt.Sprintln("[ %w", block.Name)
		render = render + fmt.Sprintln(" $ %w", string(block.Input))
		render = render + fmt.Sprintln("]")
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
