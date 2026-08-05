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
	"github.com/rstarc/elencode/internal/tui/commandmenu"
	"github.com/rstarc/elencode/internal/tui/menu"
	"github.com/rstarc/elencode/internal/tui/modelpicker"
	"github.com/rstarc/elencode/internal/tui/transcript"
)

// banner titles the session. It is the document's first entry, written out by
// main before the program starts: see openDocument.
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
	// TUI state. doc is everything the session has settled, held as entries
	// rather than as the rows they rendered to so a width change can lay the
	// whole thing out again. The block still being generated is not in it yet:
	// the stream holds that until it settles.
	doc     transcript.Document
	stream  transcript.Stream
	input   textinput.Model // user input field
	spinner spinner.Model   // shown above input while state is uiStateProcessing
	width   int             // terminal width, 0 until the first WindowSizeMsg
	state   uiState
	// Sub-components. Each owns its own state and reports what the user did as
	// a message, which Update handles below.
	commands commands.Registry // the slash commands this session knows
	menu     commandmenu.Model // the command menu under the input
	picker   modelpicker.Model // the model list /model opens
	// configVisible replaces the whole frame with the read-only config view
	configVisible bool
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
		agent: agent,
		// The session opens with its title. main writes the document out before
		// the program starts; from then on it is this model's to extend.
		doc:      transcript.Document{transcript.HeaderEntry{Title: banner}},
		config:   cfg,
		commands: registry,
		menu:     commandmenu.New(registry),
		picker:   modelpicker.New(),
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

// printAbove inserts rendered above the frame, where it stays until the next
// reprint lays the document out again.
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

// append records an entry in the document and prints it above the frame. The
// two go together: what is on screen is only ever a drawing of the document.
func (m *model) append(entry transcript.Entry) tea.Cmd {
	m.doc.Append(entry)
	return printAbove(entry.Render(m.width))
}

// settle moves the block in flight into the document and returns whatever of it
// is not on screen yet. The rows printed as it streamed are provisional: they
// are not in the document, so only the entry survives a reprint.
func (m *model) settle() string {
	partial := m.stream.Partial()
	if partial == nil {
		return ""
	}
	rest := m.stream.End()
	m.doc.Append(transcript.BlockEntry{Block: partial, Role: agent.RoleAssistant})
	return rest
}

// delta adds a fragment to the block in flight and prints whatever that
// settles. Reasoning and the answer are separate blocks, so a fragment of one
// ends the other: the stream would end it silently, so it is settled here
// instead, where it can reach the document.
func (m *model) delta(text string, thinking bool) tea.Cmd {
	var settled tea.Cmd
	if m.stream.Partial() != nil && m.stream.Thinking() != thinking {
		settled = printAbove(m.settle())
	}
	return tea.Sequence(settled, printAbove(m.stream.Delta(text, thinking)))
}

// landed records what a message adds when it arrives: what is left of the block
// in flight, then the blocks that never stream. What already streamed is in the
// document as its own entry, so the message is not recorded whole.
func (m *model) landed(msg agent.Message) tea.Cmd {
	cmds := []tea.Cmd{printAbove(m.settle())}
	for _, entry := range transcript.LandedBlocks(msg) {
		cmds = append(cmds, m.append(entry))
	}
	return tea.Sequence(cmds...)
}

// clearAll erases the screen, homes the cursor and drops the scrollback. It
// leads every reprint: the old layout is at the wrong width and there is no way
// to reach the part of it that has scrolled away.
const clearAll = "\x1b[2J\x1b[H\x1b[3J"

// reprint lays the whole document out again at the current width.
//
// It goes out as one tea.Println rather than a raw write plus a print because
// insertAbove ends by telling the renderer its frame starts at the cursor. The
// clear homes the cursor, the document is written from there, and the frame
// lands directly underneath — so the renderer's idea of where it is stays true
// without any resynchronizing.
func (m model) reprint() tea.Cmd {
	return tea.Println(clearAll + m.doc.Render(m.width))
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

// forwardToInput hands a keypress to the text input and settles the menu around
// the edit it made.
func (m model) forwardToInput(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.menu = m.menu.SetQuery(m.input.Value())
	return m, cmd
}

// runCommand handles Enter on a command line: it runs the command the input
// names exactly, or reports that there is no such command. What the command
// does arrives back as a message, so the effect is handled in Update alongside
// every other one rather than here.
func (m model) runCommand() (model, tea.Cmd) {
	cmd, ok := m.commands.Run(m.input.Value())
	if !ok {
		cmd = m.reportError(fmt.Errorf("unknown command: %s", m.input.Value()))
	}

	m.input.Reset()
	m.menu = m.menu.SetQuery("")
	return m, cmd
}

// reportError settles a failure into the document, so it survives a reprint
// rather than vanishing the next time the session is laid out.
func (m *model) reportError(err error) tea.Cmd {
	return m.append(transcript.ErrorEntry{Err: err})
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
		// Bound before returning: reportError records the entry on m, and a
		// return operand evaluated first would carry the document without it.
		failed := m.reportError(fmt.Errorf("listing models: %w", msg.err))
		return m, failed
	}

	if msg.choose != "" {
		for _, candidate := range msg.models {
			if strings.EqualFold(candidate.ID, msg.choose) {
				return m.selectModel(candidate)
			}
		}
		// The list was just fetched, so an unknown id is the user's typo rather
		// than a stale cache
		failed := m.reportError(fmt.Errorf("unknown model: %s", msg.choose))
		return m, failed
	}

	m.picker = m.picker.Show(msg.models, m.config.Model)
	return m, nil
}

// selectModel switches to id, clearing the conversation the previous model
// produced and remembering the choice for the next session.
func (m model) selectModel(chosen agent.Model) (model, tea.Cmd) {
	// The turn in flight was started on the old model and is about to lose the
	// context window it belongs to, so it is abandoned rather than left running.
	var interrupted tea.Cmd
	if m.state == uiStateProcessing {
		interrupted = printAbove(m.settle())
		m = m.endTurn()
	}

	m.agent.SetModel(chosen)
	m.config.Model = chosen.ID

	// The switch changes nothing on screen by itself: the transcript stays as it
	// is, and the conversation above is no longer sent to anyone. The notice is
	// the only thing that says so.
	notice := m.append(transcript.NoticeEntry{Text: "switched to " + chosen.ID + " (context cleared)"})

	// Reported but not fatal: the model is switched for this session either way.
	// Appended after the notice so the document reads in the order it printed.
	var failed tea.Cmd
	if err := m.config.Save(); err != nil {
		failed = m.reportError(fmt.Errorf("saving the model to %s: %w", m.config.Path, err))
	}

	// Sequenced, not batched: these have to reach the screen in this order
	return m, tea.Sequence(interrupted, notice, failed)
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
	prompt := m.append(transcript.MessageEntry{
		Message: agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: userInput}}),
	})
	return m, tea.Sequence(prompt, tea.Batch(waitForEvent(m.events, m.turnID), m.spinner.Tick))
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
		// A new width means every block wraps differently, so the session is laid
		// out again from the document. Height does not affect wrapping, and the
		// first size message is not a change: there was no layout before it.
		reflow := m.width > 0 && m.width != msg.Width
		if reflow {
			// Rendered row indexes are not stable across widths, so the block in
			// flight has to settle into the document before it is laid out again.
			// What settle hands back is dropped rather than printed: the reprint
			// below is about to draw the whole document anyway.
			m.settle()
		}
		m.width = msg.Width
		// textinput draws the prompt before, and a cursor cell after, the width
		// it is given, so passing the terminal width makes the input row wider
		// than the terminal. JoinVertical then pads every other row out to
		// match, pushing the whole view past the right edge.
		m.input.SetWidth(max(msg.Width-lipgloss.Width(m.input.Prompt)-1, 1))
		m.stream.SetWidth(msg.Width)
		m.menu.SetWidth(msg.Width)
		m.picker.SetWidth(msg.Width)
		if !reflow {
			return m, nil
		}
		return m, m.reprint()
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
		// wherever the user is. A keystroke reaching the input underneath would
		// open the command menu behind the picker.
		if m.picker.Focused() && msg.String() != "ctrl+c" {
			var cmd tea.Cmd
			m.picker, cmd = m.picker.Update(msg)
			return m, cmd
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
			// These belong to the menu while it is showing, and to the input
			// otherwise: an arrow key still has to move the cursor.
			if !m.menu.Visible() {
				return m.forwardToInput(msg)
			}
			var cmd tea.Cmd
			m.menu, cmd = m.menu.Update(msg)
			return m, cmd
		case "enter":
			// A command line never reaches the agent, in either UI state: /quit
			// is an escape hatch, so it must work while a turn is in flight.
			if strings.HasPrefix(m.input.Value(), commands.Prefix) {
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
			print = m.delta(event.Text, false)
		case agent.ThinkingDeltaEvent:
			print = m.delta(event.Text, true)
		case agent.MessageEvent:
			print = m.landed(event.Message)
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

	case commandmenu.CompleteMsg:
		m.input.SetValue(msg.Input)
		m.input.CursorEnd()
		m.menu = m.menu.SetQuery(msg.Input)
		return m, nil

	case modelpicker.SelectedMsg:
		return m.selectModel(msg.Model)

	case modelpicker.ClosedMsg:
		// The picker closed itself; there is nothing left to undo here.
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
		rest := m.settle()
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
	if view := m.picker.View(); view != "" {
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
