package agent

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

func renderMessage(msg Message, width int) string {
	render := ""
	for _, block := range msg.Content {
		render = render + renderBlock(block, width) + "\n"
	}
	return render
}

// RenderTranscript returns a rendered string that contains the entire context
// window, laid out for a terminal width columns wide.
// Uses a Mutex to guard the contextWindow
func RenderTranscript(agent *Agent, width int) string {
	agent.mu.Lock()
	defer agent.mu.Unlock()

	result := ""
	for _, msg := range agent.contextWindow {
		result = result + renderMessage(msg, width) + "\n"
	}
	return result
}
