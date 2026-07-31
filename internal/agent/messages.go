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

func renderMessage(msg Message) string {
	render := ""
	for _, block := range msg.Content {
		render = render + renderBlock(block) + "\n"
	}
	return render
}

// RenderTranscript returns a rendered string that contains the entire context window
func RenderTranscript(agent *Agent) string {
	result := ""
	for _, msg := range agent.contextWindow {
		result = result + renderMessage(msg) + "\n"
	}
	return result
}
