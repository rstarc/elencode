package agent

import (
	"context"
	"time"
)

type Provider interface {
	// Stream starts a turn and returns a channel of incremental Events.
	// The turn is terminated by exactly one ResponseEvent or ErrorEvent.
	Stream(ctx context.Context, req Request) <-chan Event
}

// Effort is how hard an effort-based model is asked to reason. Providers clamp
// it to the levels their own API accepts, and send nothing at all for the zero
// value, which leaves the level to the API.
type Effort string

const (
	EffortNone   Effort = ""
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// Efforts are the levels that can be asked for, weakest first. EffortNone is
// not among them: it is the absence of a choice, not a level.
var Efforts = []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// ParseEffort reads a configured level, reporting whether it names one. The
// empty string is EffortNone rather than a failure: it is how "let the API
// decide" is written. This is the only place a level is recognised, so a
// caller reading one from a file does not need its own copy of the vocabulary.
func ParseEffort(s string) (Effort, bool) {
	if s == "" {
		return EffortNone, true
	}
	for _, effort := range Efforts {
		if string(effort) == s {
			return effort, true
		}
	}
	return EffortNone, false
}

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

// RetryEvent reports that a round of inference failed with something transient
// and is about to be tried again. Unlike ErrorEvent it is not terminal: the turn
// is still alive, and the UI shows this so a backoff does not look like a hang.
type RetryEvent struct {
	Attempt int // which attempt just failed, from 1
	Of      int // how many attempts the agent will make in total
	In      time.Duration
	Err     error
}

func (e RetryEvent) event() {}

// RetryableError marks a failure the same request could survive, such as a rate
// limit or a server error. Providers wrap what they emit, since deciding this
// needs the vendor's error types; the agent only asks whether the mark is there.
//
// After carries the provider's own hint, from a Retry-After header or the like.
// Zero means it gave none and the agent should fall back to its own backoff.
type RetryableError struct {
	Err   error
	After time.Duration
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

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
