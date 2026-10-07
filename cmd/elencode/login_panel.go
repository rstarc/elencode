package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/tui/menu"
)

// loginPanel is what is shown while a login waits on the user: c copies the
// link, esc gives up, and the panel says what the last key did. Shared by the
// session and `elencode connect` on a terminal, so both answer the same keys
// the same way.
type loginPanel struct {
	page   string // what c copies: the page the latest prompt asked to open
	copied bool   // the confirmation is up
	// copies counts the copies made, so the timer of an earlier one cannot
	// take a later one's confirmation down early.
	copies int
}

// copiedFor is how long the confirmation stays up before the hint comes back,
// so c can be pressed again knowing it will do something.
const copiedFor = 3 * time.Second

// copiedColor sets the confirmation apart from the dim hints around it.
var copiedColor = lipgloss.BrightGreen

// copiedExpiredMsg takes down the confirmation of the copies-th copy.
type copiedExpiredMsg struct{ copies int }

// show takes in a prompt. Only one with a page changes what c copies, and a
// new page has not been copied yet.
func (p loginPanel) show(prompt loginPrompt) loginPanel {
	if prompt.page != "" {
		p.page = prompt.page
		p.copied = false
	}
	return p
}

// press handles a key while the login waits: c copies the page, esc or
// ctrl+c gives the login up, which cancel reports for the caller to do.
// Everything else is swallowed: while it waits, the login has the keyboard.
func (p loginPanel) press(msg tea.KeyPressMsg, clip clipboard) (next loginPanel, cmd tea.Cmd, cancel bool) {
	switch msg.String() {
	case "c":
		if p.page == "" {
			return p, nil, false
		}
		return p, clip.copy(p.page), false
	case "esc", "ctrl+c":
		return p, nil, true
	}
	return p, nil, false
}

// done takes in that the copy went through, and puts the confirmation up
// until copiedFor has passed.
func (p loginPanel) done(copiedMsg) (loginPanel, tea.Cmd) {
	p.copied = true
	p.copies++
	copies := p.copies
	return p, tea.Tick(copiedFor, func(time.Time) tea.Msg { return copiedExpiredMsg{copies: copies} })
}

// expire takes the confirmation down, unless a later copy has put up its own.
func (p loginPanel) expire(msg copiedExpiredMsg) loginPanel {
	if msg.copies == p.copies {
		p.copied = false
	}
	return p
}

// view is the panel's one row: dim hints, with the confirmation in colour
// while it is up.
func (p loginPanel) view() string {
	dim := lipgloss.NewStyle().Foreground(menu.DescriptionColor)
	row := dim.Render("waiting for the ChatGPT sign-in · ")
	switch {
	case p.copied:
		row += lipgloss.NewStyle().Foreground(copiedColor).Render("copied to clipboard!") + dim.Render(" · ")
	case p.page != "":
		row += dim.Render("c to copy the link · ")
	}
	return row + dim.Render("esc to cancel")
}
