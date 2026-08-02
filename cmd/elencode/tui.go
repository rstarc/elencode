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
	"github.com/rstarc/elencode/internal/config"
)

// banner titles the session, printed once the terminal width is known
const banner = "elencode"

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
	// TUI state. The transcript is not held here: it is printed above the frame
	// as it happens and belongs to the terminal's scrollback from then on.
	partial  string          // assistant text streamed so far
	streamed int             // rows of partial already printed; the rest is in the frame
	input    textinput.Model // user input field
	spinner  spinner.Model   // shown above input while state is uiStateProcessing
	width    int             // terminal width, 0 until the first WindowSizeMsg
	state    uiState
	// command menu state. Visibility is derived from the input rather than
	// stored, so the two cannot disagree; see menuVisible.
	menuDismissed bool // Esc hides the menu for the rest of this command line
	menuIndex     int  // highlighted row within the current match set
	// configVisible replaces the whole frame with the read-only config view
	configVisible bool
	headerPrinted bool // the session title has been printed
	// model picker state, driven by /model
	modelsLoading      bool          // the model list is being fetched
	modelPickerVisible bool          // the picker has the keyboard
	models             []agent.Model // what the last fetch returned
	modelIndex         int           // highlighted row within models
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
	return renderMenu(matchCommands(m.input.Value()), m.menuIndex, m.width)
}

// modelPickerView renders the model picker, or "" while it is closed
func (m model) modelPickerView() string {
	if !m.modelPickerVisible {
		return ""
	}
	return renderModelMenu(m.models, m.modelIndex, m.width)
}

func newModel(agent *agent.Agent, cfg config.Config) model {

	input := textinput.New()
	input.Placeholder = "start typing..."
	input.SetVirtualCursor(false)
	input.Focus()
	input.Prompt = inputPrompt + " "
	input.CharLimit = 0

	return model{
		agent:   agent,
		config:  cfg,
		input:   input,
		spinner: spinner.New(spinner.WithSpinner(spinner.Ellipsis)),
		state:   uiStateIdle,
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

// streamRows renders the text streamed so far as transcript rows. Every row but
// the last is settled: wrapping is greedy, so more text can only extend the
// last row, never reflow the ones above it.
func (m model) streamRows() []string {
	if m.partial == "" {
		return nil
	}
	return strings.Split(agent.RenderStreamingText(m.partial, m.width), "\n")
}

// streamTail is the row the text is still being written into, the one part of
// the transcript that lives in the frame rather than in the scrollback.
func (m model) streamTail() string {
	rows := m.streamRows()
	if len(rows) == 0 {
		return ""
	}
	return rows[len(rows)-1]
}

// flushStream returns the rows that have settled since the last flush, and
// records them as printed. The trailing row is held back: it can still grow.
func (m *model) flushStream() string {
	rows := m.streamRows()
	if len(rows) <= m.streamed+1 {
		return ""
	}

	settled := rows[m.streamed : len(rows)-1]
	m.streamed = len(rows) - 1
	return strings.Join(settled, "\n")
}

// endStream returns whatever of the streamed text has not been printed yet,
// including the trailing row, and forgets it. Called when the text can no
// longer grow: the message landed, or the turn ended without it.
func (m *model) endStream() string {
	rows := m.streamRows()
	var rest string
	if m.streamed < len(rows) {
		rest = strings.Join(rows[m.streamed:], "\n")
	}
	m.partial, m.streamed = "", 0
	return rest
}

// printMessage returns what a landed message adds to the transcript. The
// assistant's text is left out: it was printed as it streamed, so only the
// trailing row and the blocks that never stream — tool uses, thinking — are
// new here.
//
// Nothing requests thinking yet. Whatever turns it on has to stream it too:
// reasoning comes before the answer in the message, but the answer is printed
// as it streams, so a thinking block printed here lands after the answer it
// led to.
func (m *model) printMessage(msg agent.Message) string {
	rendered := []string{}
	if rest := m.endStream(); rest != "" {
		rendered = append(rendered, rest)
	}

	for _, block := range msg.Content {
		if _, isText := block.(agent.TextBlock); isText && msg.Role == agent.RoleAssistant {
			continue
		}
		if row := agent.RenderBlock(block, msg.Role, m.width); row != "" {
			rendered = append(rendered, row)
		}
	}
	return strings.Join(rendered, "\n")
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

	// The match set may have changed under the highlight, so it would otherwise
	// point at a different command than the one the user was looking at.
	m.menuIndex = 0
	// Esc dismisses the menu for one command line only; leaving the line clears it.
	if !strings.HasPrefix(m.input.Value(), commandPrefix) {
		m.menuDismissed = false
	}
	return m, cmd
}

// runCommand handles Enter on a command line: it runs the command the input
// names exactly, or reports that there is no such command.
func (m model) runCommand() (model, tea.Cmd) {
	cmd, ok := lookupCommand(m.input.Value())
	if !ok {
		unknown := fmt.Errorf("unknown command: %s", m.input.Value())
		m.input.Reset()
		m.menuDismissed = false
		m.menuIndex = 0
		return m, m.reportError(unknown)
	}

	_, arg := splitCommand(m.input.Value())

	switch cmd.name {
	case "config":
		m.configVisible = true
		m.input.Reset()
		m.menuIndex = 0
		return m, nil
	case "model":
		return m.loadModels(arg)
	case "quit":
		// Abandon any in-flight turn, as ctrl+c does, so its goroutine unblocks
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	default:
		m.input.Reset()
		return m, m.reportError(fmt.Errorf("command not implemented: %s", cmd.name))
	}
}

// reportError prints a failure into the transcript, where it stays: the user
// keeps whatever scrolled past, rather than watching it vanish on the next
// repaint.
func (m model) reportError(err error) tea.Cmd {
	return printAbove(agent.RenderError(err, m.width))
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
	m.input.Reset()
	m.menuIndex = 0

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
				return m.selectModel(candidate.ID)
			}
		}
		// The list was just fetched, so an unknown id is the user's typo rather
		// than a stale cache
		return m, m.reportError(fmt.Errorf("unknown model: %s", msg.choose))
	}

	m.models = msg.models
	m.modelPickerVisible = true
	// Start where the user already is, rather than making them find it
	m.modelIndex = 0
	for i, candidate := range msg.models {
		if candidate.ID == m.config.Model {
			m.modelIndex = i
		}
	}
	return m, nil
}

// selectModel switches to id, clearing the conversation the previous model
// produced and remembering the choice for the next session.
func (m model) selectModel(id string) (model, tea.Cmd) {
	// The turn in flight was started on the old model and is about to lose the
	// context window it belongs to, so it is abandoned rather than left running.
	var interrupted string
	if m.state == uiStateProcessing {
		interrupted = m.endStream()
		m = m.endTurn()
	}

	m.agent.SetModel(id)
	m.config.Model = id
	m.modelPickerVisible = false
	m.models = nil
	m.modelIndex = 0

	// Reported but not fatal: the model is switched for this session either way
	var failed tea.Cmd
	if err := m.config.Save(); err != nil {
		failed = m.reportError(fmt.Errorf("saving the model to %s: %w", m.config.Path, err))
	}

	// The switch changes nothing on screen by itself: what is printed stays
	// printed, and the conversation above is no longer sent to anyone. The
	// notice is the only thing that says so.
	notice := agent.RenderNotice("switched to "+id+" (context cleared)", m.width)

	// Sequenced, not batched: these have to reach the scrollback in this order
	return m, tea.Sequence(printAbove(interrupted), printAbove(notice), failed)
}

// updateModelPicker drives the picker, which owns the keyboard while it is
// open: a keystroke reaching the input would open the command menu underneath.
func (m model) updateModelPicker(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.modelPickerVisible = false
		m.models = nil
		m.modelIndex = 0
	case "up", "down":
		delta := 1
		if msg.String() == "up" {
			delta = -1
		}
		m.modelIndex = moveHighlight(m.modelIndex, delta, len(m.models))
	case "enter":
		if len(m.models) > 0 {
			return m.selectModel(m.models[m.modelIndex].ID)
		}
	}
	return m, nil
}

// startTurn hands userInput to the agent and begins receiving its Events
func (m model) startTurn(userInput string) (model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = m.agent.Run(ctx, userInput)
	m.partial = ""
	m.streamed = 0
	m.state = uiStateProcessing

	// The prompt is printed here rather than when the agent echoes it back,
	// because it never comes back: Run appends it to the context window without
	// announcing it. Sequenced ahead of the turn so it cannot land after the
	// reply it asked for.
	prompt := agent.RenderMessage(agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: userInput}}), m.width)
	return m, tea.Sequence(printAbove(prompt), tea.Batch(waitForEvent(m.events), m.spinner.Tick))
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
		m.width = msg.Width
		// textinput draws the prompt before, and a cursor cell after, the width
		// it is given, so passing the terminal width makes the input row wider
		// than the terminal. JoinVertical then pads every other row out to
		// match, pushing the whole view past the right edge.
		m.input.SetWidth(max(msg.Width-lipgloss.Width(m.input.Prompt)-1, 1))
		// Only the frame follows the new width. What is already printed keeps
		// the width it was printed at, as the terminal owns those lines now.

		// The header is printed rather than drawn, so it scrolls away with the
		// rest of the session instead of sitting above every frame. It waits
		// for this message because it spans the terminal, and until now there
		// was no width to span. A resize is not a new session.
		if !m.headerPrinted {
			m.headerPrinted = true
			return m, printAbove(agent.RenderHeader(banner, m.width))
		}
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
		}
		// The picker takes every key but ctrl+c, which keeps meaning "quit"
		// wherever the user is.
		if m.modelPickerVisible && msg.String() != "ctrl+c" {
			return m.updateModelPicker(msg)
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
			return m, nil
		case "up", "down":
			if !m.menuVisible() {
				return m.forwardToInput(msg)
			}
			delta := 1
			if msg.String() == "up" {
				delta = -1
			}
			m.menuIndex = moveHighlight(m.menuIndex, delta, len(matchCommands(m.input.Value())))
			return m, nil
		case "tab":
			if !m.menuVisible() {
				return m.forwardToInput(msg)
			}
			if matches := matchCommands(m.input.Value()); len(matches) > 0 {
				m.input.SetValue(commandPrefix + matches[m.menuIndex].name)
				m.input.CursorEnd()
				m.menuIndex = 0
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
			return m.forwardToInput(msg)
		}
	case streamEventMsg:
		var print tea.Cmd
		switch event := msg.event.(type) {
		case agent.TextDeltaEvent:
			m.partial = m.partial + event.Text
			print = printAbove(m.flushStream())
		case agent.MessageEvent:
			print = printAbove(m.printMessage(event.Message))
		case agent.ErrorEvent:
			print = m.reportError(event.Err)
		}
		// Sequenced, not batched: batched commands run concurrently, so the next
		// event could be printed before this one. Waiting for the next event is
		// chained behind the print for the same reason. The turn ends when the
		// channel closes, not here: a MessageEvent may be followed by tool
		// results and another round of inference.
		return m, tea.Sequence(print, waitForEvent(m.events))
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
		// A turn cut short leaves its last row in the frame, where nothing will
		// print it once the frame stops showing it.
		rest := m.endStream()
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
	if tail := m.streamTail(); tail != "" {
		rows = append(rows, tail)
	}
	if m.busy() {
		rows = append(rows, m.spinnerLine())
	}
	if hint := m.quitHint(); hint != "" {
		rows = append(rows, hint)
	}
	if menu := m.menuView(); menu != "" {
		rows = append(rows, menu)
	}
	if picker := m.modelPickerView(); picker != "" {
		rows = append(rows, picker)
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
