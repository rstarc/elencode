package main

import (
	"context"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
)

type uiState int

const (
	uiStateIdle       uiState = iota // Agent not active
	uiStateProcessing                // Waiting for Response
)

type model struct {
	// agent state
	agent *agent.Agent
	// in-flight turn, valid only while state is uiStateProcessing.
	// Both are handles to this turn specifically, not to the agent.
	events <-chan agent.Event
	cancel context.CancelFunc
	// TUI state
	partial  string          // assistant text streamed so far, not yet in the transcript
	viewport viewport.Model  // viewport displaying the transcript
	input    textinput.Model // user input field
	state    uiState
	err      error
}

func newModel(agent *agent.Agent) model {

	input := textinput.New()
	input.Placeholder = "start typing..."
	input.SetVirtualCursor(false)
	input.Focus()
	input.Prompt = "> "
	input.CharLimit = 0

	viewport := viewport.New()
	viewport.SetContent("== elencode ==")

	// Only up down scrolling
	viewport.KeyMap.Left.SetEnabled(false)
	viewport.KeyMap.Right.SetEnabled(false)

	return model{
		agent:    agent,
		viewport: viewport,
		input:    input,
		state:    uiStateIdle,
	}
}

// streamEventMsg carries one Event from the in-flight turn
type streamEventMsg struct{ event agent.Event }

// streamClosedMsg reports that the turn ended and the channel is drained
type streamClosedMsg struct{}

// waitForEvent receives one Event from the in-flight turn. Update re-issues it
// after each event, since a tea.Cmd delivers exactly one message. The channel is captured
// by value, so a command left over from a finished turn reads that turn's
// closed channel rather than stealing an event from the next one.
func waitForEvent(events <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return streamClosedMsg{}
		}
		return streamEventMsg{event}
	}
}

// startTurn hands userInput to the agent and begins receiving its Events
func (m model) startTurn(userInput string) (model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = m.agent.Run(ctx, userInput)
	m.partial = ""
	m.state = uiStateProcessing
	m.err = nil
	m.refresh()
	return m, waitForEvent(m.events)
}

// endTurn releases the finished turn's resources and returns to idle
func (m model) endTurn() model {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.events = nil
	m.partial = ""
	m.state = uiStateIdle
	m.refresh()
	return m
}

// refresh repaints the viewport from the transcript plus any in-flight text
func (m *model) refresh() {
	content := agent.RenderTranscript(m.agent)
	if m.partial != "" {
		content = content + agent.RenderStreamingText(m.partial) + "\n"
	}
	if m.err != nil {
		content = content + m.err.Error() + "\n"
	}
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// Init implements the bubbletea Model interface
func (m model) Init() tea.Cmd {
	return textinput.Blink
}

// Update implements the bubbletea Model interface
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// TODO: Resize window
		m.viewport.SetWidth(msg.Width)
		m.input.SetWidth(msg.Width)
		// TODO: correctly compute height of text input
		m.viewport.SetHeight(msg.Height - 1)
		m.viewport.GotoBottom()
		return m, nil
	case tea.KeyPressMsg:
		// handle user input
		switch msg.String() {
		case "ctrl+c":
			// Abandon any in-flight turn so its goroutine unblocks instead of
			// waiting on a channel nobody will read again
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "enter":
			// only actually do anything if we are not currently waiting and there is actual input
			if m.state == uiStateIdle && m.input.Value() != "" {
				userInput := m.input.Value()
				m.input.Reset()
				return m.startTurn(userInput)
			}
		default:
			// Send other keypresses to text input model
			// TODO: Forward to viewport as well
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	case streamEventMsg:
		switch event := msg.event.(type) {
		case agent.TextDeltaEvent:
			m.partial = m.partial + event.Text
		case agent.MessageEvent:
			// The message is already in the transcript, so drop the partial
			// copy we were painting to avoid rendering it twice
			m.partial = ""
		case agent.ErrorEvent:
			m.err = event.Err
		}
		m.refresh()
		// Wait for the next event. The turn ends when the channel closes, not
		// here: a MessageEvent may be followed by tool results and more inference.
		return m, waitForEvent(m.events)
	case streamClosedMsg:
		return m.endTurn(), nil

	case cursor.BlinkMsg:
		// Forward to textinput
		m.input, _ = m.input.Update(msg)
		return m, nil
	default:
	}

	return m, textinput.Blink
}

// View implements the bubbletea Model interface
func (m model) View() tea.View {
	// TODO: Add throbber/spinner during processing state
	viewportView := m.viewport.View()
	inputView := m.input.View()

	cursor := m.input.Cursor()
	// assemble view
	view := tea.NewView(lipgloss.JoinVertical(lipgloss.Top, viewportView, inputView))
	view.AltScreen = false
	view.Cursor = cursor
	return view
}
