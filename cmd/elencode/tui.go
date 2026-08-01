package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
)

// banner is shown until there is a transcript to show instead
const banner = "== elencode =="

// inputPrompt mirrors agent.UserPromptMarker, which marks user messages in
// the transcript, so the two stay visibly the same character. Declared at
// package level (not inside newModel) because its *agent.Agent parameter
// shadows the agent package name.
const inputPrompt = agent.UserPromptMarker

type uiState int

const (
	uiStateIdle       uiState = iota // Agent not active
	uiStateProcessing                // Waiting for Response
)

type model struct {
	// agent state
	agent  *agent.Agent
	config config.Config // shown by /config, never otherwise read here
	// in-flight turn, valid only while state is uiStateProcessing.
	// Both are handles to this turn specifically, not to the agent.
	events <-chan agent.Event
	cancel context.CancelFunc
	// TUI state
	partial  string          // assistant text streamed so far, not yet in the transcript
	viewport viewport.Model  // viewport displaying the transcript
	input    textinput.Model // user input field
	spinner  spinner.Model   // shown above input while state is uiStateProcessing
	width    int             // terminal width, 0 until the first WindowSizeMsg
	height   int             // terminal height, 0 until the first WindowSizeMsg
	state    uiState
	err      error
	// command menu state. Visibility is derived from the input rather than
	// stored, so the two cannot disagree; see menuVisible.
	menuDismissed bool       // Esc hides the menu for the rest of this command line
	menu          list.Model // command rows; filtered from the prompt, not its own input
	// configVisible replaces the whole frame with the read-only config view
	configVisible bool
	// quit confirmation. quitGeneration counts armings so a disarm message left
	// over from an earlier one cannot disarm the current one.
	quitArmed      bool
	quitGeneration int
}

// quitDisarmMsg withdraws the exit confirmation armed by generation
type quitDisarmMsg struct{ generation int }

// quitConfirmWindow is how long the exit confirmation stays armed
const quitConfirmWindow = 2 * time.Second

// armQuit asks for a second ctrl+c and schedules the confirmation to lapse
func (m model) armQuit() (model, tea.Cmd) {
	m.quitArmed = true
	m.quitGeneration++
	m.resize()

	generation := m.quitGeneration
	return m, tea.Tick(quitConfirmWindow, func(time.Time) tea.Msg {
		return quitDisarmMsg{generation: generation}
	})
}

// quitHint tells the user how to finish exiting, or "" when nothing is pending
func (m model) quitHint() string {
	if !m.quitArmed {
		return ""
	}
	return lipgloss.NewStyle().Foreground(menuDescriptionColor).Render("press ctrl+c again to exit")
}

// menuVisible reports whether the command menu is showing
func (m model) menuVisible() bool {
	return !m.menuDismissed && strings.HasPrefix(m.input.Value(), commandPrefix)
}

// menuView renders the command menu, or "" while it is hidden
func (m model) menuView() string {
	if !m.menuVisible() {
		return ""
	}
	return m.menu.View()
}

// syncMenu re-filters the menu from the prompt and sizes it to the rows that
// survived, so it grows and shrinks with the query instead of holding a fixed
// block of blank lines.
func (m *model) syncMenu() {
	m.menu.SetFilterText(menuQuery(m.input.Value()))
	m.menu.SetWidth(m.width)
	m.menu.SetHeight(max(len(m.menu.VisibleItems()), 1))
}

func newModel(agent *agent.Agent, cfg config.Config) model {

	input := textinput.New()
	input.Placeholder = "start typing..."
	input.SetVirtualCursor(false)
	input.Focus()
	input.Prompt = inputPrompt + " "
	input.CharLimit = 0

	viewport := viewport.New()
	viewport.SetContent(banner)

	// Only up down scrolling
	viewport.KeyMap.Left.SetEnabled(false)
	viewport.KeyMap.Right.SetEnabled(false)

	return model{
		agent:    agent,
		config:   cfg,
		menu:     newCommandMenu(),
		viewport: viewport,
		input:    input,
		spinner:  spinner.New(spinner.WithSpinner(spinner.Ellipsis)),
		state:    uiStateIdle,
	}
}

// spinnerLine renders the "processing..." indicator shown above the input
// while a turn is in flight.
func (m model) spinnerLine() string {
	return "processing" + m.spinner.View()
}

// chromeHeight is the number of rows View stacks below the viewport, measured
// rather than assumed so the two cannot drift apart.
//
// The spinner row counts even while idle. Reserving it keeps the frame the
// same height when a turn starts: the inline renderer drops the top row of a
// frame taller than the terminal, and right after Enter that row is the line
// the user just sent.
func (m model) chromeHeight() int {
	height := lipgloss.Height(m.spinnerLine()) + lipgloss.Height(m.input.View())
	if menu := m.menuView(); menu != "" {
		height += lipgloss.Height(menu)
	}
	if hint := m.quitHint(); hint != "" {
		height += lipgloss.Height(hint)
	}
	return height
}

// resize refits the viewport to whatever chrome currently sits below it. The
// menu opens and closes between WindowSizeMsgs, so the height it leaves behind
// has to be recomputed on those keystrokes too, not only on a real resize.
func (m *model) resize() {
	m.viewport.SetHeight(max(m.height-m.chromeHeight(), 1))
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

// forwardToInput hands a keypress to the text input and settles the menu around
// the edit it made.
func (m model) forwardToInput(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)

	// Re-filter for the edit just made. SetFilterText resets the selection to the
	// top, so the highlight cannot be left pointing at a command that no longer
	// matches.
	m.syncMenu()
	// Esc dismisses the menu for one command line only; leaving the line clears it.
	if !strings.HasPrefix(m.input.Value(), commandPrefix) {
		m.menuDismissed = false
	}
	m.resize()
	return m, cmd
}

// runCommand handles Enter on a command line: it runs the command the input
// names exactly, or reports that there is no such command.
func (m model) runCommand() (model, tea.Cmd) {
	cmd, ok := lookupCommand(m.input.Value())
	if !ok {
		m.err = fmt.Errorf("unknown command: %s", m.input.Value())
		m.input.Reset()
		m.menuDismissed = false
		m.resize()
		m.refresh()
		return m, nil
	}

	switch cmd.name {
	case "config":
		m.configVisible = true
		m.input.Reset()
		m.resize()
		return m, nil
	case "quit":
		// Abandon any in-flight turn, as ctrl+c does, so its goroutine unblocks
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	default:
		m.err = fmt.Errorf("command not implemented: %s", cmd.name)
		m.input.Reset()
		m.resize()
		m.refresh()
		return m, nil
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
	return m, tea.Batch(waitForEvent(m.events), m.spinner.Tick)
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
	content := agent.RenderTranscript(m.agent, m.width)
	if m.partial != "" {
		content = content + agent.RenderStreamingText(m.partial, m.width) + "\n"
	}
	if m.err != nil {
		content = content + agent.RenderError(m.err, m.width) + "\n"
	}
	if content == "" {
		content = banner
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
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.SetWidth(msg.Width)
		m.menu.SetWidth(msg.Width)
		// textinput draws the prompt before, and a cursor cell after, the width
		// it is given, so passing the terminal width makes the input row wider
		// than the terminal. JoinVertical then pads every other row out to
		// match, pushing the whole view past the right edge.
		m.input.SetWidth(max(msg.Width-lipgloss.Width(m.input.Prompt)-1, 1))
		// Measured after SetWidth above, since the input's height depends on
		// the width it was given. A terminal too short to hold even one row of
		// transcript overflows rather than leaving the viewport empty.
		m.resize()
		// Blocks are laid out for a fixed width, so content rendered for the
		// old size would be clipped at the new one until something else
		// happened to repaint it.
		m.refresh()
		return m, nil
	case tea.KeyPressMsg:
		// The config view owns the whole frame, so it takes the keyboard with it:
		// the input is off screen and would otherwise be typed into blind.
		if m.configVisible {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.configVisible = false
			}
			return m, nil
		}
		// Any other key withdraws a pending exit confirmation, so ctrl+c followed
		// by typing does not leave a live quit waiting one keystroke away.
		if m.quitArmed && msg.String() != "ctrl+c" {
			m.quitArmed = false
			m.resize()
		}
		// handle user input
		switch msg.String() {
		case "ctrl+c":
			// Confirmed: the user pressed it twice. Abandon any in-flight turn so
			// its goroutine unblocks instead of waiting on a channel nobody will
			// read again.
			if m.quitArmed {
				if m.cancel != nil {
					m.cancel()
				}
				return m, tea.Quit
			}
			// While the agent is working, ctrl+c interrupts rather than starting
			// to quit: arming here would make an interrupt look half-committed.
			if m.state == uiStateProcessing {
				if m.cancel != nil {
					m.cancel()
				}
				return m, nil
			}
			return m.armQuit()
		case "esc":
			if !m.menuVisible() {
				return m.forwardToInput(msg)
			}
			m.menuDismissed = true
			m.resize()
			return m, nil
		case "up", "down":
			if !m.menuVisible() {
				return m.forwardToInput(msg)
			}
			if msg.String() == "up" {
				m.menu.CursorUp()
			} else {
				m.menu.CursorDown()
			}
			return m, nil
		case "tab":
			if !m.menuVisible() {
				return m.forwardToInput(msg)
			}
			if selected, ok := m.menu.SelectedItem().(command); ok {
				m.input.SetValue(commandPrefix + selected.name)
				m.input.CursorEnd()
				m.syncMenu()
				m.resize()
			}
			return m, nil
		case "enter":
			// A command line never reaches the agent, in either UI state: /quit
			// is an escape hatch, so it must work while a turn is in flight.
			if strings.HasPrefix(m.input.Value(), commandPrefix) {
				return m.runCommand()
			}
			// only actually do anything if we are not currently waiting and there is actual input
			if m.state == uiStateIdle && m.input.Value() != "" {
				userInput := m.input.Value()
				m.input.Reset()
				return m.startTurn(userInput)
			}
		default:
			// Send other keypresses to text input model
			// TODO: Forward to viewport as well
			return m.forwardToInput(msg)
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
	case quitDisarmMsg:
		// Ignore a message from an earlier arming: the user has since disarmed and
		// armed again, and this one would cancel a confirmation they just asked for.
		if msg.generation == m.quitGeneration {
			m.quitArmed = false
			m.resize()
		}
		return m, nil

	case streamClosedMsg:
		return m.endTurn(), nil

	case cursor.BlinkMsg:
		// Forward to textinput
		m.input, _ = m.input.Update(msg)
		return m, nil

	case spinner.TickMsg:
		// Once idle, stop re-issuing ticks so the spinner doesn't keep
		// animating in the background between turns.
		if m.state != uiStateProcessing {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	default:
	}

	return m, textinput.Blink
}

// View implements the bubbletea Model interface
func (m model) View() tea.View {
	// A modal view: it replaces the frame rather than adding a row to it, so
	// there is no input on screen and no cursor to place.
	if m.configVisible {
		view := tea.NewView(renderConfig(m.config, m.width))
		view.AltScreen = false
		return view
	}

	viewportView := m.viewport.View()
	inputView := m.input.View()

	rows := []string{viewportView}
	if m.state == uiStateProcessing {
		rows = append(rows, m.spinnerLine())
	}
	if hint := m.quitHint(); hint != "" {
		rows = append(rows, hint)
	}
	if menu := m.menuView(); menu != "" {
		rows = append(rows, menu)
	}
	rows = append(rows, inputView)

	// fix position of textinput cursor
	cursor := m.input.Cursor()
	if cursor != nil {
		for _, row := range rows[:len(rows)-1] {
			cursor.Y += lipgloss.Height(row)
		}
	}
	// assemble view
	view := tea.NewView(lipgloss.JoinVertical(lipgloss.Top, rows...))
	view.AltScreen = false
	view.Cursor = cursor
	return view
}
