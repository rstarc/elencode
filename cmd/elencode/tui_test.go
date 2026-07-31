package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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

type errStub struct{}

func (errStub) Error() string { return "stub failure" }
