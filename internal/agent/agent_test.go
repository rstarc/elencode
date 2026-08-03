package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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
	models   []Model // what Models returns
}

func (p *scriptedProvider) Models(ctx context.Context) ([]Model, error) { return p.models, nil }

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

func (p *blockingProvider) Models(ctx context.Context) ([]Model, error) { return nil, nil }

func (p *blockingProvider) Stream(ctx context.Context, req Request) <-chan Event {
	events := make(chan Event)
	go func() {
		defer close(events)
		close(p.started)
		<-ctx.Done()
	}()
	return events
}

// switchProvider blocks the first stream and answers later streams immediately,
// so a model switch can be placed between an old and a new turn deterministically.
type switchProvider struct {
	started  chan struct{}
	release  chan struct{}
	calls    int
	requests []Request
	response Response
}

func (p *switchProvider) Models(ctx context.Context) ([]Model, error) { return nil, nil }

func (p *switchProvider) Stream(ctx context.Context, req Request) <-chan Event {
	call := p.calls
	p.calls++
	p.requests = append(p.requests, req)
	events := make(chan Event, 1)
	if call == 0 {
		go func() {
			close(p.started)
			select {
			case <-p.release:
				events <- ResponseEvent{Response: p.response}
			case <-ctx.Done():
			}
			close(events)
		}()
		return events
	}
	events <- ResponseEvent{Response: p.response}
	close(events)
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
	// An error that is not retryable is tried once and no more
	if provider.calls != 1 {
		t.Errorf("rounds of inference = %d, want 1", provider.calls)
	}
	// The prompt survives: inference only ever fails at a point where the window
	// is still sendable, so there is nothing to undo and the user keeps their turn.
	wantWindow := []Message{NewUserMessage([]Block{TextBlock{Text: "hi"}})}
	if !reflect.DeepEqual(a.contextWindow, wantWindow) {
		t.Errorf("context window =\n\t%#v\nwant\n\t%#v", a.contextWindow, wantWindow)
	}
}

// TestRunKeepsCompletedRoundsWhenALaterRoundFails is the point of the
// round-level rollback: a failure deep into a turn must not throw away the tool
// work that already succeeded.
func TestRunKeepsCompletedRoundsWhenALaterRoundFails(t *testing.T) {
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

	wantWindow := []Message{
		NewUserMessage([]Block{TextBlock{Text: "read a.txt"}}),
		assistantMessage(toolUse),
		NewUserMessage([]Block{NewToolResultBlock("toolu_1", "file contents", false)}),
	}
	if !reflect.DeepEqual(a.contextWindow, wantWindow) {
		t.Errorf("context window =\n\t%#v\nwant\n\t%#v", a.contextWindow, wantWindow)
	}
}

func TestRunRetriesRetryableErrors(t *testing.T) {
	assistant := assistantMessage(TextBlock{Text: "hello"})
	provider := &scriptedProvider{turns: [][]Event{
		{ErrorEvent{Err: &RetryableError{Err: errors.New("rate limited"), After: time.Millisecond}}},
		{ErrorEvent{Err: &RetryableError{Err: errors.New("rate limited"), After: time.Millisecond}}},
		{ResponseEvent{Response: Response{Message: assistant, StopReason: StopReasonEndTurn}}},
	}}
	a := New(provider, nil)

	got := collect(t, a.Run(context.Background(), "hi"))

	if provider.calls != 3 {
		t.Fatalf("rounds of inference = %d, want 3 (two retries then success)", provider.calls)
	}

	var retries []RetryEvent
	for _, event := range got {
		if retry, ok := event.(RetryEvent); ok {
			retries = append(retries, retry)
		}
		if err, ok := event.(ErrorEvent); ok {
			t.Errorf("turn reported %v, want the retry to have recovered it", err.Err)
		}
	}
	if len(retries) != 2 {
		t.Fatalf("retry events = %d, want 2", len(retries))
	}
	if retries[0].Attempt != 1 || retries[1].Attempt != 2 {
		t.Errorf("retry attempts = %d, %d, want 1, 2", retries[0].Attempt, retries[1].Attempt)
	}
	if retries[0].Of != maxAttempts {
		t.Errorf("retry Of = %d, want %d", retries[0].Of, maxAttempts)
	}

	// The turn carries on as if nothing happened
	wantWindow := []Message{NewUserMessage([]Block{TextBlock{Text: "hi"}}), assistant}
	if !reflect.DeepEqual(a.contextWindow, wantWindow) {
		t.Errorf("context window =\n\t%#v\nwant\n\t%#v", a.contextWindow, wantWindow)
	}
}

func TestRunGivesUpAfterMaxAttempts(t *testing.T) {
	round := []Event{ErrorEvent{Err: &RetryableError{Err: errors.New("rate limited"), After: time.Millisecond}}}
	turns := make([][]Event, maxAttempts)
	for i := range turns {
		turns[i] = round
	}
	provider := &scriptedProvider{turns: turns}
	a := New(provider, nil)

	got := collect(t, a.Run(context.Background(), "hi"))

	if provider.calls != maxAttempts {
		t.Fatalf("rounds of inference = %d, want %d", provider.calls, maxAttempts)
	}
	// The last attempt reports rather than announcing a retry that never comes
	last, ok := got[len(got)-1].(ErrorEvent)
	if !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", got[len(got)-1])
	}
	if !strings.Contains(last.Err.Error(), "rate limited") {
		t.Errorf("error = %q, want it to carry the provider's message", last.Err)
	}
}

// TestRunAbandonsARetryWhenCancelled covers ctrl+c during the backoff: the wait
// must be interruptible, or the UI stays stuck for the length of the delay.
func TestRunAbandonsARetryWhenCancelled(t *testing.T) {
	provider := &scriptedProvider{turns: [][]Event{
		{ErrorEvent{Err: &RetryableError{Err: errors.New("rate limited"), After: time.Hour}}},
	}}
	a := New(provider, nil)

	ctx, cancel := context.WithCancel(context.Background())
	events := a.Run(ctx, "hi")

	// Drain until the retry is announced, which means the sleep has begun
	for event := range events {
		if _, ok := event.(RetryEvent); ok {
			break
		}
	}
	cancel()

	// collect returning at all is the assertion: a sleep that ignored ctx would
	// hold the turn open for an hour and time out here.
	collect(t, events)
}

func TestBackoffGrowsAndStaysCapped(t *testing.T) {
	tests := []struct {
		attempt int
		after   time.Duration
		want    time.Duration
	}{
		{attempt: 1, want: initialDelay},
		{attempt: 2, want: 2 * initialDelay},
		{attempt: 3, want: 4 * initialDelay},
		// Doubling would overshoot; the cap holds it
		{attempt: 9, want: maxDelay},
		// A hint from the provider wins, but is capped too: an outsized
		// Retry-After would otherwise park the turn for hours.
		{attempt: 1, after: 5 * time.Second, want: 5 * time.Second},
		{attempt: 1, after: time.Hour, want: maxDelay},
	}

	for _, test := range tests {
		if got := backoff(test.attempt, test.after); got != test.want {
			t.Errorf("backoff(%d, %s) = %s, want %s", test.attempt, test.after, got, test.want)
		}
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

	// The first turn survives untouched, and the second keeps its prompt
	wantWindow := []Message{
		NewUserMessage([]Block{TextBlock{Text: "hi"}}),
		assistant,
		NewUserMessage([]Block{TextBlock{Text: "hi again"}}),
	}
	if !reflect.DeepEqual(a.contextWindow, wantWindow) {
		t.Errorf("context window =\n\t%#v\nwant\n\t%#v", a.contextWindow, wantWindow)
	}
}

func TestRunRecoversFromToolPanic(t *testing.T) {
	toolUse := ToolUseBlock{ID: "toolu_1", Name: "read", Input: json.RawMessage(`{}`)}
	provider := &scriptedProvider{turns: [][]Event{
		{ResponseEvent{Response: Response{
			Message:    assistantMessage(toolUse),
			StopReason: StopReasonToolUse,
		}}},
	}}
	read := Tool{
		Name: "read",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			panic("tool blew up")
		},
	}
	a := New(provider, []Tool{read})

	// Run's work happens on its own goroutine, so an unrecovered panic here
	// takes down the whole process rather than failing this test.
	got := collect(t, a.Run(context.Background(), "read a.txt"))

	if len(got) == 0 {
		t.Fatal("no events, want the panic reported as an ErrorEvent")
	}
	last, ok := got[len(got)-1].(ErrorEvent)
	if !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", got[len(got)-1])
	}
	if !strings.Contains(last.Err.Error(), "tool blew up") {
		t.Errorf("error = %q, want it to mention the panic value", last.Err)
	}
	if len(a.contextWindow) != 0 {
		t.Errorf("context window = %#v, want it rolled back to empty", a.contextWindow)
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

func TestModelsComeFromTheProvider(t *testing.T) {
	provider := &scriptedProvider{models: []Model{{ID: "a", DisplayName: "A"}}}
	a := New(provider, nil)

	got, err := a.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}

	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("Models() = %v, want the provider's list", got)
	}
}

func TestRunCarriesTheSelectedModelInItsRequest(t *testing.T) {
	provider := &scriptedProvider{turns: [][]Event{{
		ResponseEvent{Response: Response{Message: assistantMessage(TextBlock{Text: "Hello"}), StopReason: StopReasonEndTurn}},
	}}}
	a := New(provider, nil)
	a.SetModel(Model{ID: "model-a", Thinking: ThinkingAdaptive})

	collect(t, a.Run(context.Background(), "hi"))

	if got := provider.requests[0].Model; got.ID != "model-a" || got.Thinking != ThinkingAdaptive {
		t.Errorf("request model = %#v, want model-a with adaptive thinking", got)
	}
}

// TestSetModelClearsTheContextWindow covers the point of switching models: the
// conversation so far was produced by another model, and is not carried over.
func TestSetModelClearsTheContextWindow(t *testing.T) {
	provider := &scriptedProvider{turns: [][]Event{{
		ResponseEvent{Response: Response{Message: assistantMessage(TextBlock{Text: "Hello"}), StopReason: StopReasonEndTurn}},
	}}}
	a := New(provider, nil)
	collect(t, a.Run(context.Background(), "hi"))
	if len(a.contextWindow) == 0 {
		t.Fatal("context window is empty before switching models, nothing to clear")
	}

	a.SetModel(Model{ID: "b"})

	if len(a.contextWindow) != 0 {
		t.Errorf("context window has %d messages after switching models, want it cleared", len(a.contextWindow))
	}
	if a.model.ID != "b" {
		t.Errorf("agent model = %q, want %q", a.model.ID, "b")
	}
}

func TestOldTurnCannotAppendAfterModelSwitch(t *testing.T) {
	provider := &switchProvider{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		response: Response{Message: assistantMessage(TextBlock{Text: "old"}), StopReason: StopReasonEndTurn},
	}
	a := New(provider, nil)
	a.SetModel(Model{ID: "a"})

	events := a.Run(context.Background(), "old prompt")
	<-provider.started
	a.SetModel(Model{ID: "b"})
	close(provider.release)
	collect(t, events)

	if len(a.contextWindow) != 0 {
		t.Errorf("context window = %#v, want stale response discarded after model switch", a.contextWindow)
	}
	if got := provider.requests[0].Model.ID; got != "a" {
		t.Errorf("old turn request model = %q, want a", got)
	}
}

func TestOldTurnCannotRollbackNewTurnAfterModelSwitch(t *testing.T) {
	provider := &switchProvider{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		response: Response{Message: assistantMessage(TextBlock{Text: "new"}), StopReason: StopReasonEndTurn},
	}
	a := New(provider, nil)

	oldContext, cancelOld := context.WithCancel(context.Background())
	oldEvents := a.Run(oldContext, "old prompt")
	<-provider.started
	a.SetModel(Model{ID: "b"})

	collect(t, a.Run(context.Background(), "new prompt"))
	cancelOld()
	collect(t, oldEvents)

	if len(a.contextWindow) != 2 {
		t.Errorf("context window = %#v, want the completed new turn to survive stale rollback", a.contextWindow)
	}
}

// TestRollbackSurvivesAModelSwitch covers switching models mid-turn: the turn
// rolls back to a mark taken before the window was cleared, which would
// otherwise be a slice bound past the end of it.
func TestRollbackSurvivesAModelSwitch(t *testing.T) {
	provider := &blockingProvider{started: make(chan struct{})}
	a := New(provider, nil)
	a.AppendMessage(NewUserMessage([]Block{TextBlock{Text: "an earlier turn"}}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := a.Run(ctx, "hi")
	<-provider.started

	a.SetModel(Model{ID: "b"})
	cancel()

	for _, event := range collect(t, events) {
		if err, ok := event.(ErrorEvent); ok {
			t.Errorf("turn ended with %v, want it to unwind cleanly", err.Err)
		}
	}
}
