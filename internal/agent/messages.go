package agent

import "strings"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// Message is the fundamental unit that the API uses.
// A message has a single role tag consists of multiple structured blocks
type Message struct {
	Role    Role
	Content []Block
}

func NewUserMessage(content []Block) Message {
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}

// RenderMessage renders msg's blocks, dropping any that render empty (such
// as a ToolResultBlock, whose raw output is not shown) so they don't leave a
// blank line behind.
func RenderMessage(msg Message, width int) string {
	var blocks []string
	for _, block := range msg.Content {
		if rendered := RenderBlock(block, msg.Role, width); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	return strings.Join(blocks, "\n")
}
