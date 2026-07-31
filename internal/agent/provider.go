package agent

import "context"

type Provider interface {
	// Stream starts a turn and returns a channel of incremental Events.
	// The turn is terminated by exactly one ResponseEvent or ErrorEvent.
	Stream(ctx context.Context, req Request) <-chan Event
}

// Event is an incremental update emitted while a turn is being processed.
// By defining it as an interface with an unexported function, we emulate a sum type in Go
type Event interface{ event() }

// TextDeltaEvent carries a fragment of assistant text as it is generated
type TextDeltaEvent struct{ Text string }

func (e TextDeltaEvent) event() {}

// ResponseEvent carries the assembled Response. Final event of a successful
// Provider.Stream. Consumed by Agent.Run, which turns it into a MessageEvent;
// it does not reach Run's caller.
type ResponseEvent struct{ Response Response }

func (e ResponseEvent) event() {}

// MessageEvent reports that a Message was appended to the context window,
// meaning the transcript changed and any in-flight text has landed
type MessageEvent struct{ Message Message }

func (e MessageEvent) event() {}

// ErrorEvent carries a failure. Final event of a failed turn.
type ErrorEvent struct{ Err error }

func (e ErrorEvent) event() {}

// Request represents a single Request we send to the provider's API
type Request struct {
	MaxTokens int64
	Tools     []Tool
	Messages  []Message
}

// Response represents the Response we receive from a provider's API
type Response struct {
	Message    Message    // Message with Role Assistant
	StopReason StopReason // Why inference has stopped
}

type StopReason string

const (
	StopReasonEndTurn      StopReason = "end_turn"
	StopReasonMaxTokens    StopReason = "max_tokens"
	StopReasonStopSequence StopReason = "stop_sequence"
	StopReasonToolUse      StopReason = "tool_use"
	StopReasonPauseTurn    StopReason = "pause_turn"
	StopReasonRefusal      StopReason = "refusal"
)
