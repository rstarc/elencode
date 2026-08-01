package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderErrorIsLabelledAndBoxed(t *testing.T) {
	got := RenderError(errors.New("429 rate_limit_error"))

	if !strings.Contains(got, "429 rate_limit_error") {
		t.Errorf("render = %q, want it to contain the error text", got)
	}
	// Labelled, so a failure is not mistaken for assistant output
	if !strings.Contains(got, "Error:") {
		t.Errorf("render = %q, want it to be labelled %q", got, "Error:")
	}
	// Boxed like every other block, so it reads as part of the transcript
	if !strings.Contains(got, "│") {
		t.Errorf("render = %q, want it drawn in a border", got)
	}
}
