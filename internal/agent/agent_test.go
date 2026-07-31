package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

// collectTimeout bounds how long a turn may take before we call it hung. Every
// test provider responds immediately, so this only fires on a real bug.
const collectTimeout = 2 * time.Second

// scriptedProvider replays a fixed set of Events per round of inference: one
// entry in turns per round. It records the Requests it was given so tests can
// assert on what the agent sent.
type scriptedProvider struct {
	turns    [][]Event
	calls    int
	requests []Request
}

func (p *scriptedProvider) Stream(ctx context.Context, req Request) <-chan Event {
	p.requests = append(p.requests, req)

	if p.calls >= len(p.turns) {
		// Surface as an ErrorEvent rather than panicking: a panic here would
		// happen on the agent's goroutine and take down the test binary.
		events := make(chan Event, 1)
		events <- ErrorEvent{Err: errors.New("provider asked for an unscripted round of inference")}
		close(events)
		return events
	}

	turn := p.turns[p.calls]
	p.calls++

	events := make(chan Event, len(turn))
	for _, event := range turn {
		events <- event
	}
	close(events)
	return events
}

// blockingProvider stands in for a slow API call: it produces no terminal event
// and only returns once ctx is cancelled.
type blockingProvider struct{ started chan struct{} }

func (p *blockingProvider) Stream(ctx context.Context, req Request) <-chan Event {
	events := make(chan Event)
	go func() {
		defer close(events)
		close(p.started)
		<-ctx.Done()
	}()
	return events
}

// collect drains events until the channel closes, failing if the turn hangs
func collect(t *testing.T, events <-chan Event) []Event {
	t.Helper()

	var got []Event
	timeout := time.After(collectTimeout)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, event)
		case <-timeout:
			t.Fatalf("turn did not end within %s, got %d events so far", collectTimeout, len(got))
			return got
		}
	}
}

func assistantMessage(blocks ...Block) Message {
	return Message{Role: RoleAssistant, Content: blocks}
}

func TestRunEmitsDeltasThenMessage(t *testing.T) {
	assistant := assistantMessage(TextBlock{Text: "Hello"})
	provider := &scriptedProvider{turns: [][]Event{{
		TextDeltaEvent{Text: "Hel"},
		TextDeltaEvent{Text: "lo"},
		ResponseEvent{Response: Response{Message: assistant, StopReason: StopReasonEndTurn}},
	}}}
	a := New(provider, nil)

	got := collect(t, a.Run(context.Background(), "hi"))

	// The ResponseEvent is consumed by Run; callers see the message only once
	// it is actually in the transcript, as a MessageEvent.
	want := []Event{
		TextDeltaEvent{Text: "Hel"},
		TextDeltaEvent{Text: "lo"},
		MessageEvent{Message: assistant},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events =\n\t%#v\nwant\n\t%#v", got, want)
	}

	wantWindow := []Message{
		NewUserMessage([]Block{TextBlock{Text: "hi"}}),
		assistant,
	}
	if !reflect.DeepEqual(a.contextWindow, wantWindow) {
		t.Errorf("context window =\n\t%#v\nwant\n\t%#v", a.contextWindow, wantWindow)
	}
}

func TestRunExecutesToolsAndInfersAgain(t *testing.T) {
	toolUse := ToolUseBlock{ID: "toolu_1", Name: "read", Input: json.RawMessage(`{"path":"a.txt"}`)}
	final := assistantMessage(TextBlock{Text: "done"})
	provider := &scriptedProvider{turns: [][]Event{
		{ResponseEvent{Response: Response{
			Message:    assistantMessage(toolUse),
			StopReason: StopReasonToolUse,
		}}},
		{ResponseEvent{Response: Response{Message: final, StopReason: StopReasonEndTurn}}},
	}}

	var toolInput json.RawMessage
	read := Tool{
		Name: "read",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			toolInput = input
			return "file contents", nil
		},
	}
	a := New(provider, []Tool{read})

	collect(t, a.Run(context.Background(), "read a.txt"))

	// Fatal, not Error: every assertion below assumes both rounds happened
	if provider.calls != 2 {
		t.Fatalf("rounds of inference = %d, want 2 (tool results must trigger a second round)", provider.calls)
	}
	if string(toolInput) != `{"path":"a.txt"}` {
		t.Errorf("tool input = %s, want {\"path\":\"a.txt\"}", toolInput)
	}

	wantWindow := []Message{
		NewUserMessage([]Block{TextBlock{Text: "read a.txt"}}),
		assistantMessage(toolUse),
		NewUserMessage([]Block{NewToolResultBlock("toolu_1", "file contents", false)}),
		final,
	}
	if !reflect.DeepEqual(a.contextWindow, wantWindow) {
		t.Errorf("context window =\n\t%#v\nwant\n\t%#v", a.contextWindow, wantWindow)
	}

	// The second round must see the tool result, not just the original prompt
	if got := len(provider.requests[1].Messages); got != 3 {
		t.Errorf("second round saw %d messages, want 3 (prompt, tool_use, tool_result)", got)
	}
}

func TestRunRecordsToolFailureAsErrorResult(t *testing.T) {
	toolUse := ToolUseBlock{ID: "toolu_1", Name: "read", Input: json.RawMessage(`{}`)}
	provider := &scriptedProvider{turns: [][]Event{
		{ResponseEvent{Response: Response{
			Message:    assistantMessage(toolUse),
			StopReason: StopReasonToolUse,
		}}},
		{ResponseEvent{Response: Response{
			Message:    assistantMessage(TextBlock{Text: "sorry"}),
			StopReason: StopReasonEndTurn,
		}}},
	}}
	read := Tool{
		Name: "read",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "", errors.New("no such file")
		},
	}
	a := New(provider, []Tool{read})

	collect(t, a.Run(context.Background(), "read a.txt"))

	// The model needs to be told what went wrong, so the error text becomes the
	// result content rather than the tool's empty return value.
	want := NewToolResultBlock("toolu_1", "no such file", true)
	got := a.contextWindow[2].Content[0]
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tool result = %#v, want %#v", got, want)
	}
}

func TestRunForwardsProviderError(t *testing.T) {
	wantErr := errors.New("api exploded")
	provider := &scriptedProvider{turns: [][]Event{{ErrorEvent{Err: wantErr}}}}
	a := New(provider, nil)

	got := collect(t, a.Run(context.Background(), "hi"))

	want := []Event{ErrorEvent{Err: wantErr}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %#v, want %#v", got, want)
	}
	// A failed turn is rolled back whole. Leaving the prompt behind would make
	// the next turn send two user messages in a row, which the API rejects.
	if len(a.contextWindow) != 0 {
		t.Errorf("context window = %#v, want it rolled back to empty", a.contextWindow)
	}
}

func TestRunRollsBackFailedToolRound(t *testing.T) {
	toolUse := ToolUseBlock{ID: "toolu_1", Name: "read", Input: json.RawMessage(`{}`)}
	provider := &scriptedProvider{turns: [][]Event{
		{ResponseEvent{Response: Response{
			Message:    assistantMessage(toolUse),
			StopReason: StopReasonToolUse,
		}}},
		{ErrorEvent{Err: errors.New("api exploded")}},
	}}
	read := Tool{
		Name: "read",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "file contents", nil
		},
	}
	a := New(provider, []Tool{read})

	collect(t, a.Run(context.Background(), "read a.txt"))

	// The rollback must reach past the tool round too: a tool_use block left in
	// the window with no matching tool_result is permanently unsendable.
	if len(a.contextWindow) != 0 {
		t.Errorf("context window = %#v, want it rolled back to empty", a.contextWindow)
	}
}

func TestRunRollsBackOnlyItsOwnTurn(t *testing.T) {
	assistant := assistantMessage(TextBlock{Text: "hello"})
	provider := &scriptedProvider{turns: [][]Event{
		{ResponseEvent{Response: Response{Message: assistant, StopReason: StopReasonEndTurn}}},
		{ErrorEvent{Err: errors.New("api exploded")}},
	}}
	a := New(provider, nil)

	collect(t, a.Run(context.Background(), "hi"))
	collect(t, a.Run(context.Background(), "hi again"))

	// The first turn succeeded, so it survives; only the second is undone.
	wantWindow := []Message{
		NewUserMessage([]Block{TextBlock{Text: "hi"}}),
		assistant,
	}
	if !reflect.DeepEqual(a.contextWindow, wantWindow) {
		t.Errorf("context window =\n\t%#v\nwant\n\t%#v", a.contextWindow, wantWindow)
	}
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	provider := &blockingProvider{started: make(chan struct{})}
	a := New(provider, nil)

	ctx, cancel := context.WithCancel(context.Background())
	events := a.Run(ctx, "hi")

	<-provider.started
	cancel()

	// collect returning at all is the assertion: a Run that ignored
	// cancellation would leave the channel open and time out here.
	if got := collect(t, events); len(got) != 0 {
		t.Errorf("events = %#v, want none after cancellation", got)
	}
}
