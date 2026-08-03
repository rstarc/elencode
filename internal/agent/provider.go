package agent

import "context"

type Provider interface {
	// Stream starts a turn and returns a channel of incremental Events.
	// The turn is terminated by exactly one ResponseEvent or ErrorEvent.
	Stream(ctx context.Context, req Request) <-chan Event
	// Models lists the models this provider can be pointed at. It talks to the
	// API, so it blocks and takes a ctx.
	Models(ctx context.Context) ([]Model, error)
}

// Model is one model the provider offers, as shown in the picker
type Model struct {
	ID          string
	DisplayName string
	Thinking    ThinkingMode
}

// ThinkingMode is how a model can be asked to reason, if at all. Models differ,
// and asking for the wrong kind is rejected outright rather than ignored, so
// the request has to be built from what the model actually accepts.
type ThinkingMode string

const (
	// ThinkingNone: the model cannot be asked to reason
	ThinkingNone ThinkingMode = ""
	// ThinkingAdaptive: the model decides for itself how much to reason
	ThinkingAdaptive ThinkingMode = "adaptive"
	// ThinkingBudgeted: the model reasons within a token budget the caller sets
	ThinkingBudgeted ThinkingMode = "budgeted"
	// ThinkingEffort: the model reasons at a discrete effort level the caller
	// picks (OpenAI reasoning_effort; Anthropic OutputConfig.Effort).
	ThinkingEffort ThinkingMode = "effort"
)

// Effort is how hard an effort-based model is asked to reason. Providers clamp
// it to the levels their own API accepts and treat the zero value as their
// default, medium.
type Effort string

const (
	EffortNone   Effort = ""
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// Event is an incremental update emitted while a turn is being processed.
// By defining it as an interface with an unexported function, we emulate a sum type in Go
type Event interface{ event() }

// TextDeltaEvent carries a fragment of assistant text as it is generated
type TextDeltaEvent struct{ Text string }

func (e TextDeltaEvent) event() {}

// ThinkingDeltaEvent carries a fragment of the model's reasoning as it is
// generated. Separate from TextDeltaEvent because the two are rendered
// differently and can follow one another within a single turn.
type ThinkingDeltaEvent struct{ Text string }

func (e ThinkingDeltaEvent) event() {}

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
	Model     Model
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
