package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
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
	Type       string `json:"type"`
	Properties map[string]Property
	Required   []string // Names of required properties TODO: Track in Property struct instead?
}

type Agent struct {
	toolsMap          map[string]Tool
	tools             []Tool
	mu                sync.Mutex
	contextWindow     []Message
	contextGeneration int
	// model and the provider serving it are one decision and are written
	// together: a model is offered by exactly one API, and asking another for
	// it is a request nobody can answer.
	model     Model
	provider  Provider
	maxTokens int64
}

type turnMark struct {
	index      int
	generation int
	model      Model
	provider   Provider
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
			response, ok := a.infer(ctx, events, mark)
			if !ok {
				return
			}

			// A response cut off mid tool call carries a tool_use whose argument
			// JSON never finished. It is never executed, so nothing will answer
			// it, and both APIs reject a tool_use with no matching result:
			// keeping it would fail every later turn, not just this one.
			if response.StopReason == StopReasonMaxTokens {
				response.Message.Content = withoutToolUse(response.Message.Content)
			}

			// A message left with no content at all is unsendable in its own
			// right, so it is dropped rather than recorded.
			if len(response.Message.Content) > 0 {
				if !a.appendMessage(mark.generation, response.Message) {
					return
				}
				if !send(ctx, events, MessageEvent{Message: response.Message}) {
					a.rollback(mark)
					return
				}
			}

			if response.StopReason != StopReasonToolUse {
				return
			}

			results := a.runTools(ctx, response)
			toolMessage := NewUserMessage(results)
			if !a.appendMessage(mark.generation, toolMessage) {
				return
			}
			if !send(ctx, events, MessageEvent{Message: toolMessage}) {
				a.rollback(mark)
				return
			}
		}
	}()

	return events
}

// Retry bounds. A rate limit usually clears in seconds, so the aim is to ride
// out a short one, not to wait out a spent quota: five attempts spend at most
// half a minute before the turn reports and hands control back to the user.
const (
	maxAttempts  = 5
	initialDelay = 2 * time.Second
	maxDelay     = 30 * time.Second
)

// infer runs one round of inference, retrying while the provider says the
// failure was transient. ok is false if the turn should stop, which covers
// cancellation and any error the retries did not clear.
func (a *Agent) infer(ctx context.Context, events chan<- Event, mark turnMark) (Response, bool) {
	for attempt := 1; ; attempt++ {
		response, err, ok := a.inferOnce(ctx, events, mark)
		if ok {
			return response, true
		}
		// No error means the consumer went away rather than the request failing
		if err == nil {
			return Response{}, false
		}

		var retryable *RetryableError
		if !errors.As(err, &retryable) {
			// A permanent failure — an oversized prompt, most plainly — would
			// reject the same window on every later turn, so the turn is undone
			// rather than left there. Retries running out is the other case and
			// keeps everything: the API was busy, the conversation is fine.
			a.rollback(mark)
			send(ctx, events, ErrorEvent{Err: err})
			return Response{}, false
		}
		if attempt == maxAttempts {
			send(ctx, events, ErrorEvent{Err: err})
			return Response{}, false
		}

		delay := backoff(attempt, retryable.After)
		if !send(ctx, events, RetryEvent{Attempt: attempt, Of: maxAttempts, In: delay, Err: err}) {
			return Response{}, false
		}
		if !sleep(ctx, delay) {
			return Response{}, false
		}
	}
}

// inferOnce runs a single request, forwarding Events to the caller and
// returning the assembled Response. A failure is returned rather than emitted,
// so infer can decide whether the caller ever needs to hear about it.
func (a *Agent) inferOnce(ctx context.Context, events chan<- Event, mark turnMark) (Response, error, bool) {
	// Both come off the mark rather than off the agent: a turn belongs to the
	// model it began on, and to whoever serves that model. Reading them live
	// would send this turn's half-finished conversation to whatever the user
	// switched to while it was in flight.
	if mark.provider == nil {
		return Response{}, errors.New("no model selected"), false
	}

	a.mu.Lock()
	req := Request{
		Model:     mark.model,
		MaxTokens: a.maxTokens, // TODO: set dynamically from API query if not set explicitly
		Tools:     a.tools,
		Messages:  a.contextWindow,
	}
	a.mu.Unlock()

	for event := range mark.provider.Stream(ctx, req) {
		switch event := event.(type) {
		case ResponseEvent:
			// Terminal, and not forwarded: the caller sees the Message once it
			// has actually been appended, as a MessageEvent.
			return event.Response, nil, true
		case ErrorEvent:
			return Response{}, event.Err, false
		default:
			if !send(ctx, events, event) {
				return Response{}, nil, false
			}
		}
	}

	return Response{}, nil, false
}

// backoff is how long to wait before the next attempt. after is the provider's
// own hint and wins when it gave one, but is capped like everything else: an
// outsized Retry-After would otherwise park the turn for hours with no way out
// but ctrl+c.
func backoff(attempt int, after time.Duration) time.Duration {
	if after > 0 {
		return min(after, maxDelay)
	}
	// Shifting rather than multiplying keeps an absurd attempt count from
	// overflowing into a negative delay; the cap makes the result the same.
	if attempt > 8 {
		return maxDelay
	}
	return min(initialDelay<<(attempt-1), maxDelay)
}

// sleep waits out the backoff, reporting false if the turn was abandoned first.
// Without the ctx arm a ctrl+c during a 30 second wait would appear to do
// nothing until the wait ended.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// withoutToolUse returns blocks with every tool call removed, leaving the rest
// in the order the model produced them.
func withoutToolUse(blocks []Block) []Block {
	kept := make([]Block, 0, len(blocks))
	for _, block := range blocks {
		if _, ok := block.(ToolUseBlock); !ok {
			kept = append(kept, block)
		}
	}
	return kept
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

// Models lists the models the current provider offers
func (a *Agent) Models(ctx context.Context) ([]Model, error) {
	a.mu.Lock()
	provider := a.provider
	a.mu.Unlock()
	return provider.Models(ctx)
}

// SetModel switches models and drops the conversation so far, which was
// produced by the previous model and does not carry over.
//
// The provider serving model comes with it: the caller holds the clients, so
// it is the only one that can say which of them answers for this model.
func (a *Agent) SetModel(model Model, provider Provider) {
	a.mu.Lock()
	a.contextGeneration++
	a.contextWindow = []Message{}
	a.model = model
	a.provider = provider
	a.mu.Unlock()
}

func (a *Agent) AppendMessage(msg Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contextWindow = append(a.contextWindow, msg)
}

// beginTurn appends the turn's opening message and returns its context mark.
// Appending and reading the length happen under one lock, so the mark can never
// point past a message another turn added.
func (a *Agent) beginTurn(msg Message) turnMark {
	a.mu.Lock()
	defer a.mu.Unlock()
	mark := turnMark{
		index:      len(a.contextWindow),
		generation: a.contextGeneration,
		model:      a.model,
		provider:   a.provider,
	}
	a.contextWindow = append(a.contextWindow, msg)
	return mark
}

func (a *Agent) appendMessage(generation int, msg Message) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if generation != a.contextGeneration {
		return false
	}
	a.contextWindow = append(a.contextWindow, msg)
	return true
}

// rollback discards everything a turn added, back to mark.
//
// Called when the turn abandons itself mid-round — a panic, or a consumer that
// stopped reading — which is the only way the window is left holding a tool_use
// block with no matching tool_result. That shape is unsendable, so every later
// turn would fail too.
//
// Also called when inference fails permanently, where what is unsendable is the
// content rather than the shape: the same window would be rejected again on
// every later turn. A transient failure needs no such undoing — both APIs
// combine consecutive user messages, so an unanswered prompt simply stays in
// the conversation and the next turn carries on from it.
//
// A stale mark is ignored: SetModel clears the window while a turn is still
// running, and its later cleanup must not affect the new model's context.
func (a *Agent) rollback(mark turnMark) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if mark.generation != a.contextGeneration {
		return
	}
	a.contextWindow = a.contextWindow[:min(mark.index, len(a.contextWindow))]
}

// New returns a pointer because Agent holds a Mutex and must not be copied.
// The agent has no model, and so nobody to ask, until SetModel says.
func New(tools []Tool) *Agent {
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
	}
}
