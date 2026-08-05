package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/commands"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/tui/menu"
	"github.com/rstarc/elencode/internal/tui/transcript"
)

// newTestModel builds a model backed by an agent that is never Run, so it needs
// no provider: only the transcript is read during rendering.
func newTestModel() model {
	return newModel(agent.New(nil, nil), config.Config{}, defaultCommands())
}

// update applies msg and asserts the result is still our model type
func update(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()

	next, _ := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return got
}

func TestUpdateAccumulatesTextDeltas(t *testing.T) {
	m := newTestModel()
	m.state = uiStateProcessing

	m = update(t, m, streamEventMsg{event: agent.TextDeltaEvent{Text: "Hel"}})
	m = update(t, m, streamEventMsg{event: agent.TextDeltaEvent{Text: "lo"}})

	if got := ansi.Strip(m.stream.Tail()); !strings.Contains(got, "Hello") {
		t.Errorf("streamed row = %q, want the deltas accumulated into %q", got, "Hello")
	}
}

func TestUpdateReturnsToIdleWhenStreamCloses(t *testing.T) {
	m := newTestModel()
	m.state = uiStateProcessing
	m.stream.Delta("half a sentence", false)

	m = update(t, m, streamClosedMsg{turnID: m.turnID})

	if m.state != uiStateIdle {
		t.Errorf("state = %v, want uiStateIdle", m.state)
	}
	if got := m.stream.Tail(); got != "" {
		t.Errorf("stream = %q, want it cleared when the turn ends", got)
	}
}

func TestStaleStreamCloseDoesNotEndNewTurn(t *testing.T) {
	m := newTestModel()
	m.state = uiStateProcessing
	m.turnID = 2
	newEvents := make(chan agent.Event)
	m.events = newEvents

	m = update(t, m, streamClosedMsg{turnID: 1})

	if m.state != uiStateProcessing {
		t.Errorf("state = %v, want the new turn to remain active", m.state)
	}
	if m.events != newEvents {
		t.Error("stale close cleared the new turn's event channel")
	}
}

func TestViewPutsCursorOnTheInputRow(t *testing.T) {
	const height = 20

	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: height})

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("view has no cursor, want one on the input row")
	}
	// Nothing is stacked above the input while idle, so the cursor sits on the
	// first row of the frame.
	wantY := 0
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (the input row)", view.Cursor.Y, wantY)
	}
}

func TestViewShowsSpinnerWhileProcessing(t *testing.T) {
	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.state = uiStateProcessing

	if view := m.View().Content; !strings.Contains(view, "processing") {
		t.Errorf("view does not show the processing spinner:\n%s", view)
	}
}

func TestViewHidesSpinnerWhenIdle(t *testing.T) {
	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})

	if view := m.View().Content; strings.Contains(view, "processing") {
		t.Errorf("view shows the processing spinner while idle:\n%s", view)
	}
}

// TestViewFitsTerminalHeightWhileProcessing guards the spinner row: it makes
// the view one row taller than the terminal, and the inline renderer drops the
// top row of an oversized frame — which right after Enter is the user's own
// message, so it only reappears once the turn ends and the spinner goes away.
func TestViewFitsTerminalHeightWhileProcessing(t *testing.T) {
	const height = 20

	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: height})
	m.state = uiStateProcessing

	if got := lipgloss.Height(m.View().Content); got > height {
		t.Errorf("view is %d rows tall, want <= %d", got, height)
	}
}

func TestViewPutsCursorBelowSpinnerWhileProcessing(t *testing.T) {
	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.state = uiStateProcessing

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("view has no cursor, want one on the input row")
	}
	wantY := lipgloss.Height(m.spinnerLine())
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (below the spinner)", view.Cursor.Y, wantY)
	}
}

// typeText sends one key press per rune, so the model sees the same message
// stream a real keyboard produces rather than a value set behind its back.
func typeText(t *testing.T, m model, text string) model {
	t.Helper()

	for _, r := range text {
		m = update(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

// updateCmd applies msg and returns the resulting model and command
func updateCmd(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()

	next, cmd := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return got, cmd
}

// press sends a key and delivers whatever it produced back to Update, as the
// runtime would. Slash commands and the sub-components report what the user did
// as a message, so the effect is one round-trip away from the keypress. Only
// for keys handled that way: plain input starts a turn, whose command blocks.
func press(t *testing.T, m model, key tea.KeyPressMsg) (model, tea.Cmd) {
	t.Helper()

	m, cmd := updateCmd(t, m, key)
	if cmd == nil {
		return m, nil
	}
	msg := cmd()
	if msg == nil {
		return m, nil
	}
	return updateCmd(t, m, msg)
}

// enter is press on the Enter key, which is how every command line is run
func enter(t *testing.T, m model) (model, tea.Cmd) {
	t.Helper()
	return press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

// quits reports whether cmd would end the program
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func newSizedModel(t *testing.T) model {
	t.Helper()
	return update(t, newTestModel(), tea.WindowSizeMsg{Width: 80, Height: 20})
}

func TestSlashOpensTheCommandMenu(t *testing.T) {
	m := typeText(t, newSizedModel(t), commands.Prefix)

	if view := m.View().Content; !strings.Contains(view, "/quit") {
		t.Errorf("view does not show the command menu:\n%s", view)
	}
}

func TestPlainTextDoesNotOpenTheCommandMenu(t *testing.T) {
	m := typeText(t, newSizedModel(t), "hello")

	if view := m.View().Content; strings.Contains(view, "/quit") {
		t.Errorf("view shows the command menu for non-command input:\n%s", view)
	}
}

// TestEscClearsTheCommandLine covers leaving a menu the user has changed their
// mind about: the half-typed command goes with it, rather than being left for
// them to delete.
func TestEscClearsTheCommandLine(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/qu")

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.menu.Open() {
		t.Error("menu still open after Esc")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared with the menu", m.input.Value())
	}
}

func TestMenuReopensOnANewCommandLine(t *testing.T) {
	m := typeText(t, newSizedModel(t), commands.Prefix)
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	m = typeText(t, m, commands.Prefix)

	if !m.menu.Open() {
		t.Error("menu stayed closed on a new command line")
	}
}

func TestTabCompletesTheHighlightedCommand(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/qu")

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	if m.input.Value() != "/quit" {
		t.Errorf("input = %q, want %q", m.input.Value(), "/quit")
	}
}

// TestArrowsCompleteIntoTheInput is the point of the arrow keys: a slash and a
// walk down the list is enough to type a command.
func TestArrowsCompleteIntoTheInput(t *testing.T) {
	m := typeText(t, newSizedModel(t), commands.Prefix)

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})

	if want := commands.Prefix + "model"; m.input.Value() != want {
		t.Errorf("input = %q, want %q", m.input.Value(), want)
	}
	// The list is filtered by what was typed, not by what the arrows wrote, or
	// there would be one row left and nowhere to move
	if got := len(m.menu.Matches()); got != 3 {
		t.Errorf("%d commands left after arrowing, want all 3", got)
	}
}

// TestEnterRunsTheHighlightedCommand covers picking a command without spelling
// it out: what the menu is pointing at is what Enter runs.
func TestEnterRunsTheHighlightedCommand(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/q")

	m, cmd := enter(t, m)

	if !quits(cmd) {
		t.Error("Enter did not run the highlighted /quit")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared once the command ran", m.input.Value())
	}
}

func TestEnterRunsQuitCommand(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/quit")

	_, cmd := enter(t, m)

	if !quits(cmd) {
		t.Error("Enter on /quit did not quit the program")
	}
}

// TestACommandLineWithAnArgumentKeepsItsCommand covers the menu telling the
// truth while an argument is typed: "/model some-id" is still the /model
// command line, and saying nothing matches would be a lie.
func TestACommandLineWithAnArgumentKeepsItsCommand(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/model some-id")

	highlighted, ok := m.menu.Highlighted()
	if !ok {
		t.Fatal("nothing highlighted, want the command the line names")
	}
	if highlighted.Name != "model" {
		t.Errorf("highlighted %q, want %q", highlighted.Name, "model")
	}
	if view := m.View().Content; strings.Contains(view, "no matching command") {
		t.Errorf("menu says nothing matches a valid command line:\n%s", view)
	}
}

// TestArrowsLeaveATypedArgumentAlone is what the guard above is for, seen from
// the outside: /model is the only match once an argument is being typed, so an
// arrow key has nowhere to go and must not rewrite the line.
func TestArrowsLeaveATypedArgumentAlone(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/model some-id")

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})

	if want := "/model some-id"; m.input.Value() != want {
		t.Errorf("input = %q, want %q", m.input.Value(), want)
	}
}

// TestEnterPassesTheArgument covers "/model   some-id": the argument is the
// command's input rather than part of its name, and the spacing between the two
// is the user's business. It uses a registry of its own, since the real
// commands do more than record what they were given.
func TestEnterPassesTheArgument(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"no argument", "/echo", ""},
		{"argument", "/echo some-id", "some-id"},
		{"extra spaces", "/echo   some-id  ", "some-id"},
		{"trailing space alone", "/echo ", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got string
			registry := commands.NewRegistry(commands.Command{
				Name:        "echo",
				Description: "records its argument",
				Execute:     func(arg string) tea.Cmd { got = arg; return nil },
			})
			m := newModel(agent.New(nil, nil), config.Config{}, registry)
			m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
			m = typeText(t, m, test.line)

			updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

			if got != test.want {
				t.Errorf("argument = %q, want %q", got, test.want)
			}
		})
	}
}

// TestEnterOnATypoDoesNotRunTheNearestCommand is the point of matching on a
// prefix: what Enter runs is spelled out far enough to be recognised.
func TestEnterOnATypoDoesNotRunTheNearestCommand(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/qut")

	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if quits(cmd) {
		t.Fatal("Enter on /qut quit the program, want the typo reported")
	}
	if got := printed(t, cmd); !strings.Contains(got, "unknown command: /qut") {
		t.Errorf("printed %q, want it to name the typo", got)
	}
}

// TestEnterOnUnknownCommandShowsAnError uses a line the menu cannot match at
// all: with nothing highlighted, Enter falls back to running what was typed.
func TestEnterOnUnknownCommandShowsAnError(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/zzz")

	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := printed(t, cmd); !strings.Contains(got, "unknown command: /zzz") {
		t.Errorf("printed %q, want it to name the unknown command", got)
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared after a rejected command", m.input.Value())
	}
	if m.state != uiStateIdle {
		t.Error("an unknown command started a turn, want it never to reach the agent")
	}
}

// TestQuitCommandWorksWhileProcessing covers the point of /quit: getting out
// without waiting for the model to finish answering.
func TestQuitCommandWorksWhileProcessing(t *testing.T) {
	m := newSizedModel(t)
	m.state = uiStateProcessing
	m = typeText(t, m, "/quit")

	if !m.menu.Open() {
		t.Error("menu does not open while a turn is in flight")
	}

	_, cmd := enter(t, m)
	if !quits(cmd) {
		t.Error("Enter on /quit did not quit while processing")
	}
}

func TestEnterStillSendsPlainInputOnly(t *testing.T) {
	m := newSizedModel(t)
	m.state = uiStateProcessing
	m = typeText(t, m, "hello")

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.input.Value() != "hello" {
		t.Errorf("input = %q, want it kept while the agent is busy", m.input.Value())
	}
}

func TestViewPutsCursorBelowTheMenu(t *testing.T) {
	m := typeText(t, newSizedModel(t), commands.Prefix)

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("view has no cursor, want one on the input row")
	}
	wantY := lipgloss.Height(m.menu.View())
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (below the menu)", view.Cursor.Y, wantY)
	}
}

// failingProvider fails every round of inference, so a turn driven through the
// real program reaches the error path.
type failingProvider struct{ err error }

func (p failingProvider) Models(ctx context.Context) ([]agent.Model, error) { return nil, p.err }

func (p failingProvider) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, 1)
	events <- agent.ErrorEvent{Err: p.err}
	close(events)
	return events
}

// TestProgramFitsErrorToTerminal drives the whole program — real event loop,
// real terminal size, real keystrokes — rather than calling Update directly, so
// it also covers View assembling the viewport and input together. That seam is
// what made the input row overflow the terminal and drag every other row with
// it, which testing Update alone did not reach.
func TestProgramFitsErrorToTerminal(t *testing.T) {
	// 20 is the narrowest box worth drawing; below that the width is clamped up
	// and overflow is accepted, so it is not a case this can assert on.
	for _, width := range []int{20, 30, 40, 80, 120} {
		t.Run(fmt.Sprintf("width%d", width), func(t *testing.T) {
			a := agent.New(failingProvider{err: errors.New("429 rate_limit_error: too many requests, slow down")}, nil)
			tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands()), teatest.WithInitialTermSize(width, 20))

			tm.Type("hi")
			tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

			teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
				return bytes.Contains(out, []byte("Error:"))
			})

			if err := tm.Quit(); err != nil {
				t.Fatalf("quitting the program: %v", err)
			}
			final, ok := tm.FinalModel(t).(model)
			if !ok {
				t.Fatalf("final model is %T, want model", tm.FinalModel(t))
			}

			// The error is printed into the scrollback, so the frame itself only
			// has to stay inside the terminal.
			assertFitsWidth(t, final.View().Content, width)
		})
	}
}

func TestConfigCommandOpensTheView(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/config")

	m, _ = enter(t, m)

	if !m.configVisible {
		t.Fatal("/config did not open the config view")
	}
	if view := m.View().Content; !strings.Contains(view, "configuration") {
		t.Errorf("view does not show the configuration:\n%s", view)
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared when the view opened", m.input.Value())
	}
}

func TestConfigViewShowsLoadedConfigWithoutTheKey(t *testing.T) {
	const key = "sk-ant-do-not-print-me"

	m := newModel(agent.New(nil, nil), config.Config{
		AnthropicAPIKey: config.Secret(key),
		Path:            "/tmp/elencode/config.json",
		APIKeyFromEnv:   true,
	}, defaultCommands())
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.configVisible = true

	view := m.View().Content
	if strings.Contains(view, key) {
		t.Error("config view shows the raw API key")
	}
	if !strings.Contains(view, "/tmp/elencode/config.json") {
		t.Errorf("config view does not show the config path:\n%s", view)
	}
}

func TestEscClosesTheConfigView(t *testing.T) {
	m := newSizedModel(t)
	m.configVisible = true

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.configVisible {
		t.Error("config view still open after Esc")
	}
}

func TestCtrlCClosesTheConfigViewWithoutQuitting(t *testing.T) {
	m := newSizedModel(t)
	m.configVisible = true

	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	if m.configVisible {
		t.Error("config view still open after ctrl+c")
	}
	if quits(cmd) {
		t.Error("ctrl+c quit the program, want it to only close the config view")
	}
}

func TestConfigViewSwallowsOtherKeys(t *testing.T) {
	m := newSizedModel(t)
	m.configVisible = true

	// The input is not on screen, so a forwarded keystroke would be typed blind
	m = typeText(t, m, "hello")

	if m.input.Value() != "" {
		t.Errorf("input = %q, want keystrokes swallowed while the config view is open", m.input.Value())
	}
	if !m.configVisible {
		t.Error("typing closed the config view")
	}
}

func TestConfigViewHidesTheTranscriptAndInput(t *testing.T) {
	m := newSizedModel(t)
	m.state = uiStateProcessing
	m = update(t, m, streamEventMsg{event: agent.TextDeltaEvent{Text: "half a sentence"}})
	m.configVisible = true

	view := m.View()
	if strings.Contains(view.Content, "half a sentence") {
		t.Errorf("config view does not replace the frame:\n%s", view.Content)
	}
	if view.Cursor != nil {
		t.Error("config view has a cursor, want none while the input is hidden")
	}
}

// ctrlC is the key press the quit tests send
var ctrlC = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

func TestFirstCtrlCArmsRatherThanQuitting(t *testing.T) {
	m := newSizedModel(t)

	m, cmd := updateCmd(t, m, ctrlC)

	if quits(cmd) {
		t.Error("the first ctrl+c quit, want it to arm and wait for a second press")
	}
	if !m.quitArmed {
		t.Error("the first ctrl+c did not arm quit")
	}
	if view := m.View().Content; !strings.Contains(view, "press ctrl+c again to exit") {
		t.Errorf("view does not tell the user how to exit:\n%s", view)
	}
}

func TestSecondCtrlCQuits(t *testing.T) {
	m := newSizedModel(t)

	m = update(t, m, ctrlC)
	_, cmd := updateCmd(t, m, ctrlC)

	if !quits(cmd) {
		t.Error("the second ctrl+c did not quit")
	}
}

func TestAnyOtherKeyDisarmsQuit(t *testing.T) {
	m := newSizedModel(t)
	m = update(t, m, ctrlC)

	m = typeText(t, m, "a")

	if m.quitArmed {
		t.Error("quit still armed after another keystroke")
	}
	if view := m.View().Content; strings.Contains(view, "press ctrl+c again to exit") {
		t.Errorf("the exit hint survived a disarming keystroke:\n%s", view)
	}
	// The keystroke that disarms is still the user's input, not swallowed
	if m.input.Value() != "a" {
		t.Errorf("input = %q, want the disarming keystroke to still be typed", m.input.Value())
	}
}

func TestQuitDisarmsOnTimeout(t *testing.T) {
	m := newSizedModel(t)
	m = update(t, m, ctrlC)

	m = update(t, m, quitDisarmMsg{generation: m.quitGeneration})

	if m.quitArmed {
		t.Error("quit still armed after its disarm message")
	}
}

// TestStaleDisarmDoesNotDisarmAReArmedQuit covers the sequence the generation
// counter exists for: arm, disarm by typing, arm again, and only then does the
// first press's timer fire. Without the counter that stale message disarms a
// quit the user just armed, and their second ctrl+c would silently do nothing.
func TestStaleDisarmDoesNotDisarmAReArmedQuit(t *testing.T) {
	m := newSizedModel(t)

	m = update(t, m, ctrlC)
	stale := m.quitGeneration
	m = typeText(t, m, "a")
	m = update(t, m, ctrlC)

	m = update(t, m, quitDisarmMsg{generation: stale})

	if !m.quitArmed {
		t.Fatal("a stale disarm message disarmed a freshly armed quit")
	}
	_, cmd := updateCmd(t, m, ctrlC)
	if !quits(cmd) {
		t.Error("ctrl+c after the stale disarm did not quit")
	}
}

// TestCtrlCDuringATurnCancelsWithoutArming keeps ctrl+c meaning "interrupt"
// while the agent is working: arming there would make an interrupt look like a
// half-pressed quit.
func TestCtrlCDuringATurnCancelsWithoutArming(t *testing.T) {
	m := newModel(agent.New(failingProvider{err: errors.New("boom")}, nil), config.Config{}, defaultCommands())
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.input.SetValue("hello")
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.state != uiStateProcessing {
		t.Fatalf("state = %v, want a turn in flight before ctrl+c", m.state)
	}

	m, cmd := updateCmd(t, m, ctrlC)

	if quits(cmd) {
		t.Error("ctrl+c during a turn quit, want it to cancel the turn")
	}
	if m.quitArmed {
		t.Error("ctrl+c during a turn armed quit, want it to only interrupt")
	}
}

// TestProgramQuitsOnQuitCommand drives the whole program rather than calling
// Update, so it covers the command actually ending the event loop: Update
// returning tea.Quit is not by itself proof the program exits.
func TestProgramQuitsOnQuitCommand(t *testing.T) {
	a := agent.New(failingProvider{err: errors.New("never asked")}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands()), teatest.WithInitialTermSize(80, 20))

	tm.Type("/")
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("exit elencode"))
	})

	tm.Type("quit")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Fails the test by timing out if the program is still running
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestProgramPicksACommandWithTheArrowKeys drives the whole program: the
// highlight travels to the input as a message, so only the real event loop
// shows a command being completed without its name being typed.
func TestProgramPicksACommandWithTheArrowKeys(t *testing.T) {
	a := agent.New(failingProvider{err: errors.New("never asked")}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands()), teatest.WithInitialTermSize(80, 20))

	tm.Type("/")
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("exit elencode"))
	})

	// Down the list to /quit, the last of the three
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return strings.Contains(ansi.Strip(string(out)), menu.MarkerSelected+" /quit")
	})

	if err := tm.Quit(); err != nil {
		t.Fatalf("quitting the program: %v", err)
	}
	final := tm.FinalModel(t).(model)
	if want := "/quit"; final.input.Value() != want {
		t.Errorf("input = %q, want %q: the arrows must complete into it", final.input.Value(), want)
	}
}

// TestProgramQuitsOnSecondCtrlC drives the whole program: the first press must
// not end the event loop and the second must.
func TestProgramQuitsOnSecondCtrlC(t *testing.T) {
	a := agent.New(failingProvider{err: errors.New("never asked")}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands()), teatest.WithInitialTermSize(80, 20))

	tm.Send(ctrlC)
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("press ctrl+c again to exit"))
	})

	tm.Send(ctrlC)
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// assertFitsWidth fails if any line would be clipped by a terminal this wide
func assertFitsWidth(t *testing.T, view string, width int) {
	t.Helper()

	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("line %d is %d columns wide, want <= %d:\n%s", i, got, width, line)
		}
	}
}

type errStub struct{}

func (errStub) Error() string { return "stub failure" }

// modelProvider serves a fixed model list.
type modelProvider struct {
	models []agent.Model
}

func (p *modelProvider) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, 1)
	events <- agent.ErrorEvent{Err: errors.New("never asked")}
	close(events)
	return events
}

func (p *modelProvider) Models(ctx context.Context) ([]agent.Model, error) { return p.models, nil }

var testModels = []agent.Model{
	{ID: "model-one", DisplayName: "Model One"},
	{ID: "model-two", DisplayName: "Model Two"},
}

// newPickerModel builds a sized model whose config points at a writable file,
// so selecting a model can save without failing on the path.
func newPickerModel(t *testing.T, provider agent.Provider) model {
	t.Helper()

	file := path.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(`{"anthropic_api_key":"key"}`), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	m := newModel(agent.New(provider, nil), config.Config{Path: file, AnthropicAPIKey: "key"}, defaultCommands())
	return update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
}

// modelsFrom runs cmd and returns the model list message it produced. It
// follows a batch, since the fetch is issued alongside the spinner tick.
func modelsFrom(t *testing.T, cmd tea.Cmd) modelsMsg {
	t.Helper()

	if cmd == nil {
		t.Fatal("command is nil, want one that fetches the model list")
	}
	switch msg := cmd().(type) {
	case modelsMsg:
		return msg
	case tea.BatchMsg:
		for _, sub := range msg {
			if got, ok := sub().(modelsMsg); ok {
				return got
			}
		}
	}
	t.Fatal("command produced no model list message")
	return modelsMsg{}
}

func TestModelCommandFetchesTheModelList(t *testing.T) {
	m := typeText(t, newPickerModel(t, &modelProvider{models: testModels}), "/model")

	m, cmd := enter(t, m)

	if !m.modelsLoading {
		t.Error("/model did not start loading the model list")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared once the command ran", m.input.Value())
	}
	got := modelsFrom(t, cmd)
	if len(got.models) != len(testModels) {
		t.Errorf("fetched %d models, want %d", len(got.models), len(testModels))
	}
}

func TestModelListOpensThePicker(t *testing.T) {
	m := newPickerModel(t, &modelProvider{models: testModels})

	m = update(t, m, modelsMsg{models: testModels})

	if m.modelsLoading {
		t.Error("still loading once the list arrived")
	}
	view := m.View().Content
	for _, want := range testModels {
		if !strings.Contains(view, want.ID) {
			t.Errorf("picker does not offer %q:\n%s", want.ID, view)
		}
	}
}

// TestModelPickerStartsOnTheCurrentModel saves the user from hunting for where
// they already are in a list of twenty.
func TestModelPickerStartsOnTheCurrentModel(t *testing.T) {
	m := newPickerModel(t, &modelProvider{models: testModels})
	m.config.Model = "model-two"

	m = update(t, m, modelsMsg{models: testModels})

	// Where the highlight starts is the list's own business; what it means
	// here is that Enter, pressed without arrowing, keeps the model in use.
	m, _ = updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.config.Model != "model-two" {
		t.Errorf("config model = %q, want the picker to open on the model in use", m.config.Model)
	}
}

func TestEffectiveDefaultModelIsShownAndSelected(t *testing.T) {
	m := newPickerModel(t, &modelProvider{models: testModels})
	m.config = configWithEffectiveModel(m.config, agent.Model{ID: "model-two"})

	if view := renderConfig(m.config, 80); !strings.Contains(view, "model-two") {
		t.Errorf("config view does not show the effective model:\n%s", view)
	}

	m = update(t, m, modelsMsg{models: testModels})
	m, _ = updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.config.Model != "model-two" {
		t.Errorf("config model = %q, want the list to open on the effective default", m.config.Model)
	}
}

func TestEnterSelectsTheHighlightedModel(t *testing.T) {
	provider := &modelProvider{models: testModels}
	m := update(t, newPickerModel(t, provider), modelsMsg{models: testModels})

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.config.Model != "model-two" {
		t.Errorf("config model = %q, want %q", m.config.Model, "model-two")
	}
	if m.models.Open() {
		t.Error("list still open after a choice")
	}
	// The arrow keys put the id there; the choice is made, so it goes
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared once the model was chosen", m.input.Value())
	}
}

// TestSelectingAModelPersistsIt covers the choice outliving the session: the
// config file is what the next start reads.
func TestSelectingAModelPersistsIt(t *testing.T) {
	m := update(t, newPickerModel(t, &modelProvider{models: testModels}), modelsMsg{models: testModels})

	m, _ = updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	body, err := os.ReadFile(m.config.Path)
	if err != nil {
		t.Fatalf("reading the saved config: %v", err)
	}
	if !strings.Contains(string(body), "model-one") {
		t.Errorf("config file does not name the chosen model:\n%s", body)
	}
}

// TestSelectingAModelClearsTheTranscript covers what a switch means: the
// conversation so far belongs to the model that produced it.

func TestModelArgumentSelectsWithoutOpeningThePicker(t *testing.T) {
	provider := &modelProvider{models: testModels}
	m := typeText(t, newPickerModel(t, provider), "/model model-two")

	m, cmd := enter(t, m)
	m = update(t, m, modelsFrom(t, cmd))

	if m.models.Open() {
		t.Error("list opened for a model named on the command line")
	}
}

func TestUnknownModelArgumentIsReported(t *testing.T) {
	provider := &modelProvider{models: testModels}
	m := typeText(t, newPickerModel(t, provider), "/model model-nine")

	m, cmd := enter(t, m)
	_, cmd = updateCmd(t, m, modelsFrom(t, cmd))

	if got := printed(t, cmd); !strings.Contains(got, "model-nine") {
		t.Errorf("printed %q, want it to name the model asked for", got)
	}
}

func TestFailureToListModelsIsReported(t *testing.T) {
	m := newPickerModel(t, failingProvider{err: errors.New("503 overloaded")})

	m, cmd := updateCmd(t, m, modelsMsg{err: errors.New("503 overloaded")})

	if m.modelsLoading {
		t.Error("still loading after the request failed")
	}
	if m.models.Open() {
		t.Error("list opened with no models to show")
	}
	if got := printed(t, cmd); !strings.Contains(got, "503 overloaded") {
		t.Errorf("printed %q, want it to say why the model list failed", got)
	}
}

func TestEscClosesTheModelPicker(t *testing.T) {
	provider := &modelProvider{models: testModels}
	m := update(t, newPickerModel(t, provider), modelsMsg{models: testModels})
	// Arrowing first, so there is something left in the input to clear
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.models.Open() {
		t.Error("list still open after Esc")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared with the list", m.input.Value())
	}
}

// TestOpeningTheModelListTakesTheInputOver covers anything typed while the
// list was still loading: it belongs to the command line the list replaces, and
// leaving it there would leave the command menu open behind the list.
func TestOpeningTheModelListTakesTheInputOver(t *testing.T) {
	m := typeText(t, newPickerModel(t, &modelProvider{models: testModels}), commands.Prefix)

	m = update(t, m, modelsMsg{models: testModels})

	if m.menu.Open() {
		t.Error("the command menu is still open behind the model list")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want the list to start on an empty one", m.input.Value())
	}
}

// TestTypingNarrowsTheModelList is what the input is for while the list is up:
// the API offers more models than the arrow keys are worth.
func TestTypingNarrowsTheModelList(t *testing.T) {
	m := update(t, newPickerModel(t, &modelProvider{models: testModels}), modelsMsg{models: testModels})

	m = typeText(t, m, "two")

	view := m.View().Content
	if !strings.Contains(view, "model-two") {
		t.Errorf("list does not show the model typed for:\n%s", view)
	}
	if strings.Contains(view, "model-one") {
		t.Errorf("list still shows a model the query rules out:\n%s", view)
	}
}

// TestNarrowingTheModelListToNothingSaysSo covers the empty message doing
// double duty now that there is a filter: an empty list is usually the query's
// doing rather than the API's.
func TestNarrowingTheModelListToNothingSaysSo(t *testing.T) {
	m := update(t, newPickerModel(t, &modelProvider{models: testModels}), modelsMsg{models: testModels})

	m = typeText(t, m, "zzz")

	if view := m.View().Content; !strings.Contains(view, "no matching model") {
		t.Errorf("list does not say nothing matched:\n%s", view)
	}
}

// TestSlashDoesNotOpenTheMenuBehindTheModelList covers the cost of letting
// keystrokes reach the input: the input is also what opens the command menu.
func TestSlashDoesNotOpenTheMenuBehindTheModelList(t *testing.T) {
	m := update(t, newPickerModel(t, &modelProvider{models: testModels}), modelsMsg{models: testModels})

	m = typeText(t, m, commands.Prefix)

	if m.menu.Open() {
		t.Error("the command menu opened behind the model list")
	}
	if !m.models.Open() {
		t.Error("typing closed the model list")
	}
}

func TestModelPickerFitsTheTerminal(t *testing.T) {
	const height = 20

	var many []agent.Model
	for i := range 40 {
		many = append(many, agent.Model{ID: fmt.Sprintf("model-%d", i), DisplayName: "Model"})
	}
	m := update(t, newPickerModel(t, &modelProvider{models: many}), modelsMsg{models: many})

	if got := lipgloss.Height(m.View().Content); got > height {
		t.Errorf("view is %d rows tall with the picker open, want <= %d", got, height)
	}
}

func TestViewShowsSpinnerWhileLoadingModels(t *testing.T) {
	m := newPickerModel(t, &modelProvider{models: testModels})
	m.modelsLoading = true

	if view := m.View().Content; !strings.Contains(view, "loading models") {
		t.Errorf("view does not say the model list is loading:\n%s", view)
	}
}

// printed returns the text cmd would insert above the frame. bubbletea's print
// message type is unexported, so the text is recovered by formatting it — %v
// renders the single-field struct as "{the text}" — rather than by a type
// assertion.
//
// Only for a command that does nothing but print. Unwrapping a tea.Sequence
// would mean running the commands behind it, and the one that waits for the
// next event blocks.
func printed(t *testing.T, cmd tea.Cmd) string {
	t.Helper()

	if cmd == nil {
		t.Fatal("command is nil, want one that prints")
	}

	// Run it off the test goroutine. tea.Sequence collapses to its only non-nil
	// command, so a sequence whose print is empty is just the command that waits
	// for the next stream event — which blocks here until the test times out.
	// Failing beats hanging.
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case msg := <-done:
		body := fmt.Sprintf("%v", msg)
		return strings.TrimSuffix(strings.TrimPrefix(body, "{"), "}")
	case <-time.After(time.Second):
		t.Fatal("command did not return: printed is for a command that prints and nothing else")
		return ""
	}
}

func TestViewShowsTheRowBeingWrittenInto(t *testing.T) {
	m := newSizedModel(t)

	m.state = uiStateProcessing
	m = update(t, m, streamEventMsg{event: agent.TextDeltaEvent{Text: "half a sen"}})

	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "half a sen") {
		t.Errorf("frame does not show the text being streamed:\n%s", view)
	}
}

// scriptedProvider streams a fixed reply, so a test can drive a whole turn
// through the real program.
type scriptedProvider struct{ deltas []string }

func (p scriptedProvider) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, len(p.deltas)+1)
	text := ""
	for _, delta := range p.deltas {
		text += delta
		events <- agent.TextDeltaEvent{Text: delta}
	}
	events <- agent.ResponseEvent{Response: agent.Response{
		Message:    agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: text}}},
		StopReason: agent.StopReasonEndTurn,
	}}
	close(events)
	return events
}

func (p scriptedProvider) Models(ctx context.Context) ([]agent.Model, error) { return nil, nil }

// TestProgramPrintsThePromptAndTheReply drives the whole program, which is the
// only place the printing is real: Update hands back commands, and it is
// bubbletea that turns them into lines above the frame.
func TestProgramPrintsThePromptAndTheReply(t *testing.T) {
	a := agent.New(scriptedProvider{deltas: []string{"Hel", "lo there, ", "how can I help?"}}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands()), teatest.WithInitialTermSize(80, 20))

	tm.Type("hi")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains([]byte(ansi.Strip(string(out))), []byte("how can I help?"))
	}, teatest.WithDuration(5*time.Second))

	if err := tm.Quit(); err != nil {
		t.Fatalf("quitting the program: %v", err)
	}
	// The prompt and the reply are printed, so neither is in the final frame
	final := tm.FinalModel(t).(model)
	if view := final.View().Content; strings.Contains(view, "how can I help?") {
		t.Errorf("the reply is still in the frame, want it printed above:\n%s", view)
	}
}

// TestSelectingAModelSaysSo covers the only feedback there is: the switch
// changes nothing on screen by itself, since what is already printed stays.
func TestSelectingAModelSaysSo(t *testing.T) {
	m := update(t, newPickerModel(t, &modelProvider{models: testModels}), modelsMsg{models: testModels})

	_, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	got := printed(t, cmd)
	if !strings.Contains(got, "model-one") {
		t.Errorf("printed %q, want it to name the model switched to", got)
	}
	// The conversation stays on screen but is no longer sent, which is only
	// obvious if the notice says so.
	if !strings.Contains(got, "context cleared") {
		t.Errorf("printed %q, want it to say the context was cleared", got)
	}
}

// TestHeaderSpansTheTerminal covers why the header cannot be printed from Init:
// the terminal width is not known until the first WindowSizeMsg arrives.
func TestHeaderSpansTheTerminal(t *testing.T) {
	const width = 72

	_, cmd := updateCmd(t, newTestModel(), tea.WindowSizeMsg{Width: width, Height: 20})

	got := printed(t, cmd)
	if lipgloss.Width(got) != width {
		t.Errorf("header is %d columns wide, want the full %d:\n%s", lipgloss.Width(got), width, got)
	}
	if !strings.Contains(got, banner) {
		t.Errorf("header does not carry the title %q:\n%s", banner, got)
	}
}

func TestHeaderIsPrintedOnce(t *testing.T) {
	m := newSizedModel(t)

	// A resize is not a new session, so it must not print a second header
	_, cmd := updateCmd(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	if cmd != nil {
		t.Errorf("resizing printed %q, want the header printed only at startup", printed(t, cmd))
	}
}

func TestThinkingStreamsIntoTheFrame(t *testing.T) {
	m := newSizedModel(t)

	m.state = uiStateProcessing
	m = update(t, m, streamEventMsg{event: agent.ThinkingDeltaEvent{Text: "let me check"}})

	// Compared against the rendered thinking block rather than the bare text, so
	// this also catches reasoning painted as though it were the answer.
	rows := strings.Split(transcript.Block(agent.ThinkingBlock{Thinking: "let me check"}, agent.RoleAssistant, 80), "\n")
	want := rows[len(rows)-1]
	if view := m.View().Content; !strings.Contains(view, want) {
		t.Errorf("frame does not show the reasoning being streamed:\n%s\nwant a row %q", view, want)
	}
}

// TestAnswerAfterThinkingFinishesTheThinkingBlock covers the switch from
// reasoning to answer: they are separate blocks, so the reasoning has to be
// printed in full before the answer starts being written.
func TestAnswerAfterThinkingFinishesTheThinkingBlock(t *testing.T) {
	m := newSizedModel(t)
	m.stream.Delta("let me check the file", true)

	settled := m.stream.Delta("It is in internal/agent.", false)

	if !strings.Contains(settled, "let me check the file") {
		t.Errorf("settled rows = %q, want the reasoning printed once the answer started", settled)
	}
	// The frame has moved on to the answer
	view := m.View().Content
	if strings.Contains(view, "let me check the file") {
		t.Errorf("frame still shows the reasoning after the answer started:\n%s", view)
	}
	if !strings.Contains(ansi.Strip(view), "It is in internal/agent.") {
		t.Errorf("frame does not show the answer:\n%s", view)
	}
}
