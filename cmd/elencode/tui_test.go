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
	if m.menuIndex != 0 {
		t.Errorf("menuIndex = %d, want 0 with a single match", m.menuIndex)
	}
}

func TestTypingResetsTheHighlight(t *testing.T) {
	m := typeText(t, newSizedModel(t), commandPrefix)
	m.menuIndex = 2 // as if the user had arrowed down a longer match set

	m = typeText(t, m, "q")

	// The match set just changed, so the old index may point at another command
	if m.menuIndex != 0 {
		t.Errorf("menuIndex = %d, want it reset to 0 when the matches change", m.menuIndex)
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

	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := printed(t, cmd); !strings.Contains(got, "unknown command: /qut") {
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

	if m.input.Value() != "hello" {
		t.Errorf("input = %q, want it kept while the agent is busy", m.input.Value())
	}
}

func TestViewPutsCursorBelowTheMenu(t *testing.T) {
	m := typeText(t, newSizedModel(t), commandPrefix)

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("view has no cursor, want one on the input row")
	}
	wantY := lipgloss.Height(m.menuView())
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (below the menu)", view.Cursor.Y, wantY)
	}
}

// failingProvider fails every round of inference, so a turn driven through the
// real program reaches the error path.
type failingProvider struct{ err error }

func (p failingProvider) Models(ctx context.Context) ([]agent.Model, error) { return nil, p.err }

func (p failingProvider) SetModel(id string) {}

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

			// The error is printed into the scrollback, so the frame itself only
			// has to stay inside the terminal.
			assertFitsWidth(t, final.View().Content, width)
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
	m = update(t, m, streamEventMsg{agent.TextDeltaEvent{Text: "half a sentence"}})
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

// modelProvider serves a fixed model list and records the model it was switched
// to, so a test can assert the choice reached the provider.
type modelProvider struct {
	models []agent.Model
	set    string
}

func (p *modelProvider) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, 1)
	events <- agent.ErrorEvent{Err: errors.New("never asked")}
	close(events)
	return events
}

func (p *modelProvider) Models(ctx context.Context) ([]agent.Model, error) { return p.models, nil }

func (p *modelProvider) SetModel(id string) { p.set = id }

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
	m := newModel(agent.New(provider, nil), config.Config{Path: file, AnthropicAPIKey: "key"})
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

	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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

	if m.modelIndex != 1 {
		t.Errorf("modelIndex = %d, want 1 (the model in use)", m.modelIndex)
	}
}

func TestEnterSelectsTheHighlightedModel(t *testing.T) {
	provider := &modelProvider{models: testModels}
	m := update(t, newPickerModel(t, provider), modelsMsg{models: testModels})

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if provider.set != "model-two" {
		t.Errorf("provider was switched to %q, want %q", provider.set, "model-two")
	}
	if m.config.Model != "model-two" {
		t.Errorf("config model = %q, want %q", m.config.Model, "model-two")
	}
	if m.modelPickerVisible {
		t.Error("picker still open after a choice")
	}

}

// TestSelectingAModelPersistsIt covers the choice outliving the session: the
// config file is what the next start reads.
func TestSelectingAModelPersistsIt(t *testing.T) {
	m := update(t, newPickerModel(t, &modelProvider{models: testModels}), modelsMsg{models: testModels})

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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

	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = update(t, m, modelsFrom(t, cmd))

	if provider.set != "model-two" {
		t.Errorf("provider was switched to %q, want %q", provider.set, "model-two")
	}
	if m.modelPickerVisible {
		t.Error("picker opened for a model named on the command line")
	}
}

func TestUnknownModelArgumentIsReported(t *testing.T) {
	provider := &modelProvider{models: testModels}
	m := typeText(t, newPickerModel(t, provider), "/model model-nine")

	m, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	_, cmd = updateCmd(t, m, modelsFrom(t, cmd))

	if provider.set != "" {
		t.Errorf("provider was switched to %q, want no switch for a model that does not exist", provider.set)
	}
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
	if m.modelPickerVisible {
		t.Error("picker opened with no models to show")
	}
	if got := printed(t, cmd); !strings.Contains(got, "503 overloaded") {
		t.Errorf("printed %q, want it to say why the model list failed", got)
	}
}

func TestEscClosesTheModelPicker(t *testing.T) {
	provider := &modelProvider{models: testModels}
	m := update(t, newPickerModel(t, provider), modelsMsg{models: testModels})

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.modelPickerVisible {
		t.Error("picker still open after Esc")
	}
	if provider.set != "" {
		t.Errorf("Esc switched the model to %q, want it to change nothing", provider.set)
	}
}

// TestModelPickerSwallowsTypedKeys keeps the picker's keyboard to itself: a
// keystroke that reached the input would open the command menu underneath it.
func TestModelPickerSwallowsTypedKeys(t *testing.T) {
	m := update(t, newPickerModel(t, &modelProvider{models: testModels}), modelsMsg{models: testModels})

	m = typeText(t, m, "/")

	if m.input.Value() != "" {
		t.Errorf("input = %q, want keystrokes swallowed while the picker is open", m.input.Value())
	}
	if !m.modelPickerVisible {
		t.Error("typing closed the picker")
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
	body := fmt.Sprintf("%v", cmd())
	return strings.TrimSuffix(strings.TrimPrefix(body, "{"), "}")
}

// wrappingText is long enough to wrap into several transcript rows at the
// widths these tests use.
var wrappingText = strings.Repeat("the quick brown fox jumps over the lazy dog ", 5)

func TestFlushStreamPrintsRowsThatHaveSettled(t *testing.T) {
	m := newSizedModel(t)
	m.partial = wrappingText
	if len(m.streamRows()) < 3 {
		t.Fatalf("test text wraps to %d rows, want at least 3", len(m.streamRows()))
	}

	got := m.flushStream()

	rows := m.streamRows()
	// Every row but the last: the last one can still grow, so printing it would
	// put a half-written line in the scrollback.
	want := strings.Join(rows[:len(rows)-1], "\n")
	if got != want {
		t.Errorf("flushed\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, rows[len(rows)-1]) {
		t.Error("flushed the row that is still being written into")
	}
}

func TestFlushStreamPrintsEachRowOnce(t *testing.T) {
	m := newSizedModel(t)
	m.partial = wrappingText
	m.flushStream()

	// No new text, so nothing has settled since the last flush
	if got := m.flushStream(); got != "" {
		t.Errorf("flushed %q a second time, want nothing", got)
	}
}

func TestFlushStreamWaitsForARowToFill(t *testing.T) {
	m := newSizedModel(t)

	m.partial = "short"

	// One unfinished row is not something to print yet
	if got := m.flushStream(); got != "" {
		t.Errorf("flushed %q, want nothing until a row has settled", got)
	}
}

func TestEndStreamPrintsWhatIsLeftInTheFrame(t *testing.T) {
	m := newSizedModel(t)
	m.partial = wrappingText
	m.flushStream()
	tail := m.streamTail()

	got := m.endStream()

	if got != tail {
		t.Errorf("end of stream printed %q, want the trailing row %q", got, tail)
	}
	if m.partial != "" || m.streamed != 0 {
		t.Errorf("stream not forgotten: partial = %q, streamed = %d", m.partial, m.streamed)
	}
}

// TestPrintMessageLeavesOutTextAlreadyStreamed guards against the reply landing
// on screen twice: it was printed as it streamed, and the message that follows
// carries the same text.
func TestPrintMessageLeavesOutTextAlreadyStreamed(t *testing.T) {
	m := newSizedModel(t)
	m.partial = "Hello"
	m.flushStream()

	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "Hello"}}}
	got := m.printMessage(landed)

	if strings.Count(got, "Hello") > 1 {
		t.Errorf("message printed the streamed text again:\n%s", got)
	}
}

// TestPrintMessagePrintsBlocksThatNeverStream covers tool calls: no delta
// carries them, so the landed message is the only chance to show them.
func TestPrintMessagePrintsBlocksThatNeverStream(t *testing.T) {
	m := newSizedModel(t)

	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.ToolUseBlock{ID: "toolu_1", Name: "read", Input: []byte(`{"path":"a.txt"}`)},
	}}

	if got := m.printMessage(landed); !strings.Contains(got, "read") {
		t.Errorf("landed message did not print the tool use:\n%s", got)
	}
}

func TestViewShowsTheRowBeingWrittenInto(t *testing.T) {
	m := newSizedModel(t)

	m = update(t, m, streamEventMsg{agent.TextDeltaEvent{Text: "half a sen"}})

	if view := m.View().Content; !strings.Contains(view, "half a sen") {
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

func (p scriptedProvider) SetModel(id string) {}

// TestProgramPrintsThePromptAndTheReply drives the whole program, which is the
// only place the printing is real: Update hands back commands, and it is
// bubbletea that turns them into lines above the frame.
func TestProgramPrintsThePromptAndTheReply(t *testing.T) {
	a := agent.New(scriptedProvider{deltas: []string{"Hel", "lo there, ", "how can I help?"}}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}), teatest.WithInitialTermSize(80, 20))

	tm.Type("hi")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("how can I help?"))
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
