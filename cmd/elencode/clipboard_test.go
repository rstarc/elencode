package main

import (
	"errors"
	"strings"
	"testing"
)

// fakeClipboard is the system clipboard as a variable the test reads.
type fakeClipboard struct {
	text string
	err  error
}

func (f *fakeClipboard) write(text string) error {
	if f.err != nil {
		return f.err
	}
	f.text = text
	return nil
}

// Locally the system's tool is the one to trust: it says whether it worked.
func TestCopyTextUsesTheSystemToolLocally(t *testing.T) {
	system := &fakeClipboard{}
	c := clipboard{write: system.write}

	msgs := run(t, c.copy("https://example.com/page"))

	find[copiedMsg](t, msgs)
	if system.text != "https://example.com/page" {
		t.Errorf("the system clipboard got %q", system.text)
	}
	if len(msgs) != 1 {
		t.Errorf("messages = %v, want only the result: the terminal need not be asked", msgs)
	}
}

// Over SSH the system's tool would fill the remote machine's clipboard, which
// the user's browser cannot reach: the terminal is asked instead, with OSC 52.
func TestCopyTextAsksTheTerminalOverSSH(t *testing.T) {
	system := &fakeClipboard{}
	c := clipboard{remote: true, write: system.write}

	msgs := run(t, c.copy("https://example.com/page"))

	find[copiedMsg](t, msgs)
	if !strings.Contains(text(msgs), "https://example.com/page") {
		t.Errorf("messages %q do not ask the terminal for the text", text(msgs))
	}
	if system.text != "" {
		t.Error("filled the remote machine's clipboard")
	}
}

// With no tool to run, asking the terminal is still worth a try.
func TestCopyTextFallsBackToTheTerminalWithoutATool(t *testing.T) {
	system := &fakeClipboard{err: errors.New("no clipboard utilities available")}
	c := clipboard{write: system.write}

	msgs := run(t, c.copy("https://example.com/page"))

	find[copiedMsg](t, msgs)
	if !strings.Contains(text(msgs), "https://example.com/page") {
		t.Errorf("messages %q do not ask the terminal for the text", text(msgs))
	}
}
