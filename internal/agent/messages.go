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

// renderMessage renders msg's blocks, dropping any that render empty (such
// as a ToolResultBlock, whose raw output is not shown) so they don't leave a
// blank line behind.
func renderMessage(msg Message, width int) string {
	var blocks []string
	for _, block := range msg.Content {
		if rendered := renderBlock(block, msg.Role, width); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	return strings.Join(blocks, "\n")
}

// RenderTranscript returns a rendered string that contains the entire context
// window, laid out for a terminal width columns wide. Messages are separated
// by a single newline, not a blank line, to keep the transcript dense. A
// message that renders empty (e.g. one made only of tool results) is
// dropped entirely rather than leaving a gap.
// Uses a Mutex to guard the contextWindow
func RenderTranscript(agent *Agent, width int) string {
	agent.mu.Lock()
	defer agent.mu.Unlock()

	var messages []string
	for _, msg := range agent.contextWindow {
		if rendered := renderMessage(msg, width); rendered != "" {
			messages = append(messages, rendered)
		}
	}
	if len(messages) == 0 {
		return ""
	}
	return strings.Join(messages, "\n") + "\n"
}
