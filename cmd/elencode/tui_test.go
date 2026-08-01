package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
)

// newTestModel builds a model backed by an agent that is never Run, so it needs
// no provider: only the transcript is read during rendering.
func newTestModel() model {
	return newModel(agent.New(nil, nil), config.Config{})
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

	m = update(t, m, streamEventMsg{agent.TextDeltaEvent{Text: "Hel"}})
	m = update(t, m, streamEventMsg{agent.TextDeltaEvent{Text: "lo"}})

	if m.partial != "Hello" {
		t.Errorf("partial = %q, want %q", m.partial, "Hello")
	}
}

func TestUpdateClearsPartialOnMessageEvent(t *testing.T) {
	m := newTestModel()
	m = update(t, m, streamEventMsg{agent.TextDeltaEvent{Text: "Hello"}})

	// Once the message is in the transcript, the partial copy must go, or the
	// same text renders twice.
	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "Hello"}}}
	m = update(t, m, streamEventMsg{agent.MessageEvent{Message: landed}})

	if m.partial != "" {
		t.Errorf("partial = %q, want it cleared once the message landed", m.partial)
	}
}

func TestUpdateReturnsToIdleWhenStreamCloses(t *testing.T) {
	m := newTestModel()
	m.state = uiStateProcessing
	m.partial = "half a sentence"

	m = update(t, m, streamClosedMsg{})

	if m.state != uiStateIdle {
		t.Errorf("state = %v, want uiStateIdle", m.state)
	}
	if m.partial != "" {
		t.Errorf("partial = %q, want it cleared when the turn ends", m.partial)
	}
}

func TestUpdateRecordsStreamError(t *testing.T) {
	m := newTestModel()

	wantErr := errStub{}
	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: wantErr}})

	if m.err != wantErr {
		t.Errorf("err = %v, want %v", m.err, wantErr)
	}
}

func TestUpdateShowsErrorInViewport(t *testing.T) {
	m := newTestModel()
	// Wide enough that the boxed error is not truncated to the viewport width
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: errStub{}}})

	if view := m.viewport.View(); !strings.Contains(view, "Error: stub failure") {
		t.Errorf("viewport does not show the labelled error:\n%s", view)
	}
}

func TestUpdateKeepsErrorVisibleAfterTurnEnds(t *testing.T) {
	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: errStub{}}})

	// The channel closes right after a failure, so clearing the error here
	// would make it flash and vanish before it could be read.
	m = update(t, m, streamClosedMsg{})

	if m.err == nil {
		t.Fatal("err was cleared when the turn ended, want it kept until the next turn")
	}
	if view := m.viewport.View(); !strings.Contains(view, "Error: stub failure") {
		t.Errorf("viewport does not show the labelled error:\n%s", view)
	}
}

func TestUpdateKeepsBannerBeforeAnythingIsSaid(t *testing.T) {
	m := newTestModel()

	// The first WindowSizeMsg arrives at startup and repaints, so an empty
	// transcript must not blank the screen.
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})

	if !strings.Contains(m.viewport.View(), banner) {
		t.Errorf("viewport = %q, want it to still show the banner", m.viewport.View())
	}
}

// TestUpdateShowsUserMessageAsSoonAsItIsSent asserts the message is in the
// viewport the instant Enter is handled, before any Event has been read off
// the turn's channel: agent.Run appends the user message to the context
// window synchronously, ahead of the goroutine that talks to the provider.
func TestUpdateShowsUserMessageAsSoonAsItIsSent(t *testing.T) {
	m := newModel(agent.New(failingProvider{err: errors.New("boom")}, nil), config.Config{})
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.input.SetValue("hello there")

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if view := m.viewport.View(); !strings.Contains(view, "hello there") {
		t.Errorf("viewport does not show the user's message right after Enter:\n%s", view)
	}
}

func TestUpdateFitsErrorToNarrowTerminal(t *testing.T) {
	const width = 40

	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: width, Height: 20})
	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: errStub{}}})

	assertFitsWidth(t, m.viewport.View(), width)
}

func TestUpdateReflowsWhenTerminalShrinks(t *testing.T) {
	const narrow = 40

	m := newTestModel()
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: errStub{}}})

	// Content laid out for the old width would be clipped at the new one, so a
	// resize has to repaint rather than only resize the viewport.
	m = update(t, m, tea.WindowSizeMsg{Width: narrow, Height: 20})

	assertFitsWidth(t, m.viewport.View(), narrow)
	if !strings.Contains(m.viewport.View(), "stub failure") {
		t.Errorf("viewport lost the error after resizing:\n%s", m.viewport.View())
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
	// textinput reports its cursor relative to itself, but View stacks the input
	// below the viewport, so the row has to be offset or the terminal blinks at
	// the top of the screen instead.
	wantY := lipgloss.Height(m.viewport.View())
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

// TestUpdateKeepsViewportUsableInTinyTerminal covers terminals too short to
// hold the rows below the viewport: subtracting them leaves nothing, and a
// viewport sized zero or less has no transcript area at all.
func TestUpdateKeepsViewportUsableInTinyTerminal(t *testing.T) {
	for _, height := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("height%d", height), func(t *testing.T) {
			m := newTestModel()
			m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: height})

			if got := m.viewport.Height(); got < 1 {
				t.Errorf("viewport height = %d, want at least 1 row of transcript", got)
			}
		})
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
	wantY := lipgloss.Height(m.viewport.View()) + lipgloss.Height(m.spinnerLine())
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (below viewport and spinner)", view.Cursor.Y, wantY)
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
	m := typeText(t, newSizedModel(t), commandPrefix)

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

func TestEscDismissesTheMenuForTheRestOfTheLine(t *testing.T) {
	m := typeText(t, newSizedModel(t), commandPrefix)

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.menuVisible() {
		t.Error("menu still visible after Esc")
	}

	// Typing on must not revive it, or Esc only hides the menu for one keystroke
	m = typeText(t, m, "q")
	if m.menuVisible() {
		t.Errorf("menu came back after typing, input = %q", m.input.Value())
	}
	if m.input.Value() != "/q" {
		t.Errorf("input = %q, want %q: Esc must not swallow later keystrokes", m.input.Value(), "/q")
	}
}

func TestMenuReopensOnANewCommandLine(t *testing.T) {
	m := typeText(t, newSizedModel(t), commandPrefix)
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	// Backspacing away the slash ends the dismissed line; the next one starts fresh
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = typeText(t, m, commandPrefix)

	if !m.menuVisible() {
		t.Error("menu stayed dismissed on a new command line")
	}
}

func TestTabCompletesTheHighlightedCommand(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/qu")

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	if m.input.Value() != "/quit" {
		t.Errorf("input = %q, want %q", m.input.Value(), "/quit")
	}
}

func TestArrowKeysDoNotReachTheInputWhileTheMenuIsOpen(t *testing.T) {
	m := typeText(t, newSizedModel(t), commandPrefix)

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})

	if m.input.Value() != commandPrefix {
		t.Errorf("input = %q, want %q: arrows must drive the menu, not the input", m.input.Value(), commandPrefix)
	}
	if m.menu.Index() != 0 {
		t.Errorf("menu index = %d, want 0 with a single match", m.menu.Index())
	}
}

func TestTypingResetsTheHighlight(t *testing.T) {
	m := typeText(t, newSizedModel(t), commandPrefix)
	m.menu.Select(1) // as if the user had arrowed down a longer match set

	m = typeText(t, m, "q")

	// The match set just changed, so the old index may point at another command
	if m.menu.Index() != 0 {
		t.Errorf("menu index = %d, want it reset to 0 when the matches change", m.menu.Index())
	}
}

func TestEnterRunsQuitCommand(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/quit")

	_, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if !quits(cmd) {
		t.Error("Enter on /quit did not quit the program")
	}
}

func TestEnterOnUnknownCommandShowsAnError(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/qut")

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.err == nil {
		t.Fatal("Enter on an unknown command recorded no error")
	}
	if !strings.Contains(m.viewport.View(), "unknown command: /qut") {
		t.Errorf("viewport does not name the unknown command:\n%s", m.viewport.View())
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

	if !m.menuVisible() {
		t.Error("menu does not open while a turn is in flight")
	}

	_, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !quits(cmd) {
		t.Error("Enter on /quit did not quit while processing")
	}
}

func TestEnterStillSendsPlainInputOnly(t *testing.T) {
	m := newSizedModel(t)
	m.state = uiStateProcessing
	m = typeText(t, m, "hello")

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.err != nil {
		t.Errorf("plain input while processing produced an error: %v", m.err)
	}
	if m.input.Value() != "hello" {
		t.Errorf("input = %q, want it kept while the agent is busy", m.input.Value())
	}
}

// TestMenuShrinksTheViewport guards the layout: the menu appears between window
// size messages, so the viewport has to be re-measured when it opens or the
// frame grows taller than the terminal.
func TestMenuShrinksTheViewport(t *testing.T) {
	const height = 20

	m := newSizedModel(t)
	before := m.viewport.Height()

	m = typeText(t, m, commandPrefix)

	if got := m.viewport.Height(); got >= before {
		t.Errorf("viewport height = %d, want less than %d once the menu opened", got, before)
	}
	if got := lipgloss.Height(m.View().Content); got > height {
		t.Errorf("view is %d rows tall, want <= %d", got, height)
	}
}

func TestMenuRestoresTheViewportWhenClosed(t *testing.T) {
	m := newSizedModel(t)
	before := m.viewport.Height()

	m = typeText(t, m, commandPrefix)
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})

	if got := m.viewport.Height(); got != before {
		t.Errorf("viewport height = %d, want %d restored once the menu closed", got, before)
	}
}

func TestViewPutsCursorBelowTheMenu(t *testing.T) {
	m := typeText(t, newSizedModel(t), commandPrefix)

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("view has no cursor, want one on the input row")
	}
	wantY := lipgloss.Height(m.viewport.View()) + lipgloss.Height(m.menuView())
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (below viewport and menu)", view.Cursor.Y, wantY)
	}
}

// failingProvider fails every round of inference, so a turn driven through the
// real program reaches the error path.
type failingProvider struct{ err error }

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
			tm := teatest.NewTestModel(t, newModel(a, config.Config{}), teatest.WithInitialTermSize(width, 20))

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

			view := final.View().Content
			if !strings.Contains(view, "rate_limit_error") {
				t.Errorf("view does not show the error:\n%s", view)
			}
			assertFitsWidth(t, view, width)
		})
	}
}

func TestConfigCommandOpensTheView(t *testing.T) {
	m := typeText(t, newSizedModel(t), "/config")

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	})
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
	m = update(t, m, streamEventMsg{agent.ErrorEvent{Err: errStub{}}})
	m.configVisible = true

	view := m.View()
	if strings.Contains(view.Content, "stub failure") {
		t.Errorf("config view does not replace the transcript:\n%s", view.Content)
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
	m := newModel(agent.New(failingProvider{err: errors.New("boom")}, nil), config.Config{})
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
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}), teatest.WithInitialTermSize(80, 20))

	tm.Type("/")
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("exit elencode"))
	})

	tm.Type("quit")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Fails the test by timing out if the program is still running
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestProgramQuitsOnSecondCtrlC drives the whole program: the first press must
// not end the event loop and the second must.
func TestProgramQuitsOnSecondCtrlC(t *testing.T) {
	a := agent.New(failingProvider{err: errors.New("never asked")}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}), teatest.WithInitialTermSize(80, 20))

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
