package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// eventBuffer is the capacity of the Event channel returned by Run. It
// decouples how fast we produce events from how fast the caller renders them,
// without letting an unread stream grow without bound.
// TODO: re-consider using a buffered vs unbuffered channel
const eventBuffer = 64

type Tool struct {
	Name             string
	Description      string
	InputSchema      InputSchema
	RequiresApproval bool
	Execute          func(ctx context.Context, input json.RawMessage) (string, error)
}

// Property is the typified struct of the JSON Schema for the Tool Schema's Property type
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// InputSchema is the struct matching the JSON Schema for the Tool Input
type InputSchema struct {
	Type       string `json:"type" default:"object"`
	Properties map[string]Property
	Required   []string // Names of required properties TODO: Track in Property struct instead?
}

type Agent struct {
	toolsMap      map[string]Tool
	tools         []Tool
	mu            sync.Mutex
	contextWindow []Message
	maxTokens     int64
	provider      Provider
}

func (a *Agent) useTool(ctx context.Context, name string, input json.RawMessage) (string, error) {
	tool := a.toolsMap[name]
	return tool.Execute(ctx, input)
}

// Run runs a full turn in a goroutine: inference, tool execution, and follow-up inference,
// repeating until the model stops asking for tools. It returns immediately with
// a channel of Events describing progress; the channel is closed when the turn
// ends, whether it completed, failed, or ctx was cancelled.
//
// Run owns the agent loop so that every caller — TUI, headless CLI, tests —
// only has to render Events rather than reimplement the loop.
func (a *Agent) Run(ctx context.Context, userInput string) <-chan Event {
	events := make(chan Event, eventBuffer)

	mark := a.beginTurn(NewUserMessage([]Block{TextBlock{Text: userInput}}))

	go func() {
		defer close(events)
		// Registered after close, so it runs first and the ErrorEvent is sent
		// while the channel is still open. Without this a panic anywhere in the
		// loop — most plausibly inside a tool's Execute — takes down the whole
		// program from a goroutine the TUI cannot recover on.
		defer func() {
			if r := recover(); r != nil {
				a.rollback(mark)
				send(ctx, events, ErrorEvent{Err: fmt.Errorf("panic during turn: %v", r)})
			}
		}()

		for {
			response, ok := a.infer(ctx, events)
			if !ok {
				a.rollback(mark)
				return
			}

			a.AppendMessage(response.Message)
			if !send(ctx, events, MessageEvent{Message: response.Message}) {
				a.rollback(mark)
				return
			}

			if response.StopReason != StopReasonToolUse {
				return
			}

			results := a.runTools(ctx, response)
			toolMessage := NewUserMessage(results)
			a.AppendMessage(toolMessage)
			if !send(ctx, events, MessageEvent{Message: toolMessage}) {
				a.rollback(mark)
				return
			}
		}
	}()

	return events
}

// infer runs one round of inference, forwarding Events to the caller and
// returning the assembled Response. ok is false if the turn should stop, which
// covers provider errors and cancellation.
func (a *Agent) infer(ctx context.Context, events chan<- Event) (Response, bool) {
	a.mu.Lock()
	req := Request{
		MaxTokens: a.maxTokens, // TODO: set dynamically from API query if not set explicitly
		Tools:     a.tools,
		Messages:  a.contextWindow,
	}
	a.mu.Unlock()

	for event := range a.provider.Stream(ctx, req) {
		switch event := event.(type) {
		case ResponseEvent:
			// Terminal, and not forwarded: the caller sees the Message once it
			// has actually been appended, as a MessageEvent.
			return event.Response, true
		case ErrorEvent:
			send(ctx, events, event)
			return Response{}, false
		default:
			if !send(ctx, events, event) {
				return Response{}, false
			}
		}
	}

	return Response{}, false
}

// runTools executes every ToolUseBlock in the response, returning the matching
// ToolResultBlocks
func (a *Agent) runTools(ctx context.Context, response Response) []Block {
	var results []Block
	for _, block := range response.Message.Content {
		toolUse, ok := block.(ToolUseBlock)
		if !ok {
			continue
		}
		result, err := a.useTool(ctx, toolUse.Name, toolUse.Input)
		if err != nil {
			result = err.Error()
		}
		results = append(results, NewToolResultBlock(toolUse.ID, result, err != nil))
	}
	return results
}

// send either sends the event to the channel or receives a ctx.Done
func send(ctx context.Context, events chan<- Event, ev Event) bool {
	select {
	case events <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// Models lists the models the provider offers
func (a *Agent) Models(ctx context.Context) ([]Model, error) {
	return a.provider.Models(ctx)
}

// SetModel switches models and drops the conversation so far, which was
// produced by the previous model and does not carry over.
func (a *Agent) SetModel(id string) {
	a.provider.SetModel(id)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.contextWindow = []Message{}
}

func (a *Agent) AppendMessage(msg Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contextWindow = append(a.contextWindow, msg)
}

// beginTurn appends the turn's opening message and returns a mark: the length
// of the context window before it. Appending and reading the length happen
// under one lock, so the mark can never point past a message another turn added.
func (a *Agent) beginTurn(msg Message) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	mark := len(a.contextWindow)
	a.contextWindow = append(a.contextWindow, msg)
	return mark
}

// rollback discards everything a failed turn added, back to mark.
//
// A turn that stops partway leaves the context window in a shape the API
// rejects: a prompt with no reply would make the next turn send two user
// messages in a row, and a tool_use block with no matching tool_result is
// unsendable outright. Either way every later turn fails too, so a failed turn
// undoes itself rather than poisoning the conversation.
// A mark past the end of the window is not a bug: SetModel clears the window
// while a turn is still running, and there is then nothing left to undo.
func (a *Agent) rollback(mark int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contextWindow = a.contextWindow[:min(mark, len(a.contextWindow))]
}

// New returns a pointer because Agent holds a Mutex and must not be copied
func New(provider Provider, tools []Tool) *Agent {
	// TODO: Use functional options

	toolMap := map[string]Tool{}
	for _, tool := range tools {
		toolMap[tool.Name] = tool
	}

	return &Agent{
		maxTokens:     8092,
		toolsMap:      toolMap,
		tools:         tools,
		contextWindow: []Message{},
		provider:      provider,
	}
}
