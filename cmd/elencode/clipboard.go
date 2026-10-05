package main

import (
	"os"

	tea "charm.land/bubbletea/v2"
	systemclipboard "github.com/atotto/clipboard"
)

// copiedMsg reports that the text was copied, or that the terminal was asked
// to: it does not answer, so the two cannot be told apart.
type copiedMsg struct{}

// clipboard copies text with the system's own tool where elencode runs on the
// user's machine, and over SSH, where that tool would fill the remote
// machine's clipboard instead, by asking the terminal with OSC 52. Fields so tests can stand in for both.
type clipboard struct {
	remote bool // an SSH session
	write  func(text string) error
}

// systemClipboard is the clipboard of the machine elencode runs on.
func systemClipboard() clipboard {
	return clipboard{remote: overSSH(os.Getenv), write: systemclipboard.WriteAll}
}

// copy puts text on the clipboard and reports when it is done. With no tool
// to run, asking the terminal is still worth a try.
func (c clipboard) copy(text string) tea.Cmd {
	asked := tea.Sequence(tea.SetClipboard(text), func() tea.Msg { return copiedMsg{} })
	if c.remote {
		return asked
	}
	return func() tea.Msg {
		if err := c.write(text); err != nil {
			return asked()
		}
		return copiedMsg{}
	}
}
