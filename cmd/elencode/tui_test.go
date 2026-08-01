package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/rstarc/elencode/internal/agent"
)

// newTestModel builds a model backed by an agent that is never Run, so it needs
// no provider: only the transcript is read during rendering.
func newTestModel() model {
	return newModel(agent.New(nil, nil))
}

// update applies msg and asserts the result is still our model type
func update(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()

	next, _ := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return got
}

func TestUpdateAccumulatesTextDeltas(t *testing.T) {
	m := newTestModel()

	m = update(t, m, streamEventMsg{agent.TextDeltaEvent{Text: "Hel"}})
	m = update(t, m, streamEventMsg{agent.TextDeltaEvent{Text: "lo"}})

	if m.partial != "Hello" {
		t.Errorf("partial = %q, want %q", m.partial, "Hello")
	}
}

func TestUpdateClearsPartialOnMessageEvent(t *testing.T) {
	m := newTestModel()
	m = update(t, m, streamEventMsg{agent.TextDeltaEvent{Text: "Hello"}})

	// Once the message is in the transcript, the partial copy must go, or the
	// same text renders twice.
	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "Hello"}}}
	m = update(t, m, streamEventMsg{agent.MessageEvent{Message: landed}})

	if m.partial != "" {
		t.Errorf("partial = %q, want it cleared once the message landed", m.partial)
	}
}

func TestUpdateReturnsToIdleWhenStreamCloses(t *testing.T) {
	m := newTestModel()
	m.state = uiStateProcessing
	m.partial = "half a sentence"

	m = update(t, m, streamClosedMsg{})

	if m.state != uiStateIdle {
		t.Errorf("state = %v, want uiStateIdle", m.state)
	}
	if m.partial != "" {
		t.Errorf("partial = %q, want it cleared when the turn ends", m.partial)
	}
}

func TestUpdateRecordsStreamError(t *testing.T) {
	m := newTestModel()

	wantErr := errStub{}
	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: wantErr}})

	if m.err != wantErr {
		t.Errorf("err = %v, want %v", m.err, wantErr)
	}
}

func TestUpdateShowsErrorInViewport(t *testing.T) {
	m := newTestModel()
	// Wide enough that the boxed error is not truncated to the viewport width
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: errStub{}}})

	if view := m.viewport.View(); !strings.Contains(view, "Error: stub failure") {
		t.Errorf("viewport does not show the labelled error:\n%s", view)
	}
}

func TestUpdateKeepsErrorVisibleAfterTurnEnds(t *testing.T) {
	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: errStub{}}})

	// The channel closes right after a failure, so clearing the error here
	// would make it flash and vanish before it could be read.
	m = update(t, m, streamClosedMsg{})

	if m.err == nil {
		t.Fatal("err was cleared when the turn ended, want it kept until the next turn")
	}
	if view := m.viewport.View(); !strings.Contains(view, "Error: stub failure") {
		t.Errorf("viewport does not show the labelled error:\n%s", view)
	}
}

func TestUpdateKeepsBannerBeforeAnythingIsSaid(t *testing.T) {
	m := newTestModel()

	// The first WindowSizeMsg arrives at startup and repaints, so an empty
	// transcript must not blank the screen.
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})

	if !strings.Contains(m.viewport.View(), banner) {
		t.Errorf("viewport = %q, want it to still show the banner", m.viewport.View())
	}
}

// TestUpdateShowsUserMessageAsSoonAsItIsSent asserts the message is in the
// viewport the instant Enter is handled, before any Event has been read off
// the turn's channel: agent.Run appends the user message to the context
// window synchronously, ahead of the goroutine that talks to the provider.
func TestUpdateShowsUserMessageAsSoonAsItIsSent(t *testing.T) {
	m := newModel(agent.New(failingProvider{err: errors.New("boom")}, nil))
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.input.SetValue("hello there")

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if view := m.viewport.View(); !strings.Contains(view, "hello there") {
		t.Errorf("viewport does not show the user's message right after Enter:\n%s", view)
	}
}

func TestUpdateFitsErrorToNarrowTerminal(t *testing.T) {
	const width = 40

	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: width, Height: 20})
	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: errStub{}}})

	assertFitsWidth(t, m.viewport.View(), width)
}

func TestUpdateReflowsWhenTerminalShrinks(t *testing.T) {
	const narrow = 40

	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: errStub{}}})

	// Content laid out for the old width would be clipped at the new one, so a
	// resize has to repaint rather than only resize the viewport.
	m = update(t, m, tea.WindowSizeMsg{Width: narrow, Height: 20})

	assertFitsWidth(t, m.viewport.View(), narrow)
	if !strings.Contains(m.viewport.View(), "stub failure") {
		t.Errorf("viewport lost the error after resizing:\n%s", m.viewport.View())
	}
}

func TestViewPutsCursorOnTheInputRow(t *testing.T) {
	const height = 20

	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: height})

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("view has no cursor, want one on the input row")
	}
	// textinput reports its cursor relative to itself, but View stacks the input
	// below the viewport, so the row has to be offset or the terminal blinks at
	// the top of the screen instead.
	wantY := lipgloss.Height(m.viewport.View())
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (the input row)", view.Cursor.Y, wantY)
	}
}

func TestViewShowsSpinnerWhileProcessing(t *testing.T) {
	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.state = uiStateProcessing

	if view := m.View().Content; !strings.Contains(view, "processing") {
		t.Errorf("view does not show the processing spinner:\n%s", view)
	}
}

func TestViewHidesSpinnerWhenIdle(t *testing.T) {
	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})

	if view := m.View().Content; strings.Contains(view, "processing") {
		t.Errorf("view shows the processing spinner while idle:\n%s", view)
	}
}

// TestUpdateKeepsViewportUsableInTinyTerminal covers terminals too short to
// hold the rows below the viewport: subtracting them leaves nothing, and a
// viewport sized zero or less has no transcript area at all.
func TestUpdateKeepsViewportUsableInTinyTerminal(t *testing.T) {
	for _, height := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("height%d", height), func(t *testing.T) {
			m := newTestModel()
			m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: height})

			if got := m.viewport.Height(); got < 1 {
				t.Errorf("viewport height = %d, want at least 1 row of transcript", got)
			}
		})
	}
}

// TestViewFitsTerminalHeightWhileProcessing guards the spinner row: it makes
// the view one row taller than the terminal, and the inline renderer drops the
// top row of an oversized frame — which right after Enter is the user's own
// message, so it only reappears once the turn ends and the spinner goes away.
func TestViewFitsTerminalHeightWhileProcessing(t *testing.T) {
	const height = 20

	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: height})
	m.state = uiStateProcessing

	if got := lipgloss.Height(m.View().Content); got > height {
		t.Errorf("view is %d rows tall, want <= %d", got, height)
	}
}

func TestViewPutsCursorBelowSpinnerWhileProcessing(t *testing.T) {
	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.state = uiStateProcessing

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("view has no cursor, want one on the input row")
	}
	wantY := lipgloss.Height(m.viewport.View()) + lipgloss.Height(m.spinnerLine())
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (below viewport and spinner)", view.Cursor.Y, wantY)
	}
}

// failingProvider fails every round of inference, so a turn driven through the
// real program reaches the error path.
type failingProvider struct{ err error }

func (p failingProvider) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, 1)
	events <- agent.ErrorEvent{Err: p.err}
	close(events)
	return events
}

// TestProgramFitsErrorToTerminal drives the whole program — real event loop,
// real terminal size, real keystrokes — rather than calling Update directly, so
// it also covers View assembling the viewport and input together. That seam is
// what made the input row overflow the terminal and drag every other row with
// it, which testing Update alone did not reach.
func TestProgramFitsErrorToTerminal(t *testing.T) {
	// 20 is the narrowest box worth drawing; below that the width is clamped up
	// and overflow is accepted, so it is not a case this can assert on.
	for _, width := range []int{20, 30, 40, 80, 120} {
		t.Run(fmt.Sprintf("width%d", width), func(t *testing.T) {
			a := agent.New(failingProvider{err: errors.New("429 rate_limit_error: too many requests, slow down")}, nil)
			tm := teatest.NewTestModel(t, newModel(a), teatest.WithInitialTermSize(width, 20))

			tm.Type("hi")
			tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

			teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
				return bytes.Contains(out, []byte("Error:"))
			})

			if err := tm.Quit(); err != nil {
				t.Fatalf("quitting the program: %v", err)
			}
			final, ok := tm.FinalModel(t).(model)
			if !ok {
				t.Fatalf("final model is %T, want model", tm.FinalModel(t))
			}

			view := final.View().Content
			if !strings.Contains(view, "rate_limit_error") {
				t.Errorf("view does not show the error:\n%s", view)
			}
			assertFitsWidth(t, view, width)
		})
	}
}

// assertFitsWidth fails if any line would be clipped by a terminal this wide
func assertFitsWidth(t *testing.T, view string, width int) {
	t.Helper()

	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("line %d is %d columns wide, want <= %d:\n%s", i, got, width, line)
		}
	}
}

type errStub struct{}

func (errStub) Error() string { return "stub failure" }
