package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/commands"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/tui/menu"
	"github.com/rstarc/elencode/internal/tui/picker"
	"github.com/rstarc/elencode/internal/tui/transcript"
)

// banner titles the session, printed once the terminal width is known
const banner = "elencode"

// inputPrompt mirrors transcript.UserPromptMarker, which marks user messages in
// the transcript, so the two stay visibly the same character. Declared at
// package level (not inside newModel) because its *agent.Agent parameter
// shadows the agent package name.
const inputPrompt = transcript.UserPromptMarker

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
	turnID int // identifies the active turn to reject messages from an older one
	// TUI state. The transcript is not held here: it is printed above the frame
	// as it happens and belongs to the terminal's scrollback from then on. The
	// one exception is the block still being generated, which the stream holds
	// until it settles.
	stream  transcript.Stream
	input   textinput.Model // user input field
	spinner spinner.Model   // shown above input while state is uiStateProcessing
	width   int             // terminal width, 0 until the first WindowSizeMsg
	state   uiState
	// Sub-components. Each owns its own state and reports what the user did as
	// a message, which Update handles below.
	commands commands.Registry              // the slash commands this session knows
	menu     picker.Model[commands.Command] // the command menu under the input
	models   picker.Model[agent.Model]      // the model list /model opens
	// configVisible replaces the whole frame with the read-only config view
	configVisible bool
	headerPrinted bool // the session title has been printed
	// modelsLoading drives the spinner while the list is fetched, which happens
	// here rather than in the picker: it is an API call and needs the agent.
	modelsLoading bool
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
	return lipgloss.NewStyle().Foreground(menu.DescriptionColor).Render("press ctrl+c again to exit")
}

func newModel(agent *agent.Agent, cfg config.Config, registry commands.Registry) model {

	input := textinput.New()
	input.Placeholder = "start typing..."
	input.SetVirtualCursor(false)
	input.Focus()
	input.Prompt = inputPrompt + " "
	input.CharLimit = 0

	return model{
		agent:    agent,
		config:   cfg,
		commands: registry,
		menu:     newCommandMenu(registry),
		models:   newModelList(),
		input:    input,
		spinner:  spinner.New(spinner.WithSpinner(spinner.Ellipsis)),
		state:    uiStateIdle,
	}
}

// spinnerLine renders the "processing..." indicator shown above the input
// while a turn is in flight.
func (m model) spinnerLine() string {
	if m.modelsLoading {
		return "loading models" + m.spinner.View()
	}
	return "processing" + m.spinner.View()
}

// busy reports whether anything is in flight, and so whether the spinner row is
// drawn rather than only reserved
func (m model) busy() bool {
	return m.state == uiStateProcessing || m.modelsLoading
}

// printAbove inserts rendered above the frame, where it stays: the terminal
// owns it from then on, which is what makes scrolling back through a long
// session the terminal's job rather than this program's.
//
// Ordering is the caller's responsibility. Commands run concurrently, so two
// prints issued from separate updates can arrive in either order; anything that
// has to land in sequence must be chained with tea.Sequence.
func printAbove(rendered string) tea.Cmd {
	if rendered == "" {
		return nil
	}
	return tea.Println(rendered)
}

// streamEventMsg carries one Event from the in-flight turn
type streamEventMsg struct {
	turnID int
	event  agent.Event
}

// streamClosedMsg reports that the turn ended and the channel is drained
type streamClosedMsg struct{ turnID int }

// waitForEvent receives one Event from the in-flight turn. Update re-issues it
// after each event, since a tea.Cmd delivers exactly one message. The channel is captured
// by value, so a command left over from a finished turn reads that turn's
// closed channel rather than stealing an event from the next one.
func waitForEvent(events <-chan agent.Event, turnID int) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return streamClosedMsg{turnID: turnID}
		}
		return streamEventMsg{turnID: turnID, event: event}
	}
}

// forwardToInput hands a keypress to the text input and settles the list the
// edit it made belongs to.
func (m model) forwardToInput(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Whichever list is open is the one the typing filters. The command menu
	// must not see the text while the model list is up, or a slash would open it
	// behind the list.
	if m.models.Open() {
		m.models = m.models.SetQuery(m.input.Value())
	} else {
		m.menu = m.menu.SetQuery(m.input.Value())
	}
	return m, cmd
}

// runCommand handles Enter on a command line: it runs the command the menu is
// pointing at, passing the rest of the line as its argument, or reports that
// the line names no command. Resolving through the menu rather than looking the
// name up again is what keeps Enter to one rule — what is highlighted is what
// runs, and the user can see it.
//
// What the command does arrives back as a message, so the effect is handled in
// Update alongside every other one rather than here.
func (m model) runCommand() (model, tea.Cmd) {
	line := m.input.Value()
	highlighted, ok := m.menu.Highlighted()

	m.input.Reset()
	m.menu = m.menu.SetQuery("")

	if !ok {
		return m, m.reportError(fmt.Errorf("unknown command: %s", line))
	}
	// "/model   some-id" runs /model with some-id rather than being looked up
	// whole
	_, arg, _ := strings.Cut(strings.TrimSpace(line), " ")
	return m, highlighted.Execute(strings.TrimSpace(arg))
}

// reportError prints a failure into the transcript, where it stays: the user
// keeps whatever scrolled past, rather than watching it vanish on the next
// repaint.
func (m model) reportError(err error) tea.Cmd {
	return printAbove(transcript.Error(err, m.width))
}

// modelsMsg carries the result of a /model lookup. choose is the model the user
// named on the command line, empty when they asked for the picker instead.
type modelsMsg struct {
	models []agent.Model
	choose string
	err    error
}

// loadModels fetches the model list in the background, since it is an API call
// and the UI must stay responsive while it runs.
func (m model) loadModels(choose string) (model, tea.Cmd) {
	m.modelsLoading = true

	// Captured rather than read off m inside the closure: the command runs later,
	// against whatever model value the loop happens to hold.
	a := m.agent
	fetch := func() tea.Msg {
		models, err := a.Models(context.Background())
		return modelsMsg{models: models, choose: choose, err: err}
	}
	return m, tea.Batch(fetch, m.spinner.Tick)
}

// showModels acts on a fetched model list: it either selects the model the user
// named or opens the picker.
func (m model) showModels(msg modelsMsg) (model, tea.Cmd) {
	m.modelsLoading = false

	if msg.err != nil {
		return m, m.reportError(fmt.Errorf("listing models: %w", msg.err))
	}

	if msg.choose != "" {
		for _, candidate := range msg.models {
			if strings.EqualFold(candidate.ID, msg.choose) {
				return m.selectModel(candidate)
			}
		}
		// The list was just fetched, so an unknown id is the user's typo rather
		// than a stale cache
		return m, m.reportError(fmt.Errorf("unknown model: %s", msg.choose))
	}

	// The list borrows the input to filter with, so it starts on an empty one:
	// whatever was typed while it loaded belongs to the command line it replaces.
	m.input.Reset()
	m.menu = m.menu.SetQuery("")
	m.models = m.models.Show(msg.models, func(candidate agent.Model) bool {
		return candidate.ID == m.config.Model
	})
	return m, nil
}

// chooseModel switches to the model the list is pointing at, or does nothing
// when the filter leaves nothing to point at: Esc stays the way out of a list
// that has no answer in it.
func (m model) chooseModel() (model, tea.Cmd) {
	chosen, ok := m.models.Highlighted()
	if !ok {
		return m, nil
	}

	m.models = m.models.Close()
	// The arrow keys left the id in the input, and it has now been acted on
	m.input.Reset()
	return m.selectModel(chosen)
}

// selectModel switches to id, clearing the conversation the previous model
// produced and remembering the choice for the next session.
func (m model) selectModel(chosen agent.Model) (model, tea.Cmd) {
	// The turn in flight was started on the old model and is about to lose the
	// context window it belongs to, so it is abandoned rather than left running.
	var interrupted string
	if m.state == uiStateProcessing {
		interrupted = m.stream.End()
		m = m.endTurn()
	}

	m.agent.SetModel(chosen)
	m.config.Model = chosen.ID

	// Reported but not fatal: the model is switched for this session either way
	var failed tea.Cmd
	if err := m.config.Save(); err != nil {
		failed = m.reportError(fmt.Errorf("saving the model to %s: %w", m.config.Path, err))
	}

	// The switch changes nothing on screen by itself: what is printed stays
	// printed, and the conversation above is no longer sent to anyone. The
	// notice is the only thing that says so.
	notice := transcript.Notice("switched to "+chosen.ID+" (context cleared)", m.width)

	// Sequenced, not batched: these have to reach the scrollback in this order
	return m, tea.Sequence(printAbove(interrupted), printAbove(notice), failed)
}

// startTurn hands userInput to the agent and begins receiving its Events
func (m model) startTurn(userInput string) (model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.turnID++
	m.events = m.agent.Run(ctx, userInput)
	m.stream.Reset()
	m.state = uiStateProcessing

	// The prompt is printed here rather than when the agent echoes it back,
	// because it never comes back: Run appends it to the context window without
	// announcing it. Sequenced ahead of the turn so it cannot land after the
	// reply it asked for.
	prompt := transcript.Message(agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: userInput}}), m.width)
	return m, tea.Sequence(printAbove(prompt), tea.Batch(waitForEvent(m.events, m.turnID), m.spinner.Tick))
}

// endTurn releases the finished turn's resources and returns to idle
func (m model) endTurn() model {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.events = nil
	m.state = uiStateIdle
	return m
}

// Init implements the bubbletea Model interface
func (m model) Init() tea.Cmd {
	return textinput.Blink
}

// Update implements the bubbletea Model interface
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		var print tea.Cmd
		if m.state == uiStateProcessing && m.width > 0 && m.width != msg.Width {
			// Rendered row indexes are not stable across widths, so settle the
			// current segment before the frame starts using the new width.
			print = printAbove(m.stream.End())
		}
		m.width = msg.Width
		// textinput draws the prompt before, and a cursor cell after, the width
		// it is given, so passing the terminal width makes the input row wider
		// than the terminal. JoinVertical then pads every other row out to
		// match, pushing the whole view past the right edge.
		m.input.SetWidth(max(msg.Width-lipgloss.Width(m.input.Prompt)-1, 1))
		m.stream.SetWidth(msg.Width)
		m.menu.SetWidth(msg.Width)
		m.models.SetWidth(msg.Width)
		// Only the frame follows the new width. What is already printed keeps
		// the width it was printed at, as the terminal owns those lines now.

		// The header is printed rather than drawn, so it scrolls away with the
		// rest of the session instead of sitting above every frame. It waits
		// for this message because it spans the terminal, and until now there
		// was no width to span. A resize is not a new session.
		if !m.headerPrinted {
			m.headerPrinted = true
			return m, printAbove(transcript.Header(banner, m.width))
		}
		return m, print
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
		case "esc", "up", "down", "tab":
			// These drive whichever list is open, and the input when none is: an
			// arrow key still has to move the cursor.
			var cmd tea.Cmd
			switch {
			case m.models.Open():
				m.models, cmd = m.models.Update(msg)
			case m.menu.Open():
				m.menu, cmd = m.menu.Update(msg)
			default:
				return m.forwardToInput(msg)
			}
			return m, cmd
		case "enter":
			// The model list holds Enter until something is chosen or Esc closes
			// it, so a filter that matches nothing cannot start a turn by accident.
			if m.models.Open() {
				return m.chooseModel()
			}
			// A command line never reaches the agent, in either UI state: /quit
			// is an escape hatch, so it must work while a turn is in flight. The
			// menu being open is that test — it is open exactly while the input
			// holds a command line.
			if m.menu.Open() {
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
			return m.forwardToInput(msg)
		}
	case streamEventMsg:
		if m.state != uiStateProcessing || msg.turnID != m.turnID {
			return m, nil
		}
		var print tea.Cmd
		switch event := msg.event.(type) {
		case agent.TextDeltaEvent:
			print = printAbove(m.stream.Delta(event.Text, false))
		case agent.ThinkingDeltaEvent:
			print = printAbove(m.stream.Delta(event.Text, true))
		case agent.MessageEvent:
			print = printAbove(m.stream.Landed(event.Message))
		case agent.ErrorEvent:
			print = m.reportError(event.Err)
		}
		// Sequenced, not batched: batched commands run concurrently, so the next
		// event could be printed before this one. Waiting for the next event is
		// chained behind the print for the same reason. The turn ends when the
		// channel closes, not here: a MessageEvent may be followed by tool
		// results and another round of inference.
		return m, tea.Sequence(print, waitForEvent(m.events, m.turnID))
	case commands.ShowConfigMsg:
		m.configVisible = true
		return m, nil

	case commands.ChooseModelMsg:
		return m.loadModels(msg.ID)

	case commands.QuitMsg:
		// Abandon any in-flight turn, as ctrl+c does, so its goroutine unblocks
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit

	case picker.PreviewMsg:
		// Only the input follows the highlight. The query behind the list stays
		// as the user typed it, so the input can read "/quit" while the list is
		// still the one "/q" matched — filtering by the name under the highlight
		// would narrow it to that row and leave nowhere to move.
		m.input.SetValue(msg.Text)
		m.input.CursorEnd()
		return m, nil

	case picker.ClosedMsg:
		// The list closed itself and forgot its query; clearing the input is what
		// keeps the two saying the same thing.
		m.input.Reset()
		return m, nil

	case modelsMsg:
		return m.showModels(msg)

	case quitDisarmMsg:
		// Ignore a message from an earlier arming: the user has since disarmed and
		// armed again, and this one would cancel a confirmation they just asked for.
		if msg.generation == m.quitGeneration {
			m.quitArmed = false
		}
		return m, nil

	case streamClosedMsg:
		if m.state != uiStateProcessing || msg.turnID != m.turnID {
			return m, nil
		}
		// A turn cut short leaves its last row in the frame, where nothing will
		// print it once the frame stops showing it.
		rest := m.stream.End()
		return m.endTurn(), printAbove(rest)

	case cursor.BlinkMsg:
		// Forward to textinput
		m.input, _ = m.input.Update(msg)
		return m, nil

	case spinner.TickMsg:
		// Once idle, stop re-issuing ticks so the spinner doesn't keep
		// animating in the background between turns.
		if !m.busy() {
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

	inputView := m.input.View()

	var rows []string
	// The only part of the transcript still in the frame: the row being written
	// into. Everything settled has already been printed above.
	if tail := m.stream.Tail(); tail != "" {
		rows = append(rows, tail)
	}
	if m.busy() {
		rows = append(rows, m.spinnerLine())
	}
	if hint := m.quitHint(); hint != "" {
		rows = append(rows, hint)
	}
	if view := m.menu.View(); view != "" {
		rows = append(rows, view)
	}
	if view := m.models.View(); view != "" {
		rows = append(rows, view)
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
