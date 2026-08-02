package agent

import "encoding/json"

// Block is a part of a message
// By definining it as an interface with an unexported function, we emulate a sum type in Go
type Block interface{ block() }

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
