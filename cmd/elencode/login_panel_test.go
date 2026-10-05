package main

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// copiedPanel is a panel showing a page that has just been copied, with the
// command that takes the confirmation back down.
func copiedPanel(t *testing.T) (loginPanel, *copiedExpiredMsg) {
	t.Helper()
	p := loginPanel{}.show(openByHand("https://auth.example/oauth/authorize?x=1"))
	p, cmd := p.done(copiedMsg{})
	if cmd == nil {
		t.Fatal("copying set no timer to take the confirmation down")
	}
	return p, &copiedExpiredMsg{copies: p.copies}
}

// The confirmation stands out from the hints around it.
func TestCopiedToClipboardIsInItsOwnColour(t *testing.T) {
	p, _ := copiedPanel(t)

	want := lipgloss.NewStyle().Foreground(copiedColor).Render("copied to clipboard!")
	if view := p.view(); !strings.Contains(view, want) {
		t.Errorf("view = %q, want the confirmation in its colour", view)
	}
}

// A few seconds on, the hint is back, so c can be pressed again knowingly.
func TestCopyHintComesBackAfterTheConfirmation(t *testing.T) {
	p, expired := copiedPanel(t)

	p = p.expire(*expired)

	if view := p.view(); strings.Contains(view, "copied") || !strings.Contains(view, "c to copy the link") {
		t.Errorf("view = %q, want the hint back", view)
	}
}

// Pressed twice, the first copy's timer must not cut the second one's
// confirmation short.
func TestAnEarlierTimerLeavesALaterConfirmationUp(t *testing.T) {
	p, first := copiedPanel(t)
	p, _ = p.done(copiedMsg{})

	p = p.expire(*first)

	if view := p.view(); !strings.Contains(view, "copied to clipboard!") {
		t.Errorf("view = %q, want the second confirmation still up", view)
	}
}

func TestCopyConfirmationLastsAFewSeconds(t *testing.T) {
	if copiedFor.Seconds() < 2 || copiedFor.Seconds() > 5 {
		t.Errorf("copiedFor = %s, want a few seconds", copiedFor)
	}
}
