package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/rstarc/elencode/internal/instructions"
	"github.com/rstarc/elencode/internal/tui/menu"
	"github.com/rstarc/elencode/internal/tui/transcript"
)

// newAgent is an agent already pointed at provider, which is what these tests
// want: they care about the turn, not about which model started it.
func newAgent(provider agent.Provider, tools []agent.Tool) *agent.Agent {
	a := agent.New(tools)
	a.SetModel(agent.Model{Provider: agent.ProviderAnthropic, ID: "test-model"}, provider)
	return a
}

// newTestModel builds a model backed by an agent that is never Run, so it needs
// no provider: only the transcript is read during rendering.
func newTestModel() model {
	return newModel(newAgent(nil, nil), config.Config{}, defaultCommands(), nil, nil)
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
	// Only the line above the input is stacked over it while idle
	wantY := 1
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
	wantY := lipgloss.Height(m.spinnerLine()) + 1
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (below the spinner)", view.Cursor.Y, wantY)
	}
}

func TestViewFramesTheInputWithModelAndShellPrompt(t *testing.T) {
	m := newModel(newAgent(nil, nil), config.Config{Model: "anthropic/test-model"}, defaultCommands(), nil, nil)
	m = update(t, m, tea.WindowSizeMsg{Width: 40, Height: 20})
	m = update(t, m, shellPromptMsg("rstarc@host: ~/elencode (main)"))

	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	// The frame pads every row out to the widest
	for i := range rows {
		rows[i] = strings.TrimRight(rows[i], " ")
	}
	if len(rows) != 4 {
		t.Fatalf("frame has %d rows, want 4:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if want := strings.Repeat("─", 19) + " anthropic/test-model"; rows[0] != want {
		t.Errorf("row above the input = %q, want %q", rows[0], want)
	}
	if want := strings.Repeat("─", 40); rows[2] != want {
		t.Errorf("row below the input = %q, want %q", rows[2], want)
	}
	if want := "rstarc@host: ~/elencode (main)"; rows[3] != want {
		t.Errorf("last row = %q, want %q", rows[3], want)
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
	if got, want := len(m.menu.Matches()), len(defaultCommands().Commands()); got != want {
		t.Errorf("%d commands left after arrowing, want all %d", got, want)
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
			m := newModel(agent.New(nil), config.Config{}, registry, nil, nil)
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

	_, cmd := updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	// The line above the input sits between the menu and the input
	wantY := lipgloss.Height(m.menu.View()) + 1
	if view.Cursor.Y != wantY {
		t.Errorf("cursor row = %d, want %d (below the menu)", view.Cursor.Y, wantY)
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
			a := newAgent(failingProvider{err: errors.New("429 rate_limit_error: too many requests, slow down")}, nil)
			tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands(), nil, nil), teatest.WithInitialTermSize(width, 20))

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

// rateLimitedProvider is rate limited once and answers on the retry, which is
// the whole point of the retry: the user gets a notice and then their reply.
type rateLimitedProvider struct{ calls int }

func (p *rateLimitedProvider) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, 2)
	if p.calls == 0 {
		p.calls++
		// A provider can stream output before discovering that the response must
		// be retried. The failed attempt must not contaminate the next one.
		events <- agent.TextDeltaEvent{Text: "stale"}
		events <- agent.ErrorEvent{Err: &agent.RetryableError{
			Err:   errors.New("too many requests"),
			After: time.Millisecond,
		}}
	} else {
		// The delta as well as the response: assistant text reaches the screen as
		// it streams, and Landed skips it on the assumption that it already has.
		events <- agent.TextDeltaEvent{Text: "recovered"}
		events <- agent.ResponseEvent{Response: agent.Response{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "recovered"}}},
			StopReason: agent.StopReasonEndTurn,
		}}
	}
	close(events)
	return events
}

func TestProgramReportsARetryAndCarriesOn(t *testing.T) {
	a := newAgent(&rateLimitedProvider{}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands(), nil, nil), teatest.WithInitialTermSize(80, 20))

	tm.Type("hi")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Both in one wait: WaitFor consumes the output as it reads, so a second call
	// would start after the bytes the first one already took.
	//
	// A backoff with nothing on screen is indistinguishable from a hang, so the
	// notice matters as much as the recovery that follows it.
	//
	// Only what follows the notice must be free of the failed attempt: before
	// it, the stale text may have been drawn into the frame, depending on
	// whether a render fell between its delta and the failure.
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		_, afterNotice, retried := bytes.Cut(out, []byte("retrying"))
		return retried &&
			bytes.Contains(afterNotice, []byte("recovered")) &&
			!bytes.Contains(afterNotice, []byte("stale"))
	})

	if err := tm.Quit(); err != nil {
		t.Fatalf("quitting the program: %v", err)
	}
}

// noisyRateLimitedProvider streams enough text to settle rows into the
// scrollback before the failure, which the unflushed tail of the other retry
// test never does.
type noisyRateLimitedProvider struct{ calls int }

func (p *noisyRateLimitedProvider) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, 2)
	if p.calls == 0 {
		p.calls++
		events <- agent.TextDeltaEvent{Text: strings.Repeat("half an answer ", 20)}
		events <- agent.ErrorEvent{Err: &agent.RetryableError{
			Err:   errors.New("overloaded"),
			After: time.Millisecond,
		}}
	} else {
		events <- agent.ResponseEvent{Response: agent.Response{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "recovered"}}},
			StopReason: agent.StopReasonEndTurn,
		}}
	}
	close(events)
	return events
}

// TestRetryNoticeDisownsPrintedOutput: printed scrollback belongs to the
// terminal, so the failed attempt's output cannot be retracted — the notice has
// to say it is not part of the answer, or the reply reads as two answers.
func TestRetryNoticeDisownsPrintedOutput(t *testing.T) {
	a := newAgent(&noisyRateLimitedProvider{}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands(), nil, nil), teatest.WithInitialTermSize(80, 20))

	tm.Type("hi")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("failed attempt"))
	})

	if err := tm.Quit(); err != nil {
		t.Fatalf("quitting the program: %v", err)
	}
}

// A hint finer than a second is real — Retry-After-Ms asks for one — and
// rounding it to "0s" reads as a bug rather than a wait.
func TestRetryDelayNeverRendersAsZero(t *testing.T) {
	tests := map[time.Duration]string{
		300 * time.Millisecond:  "300ms",
		2 * time.Second:         "2s",
		2500 * time.Millisecond: "3s",
	}
	for in, want := range tests {
		if got := retryDelay(in); got != want {
			t.Errorf("retryDelay(%s) = %q, want %q", in, got, want)
		}
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

	m := newModel(agent.New(nil), config.Config{
		Credentials: config.Credentials{agent.ProviderAnthropic: {APIKey: key}},
		Path:        "/tmp/elencode/config.json",
	}, defaultCommands(), nil, nil)
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
	m := newModel(newAgent(failingProvider{err: errors.New("boom")}, nil), config.Config{}, defaultCommands(), nil, nil)
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
	a := newAgent(failingProvider{err: errors.New("never asked")}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands(), nil, nil), teatest.WithInitialTermSize(80, 20))

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
	a := newAgent(failingProvider{err: errors.New("never asked")}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands(), nil, nil), teatest.WithInitialTermSize(80, 20))

	tm.Type("/")
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("exit elencode"))
	})

	// Down the list to /quit, the last of them
	for range len(defaultCommands().Commands()) - 1 {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
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
	a := newAgent(failingProvider{err: errors.New("never asked")}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands(), nil, nil), teatest.WithInitialTermSize(80, 20))

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

// recordingProvider answers every turn with the same reply and keeps the
// requests it was given, so a test can tell which provider a turn went to.
type recordingProvider struct{ requests []agent.Request }

func (p *recordingProvider) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	p.requests = append(p.requests, req)

	events := make(chan agent.Event, 1)
	events <- agent.ResponseEvent{Response: agent.Response{
		Message:    agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "hi"}}},
		StopReason: agent.StopReasonEndTurn,
	}}
	close(events)
	return events
}

// keyed is the provider set a test session was started with: which providers
// have a client is the only thing that decides what the picker offers.
func keyed(names ...agent.ProviderName) providerSet {
	providers := providerSet{}
	for _, name := range names {
		providers[name] = &recordingProvider{}
	}
	return providers
}

var testModels = []agent.Model{
	{Provider: agent.ProviderAnthropic, ID: "model-one", DisplayName: "Model One"},
	{Provider: agent.ProviderOpenAI, ID: "model-two", DisplayName: "Model Two"},
}

// newPickerModel builds a sized model whose config points at a writable file,
// so selecting a model can save without failing on the path.
func newPickerModel(t *testing.T, providers providerSet, models []agent.Model) model {
	t.Helper()

	file := path.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(`{"thinking_enabled":true}`), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cfg := config.Config{Path: file}
	m := newModel(agent.New(nil), cfg, defaultCommands(), providers, models)
	return update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
}

// openPicker runs /model with no argument, which is how the picker is opened
func openPicker(t *testing.T, m model) model {
	t.Helper()

	m, _ = enter(t, typeText(t, m, "/model"))
	return m
}

// Which models exist ships with the binary, so there is nothing to wait for:
// no request, no spinner, no way for opening the picker to fail.
func TestModelCommandOpensThePickerWithoutAnAPICall(t *testing.T) {
	m := typeText(t, newPickerModel(t, keyed(agent.ProviderAnthropic), testModels), "/model")

	m, _ = enter(t, m)

	if !m.modelList.Open() {
		t.Error("/model did not open the picker")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared once the command ran", m.input.Value())
	}
}

func TestThePickerOffersEveryKeyedProvidersModels(t *testing.T) {
	m := newPickerModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), testModels)

	m = openPicker(t, m)

	view := m.View().Content
	for _, want := range testModels {
		if !strings.Contains(view, want.ID) {
			t.Errorf("picker does not offer %q:\n%s", want.ID, view)
		}
	}
}

// A model nobody has a key for cannot be talked to, so offering it would only
// produce a failed turn.
func TestThePickerOmitsModelsOfAProviderWithoutAKey(t *testing.T) {
	m := newPickerModel(t, keyed(agent.ProviderAnthropic), testModels)

	m = openPicker(t, m)

	view := m.View().Content
	if !strings.Contains(view, "model-one") {
		t.Errorf("picker does not offer the keyed provider's model:\n%s", view)
	}
	if strings.Contains(view, "model-two") {
		t.Errorf("picker offers a model whose provider has no key:\n%s", view)
	}
}

// Naming it outright deserves a better answer than "unknown model": the model
// exists, the key does not.
func TestChoosingAModelWhoseProviderHasNoKeyIsReported(t *testing.T) {
	m := typeText(t, newPickerModel(t, keyed(agent.ProviderAnthropic), testModels), "/model model-two")

	_, cmd := enter(t, m)

	got := printed(t, cmd)
	if !strings.Contains(got, "openai") || !strings.Contains(got, "key") {
		t.Errorf("printed %q, want it to say openai has no API key", got)
	}
}

// TestModelPickerStartsOnTheCurrentModel saves the user from hunting for where
// they already are in a list of twenty.
func TestModelPickerStartsOnTheCurrentModel(t *testing.T) {
	m := newPickerModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), testModels)
	m.config.Model = "openai/model-two"

	m = openPicker(t, m)

	// Where the highlight starts is the picker's own business; what it means
	// here is that Enter, pressed without arrowing, keeps the model in use.
	m, _ = enter(t, m)
	if m.config.Model != "openai/model-two" {
		t.Errorf("config model = %q, want the picker to open on the model in use", m.config.Model)
	}
}

// TestCursorKeysLeaveTheModelListHighlightAlone covers a key the input takes
// but does not type: it reaches the input because the list only reserves the
// keys it drives, and it must not move a highlight the user has not touched.
func TestCursorKeysLeaveTheModelListHighlightAlone(t *testing.T) {
	m := newPickerModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), testModels)
	m.config.Model = "openai/model-two"
	m = openPicker(t, m)

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})

	m, _ = enter(t, m)
	if m.config.Model != "openai/model-two" {
		t.Errorf("config model = %q, want a cursor key to leave the highlight on the model in use", m.config.Model)
	}
}

func TestEffectiveDefaultModelIsShownAndSelected(t *testing.T) {
	m := newPickerModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), testModels)
	m.config = configWithEffectiveModel(m.config, testModels[1])

	if view := renderConfig(m.config, 80); !strings.Contains(view, "model-two") {
		t.Errorf("config view does not show the effective model:\n%s", view)
	}

	m = openPicker(t, m)
	m, _ = enter(t, m)
	if m.config.Model != "openai/model-two" {
		t.Errorf("config model = %q, want the picker to open on the effective default", m.config.Model)
	}
}

func TestEnterSelectsTheHighlightedModel(t *testing.T) {
	m := openPicker(t, newPickerModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), testModels))

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = updateCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.config.Model != "openai/model-two" {
		t.Errorf("config model = %q, want %q", m.config.Model, "openai/model-two")
	}
	if m.modelList.Open() {
		t.Error("list still open after a choice")
	}
	// The arrow keys put the id there; the choice is made, so it goes
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared once the model was chosen", m.input.Value())
	}
}

// TestSelectingAModelSavesItsQualifiedName covers the choice outliving the
// session: the config file is what the next start reads, and a bare id would
// not say who to ask for it.
func TestSelectingAModelSavesItsQualifiedName(t *testing.T) {
	m := openPicker(t, newPickerModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), testModels))

	m, _ = enter(t, m)

	body, err := os.ReadFile(m.config.Path)
	if err != nil {
		t.Fatalf("reading the saved config: %v", err)
	}
	if !strings.Contains(string(body), "anthropic/model-one") {
		t.Errorf("config file does not name the chosen model:\n%s", body)
	}
}

// Switching models is what switches providers: the next turn has to go to
// whoever serves the model just chosen.
func TestSelectingAModelSwitchesTheProviderItStreamsTo(t *testing.T) {
	providers := keyed(agent.ProviderAnthropic, agent.ProviderOpenAI)
	m := openPicker(t, newPickerModel(t, providers, testModels))

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = enter(t, m)

	for range m.agent.Run(context.Background(), "hi") {
	}

	openaiClient := providers[agent.ProviderOpenAI].(*recordingProvider)
	anthropicClient := providers[agent.ProviderAnthropic].(*recordingProvider)
	if len(openaiClient.requests) != 1 {
		t.Errorf("the chosen model's provider served %d turns, want 1", len(openaiClient.requests))
	}
	if len(anthropicClient.requests) != 0 {
		t.Errorf("the previous provider served %d turns after the switch", len(anthropicClient.requests))
	}
}

func TestModelArgumentSelectsWithoutOpeningThePicker(t *testing.T) {
	m := typeText(t, newPickerModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), testModels), "/model model-two")

	m, _ = enter(t, m)

	if m.modelList.Open() {
		t.Error("list opened for a model named on the command line")
	}
	if m.config.Model != "openai/model-two" {
		t.Errorf("config model = %q, want the model named on the command line", m.config.Model)
	}
}

// The qualified form reaches a model released since this build: the catalog
// cannot know it, but its provider can still be named.
func TestAQualifiedModelOutsideTheCatalogIsSelected(t *testing.T) {
	m := typeText(t, newPickerModel(t, keyed(agent.ProviderOpenAI), testModels), "/model openai/gpt-brand-new")

	m, cmd := enter(t, m)

	if m.config.Model != "openai/gpt-brand-new" {
		t.Errorf("config model = %q, want the model named on the command line", m.config.Model)
	}
	// Nothing is known about it, so it is run without thinking — silently
	// enough to look like a bug unless the notice says so.
	if got := printed(t, cmd); !strings.Contains(got, "thinking") {
		t.Errorf("printed %q, want it to say the model runs without thinking", got)
	}
}

func TestUnknownModelArgumentIsReported(t *testing.T) {
	m := typeText(t, newPickerModel(t, keyed(agent.ProviderAnthropic), testModels), "/model model-nine")

	_, cmd := enter(t, m)

	if got := printed(t, cmd); !strings.Contains(got, "model-nine") {
		t.Errorf("printed %q, want it to name the model asked for", got)
	}
}

func TestEscClosesTheModelPicker(t *testing.T) {
	m := openPicker(t, newPickerModel(t, keyed(agent.ProviderAnthropic), testModels))
	// Arrowing first, so there is something left in the input to clear
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.modelList.Open() {
		t.Error("list still open after Esc")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared with the list", m.input.Value())
	}
}

// TestOpeningTheModelListTakesTheInputOver covers text left on the input when
// the list opens: it belongs to the command line the list replaces, so it must
// not linger behind the list as an open command menu.
func TestOpeningTheModelListTakesTheInputOver(t *testing.T) {
	m := typeText(t, newPickerModel(t, keyed(agent.ProviderAnthropic), testModels), commands.Prefix)

	m = update(t, m, commands.ChooseModelMsg{})

	if m.menu.Open() {
		t.Error("the command menu is still open behind the model list")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want the list to start on an empty one", m.input.Value())
	}
}

// TestTypingNarrowsTheModelList is what the input is for while the list is up:
// the catalog offers more models than the arrow keys are worth.
func TestTypingNarrowsTheModelList(t *testing.T) {
	m := openPicker(t, newPickerModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), testModels))

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
// double duty now that there is a filter: an empty list is the query's doing,
// not the catalog's.
func TestNarrowingTheModelListToNothingSaysSo(t *testing.T) {
	m := openPicker(t, newPickerModel(t, keyed(agent.ProviderAnthropic), testModels))

	m = typeText(t, m, "zzz")

	if view := m.View().Content; !strings.Contains(view, "no matching model") {
		t.Errorf("list does not say nothing matched:\n%s", view)
	}
}

// TestSlashDoesNotOpenTheMenuBehindTheModelList covers the cost of letting
// keystrokes reach the input: the input is also what opens the command menu.
func TestSlashDoesNotOpenTheMenuBehindTheModelList(t *testing.T) {
	m := openPicker(t, newPickerModel(t, keyed(agent.ProviderAnthropic), testModels))

	m = typeText(t, m, commands.Prefix)

	if m.menu.Open() {
		t.Error("the command menu opened behind the model list")
	}
	if !m.modelList.Open() {
		t.Error("typing closed the model list")
	}
}

func TestModelPickerFitsTheTerminal(t *testing.T) {
	const height = 20

	var many []agent.Model
	for i := range 40 {
		many = append(many, agent.Model{Provider: agent.ProviderAnthropic, ID: fmt.Sprintf("model-%d", i), DisplayName: "Model"})
	}
	m := openPicker(t, newPickerModel(t, keyed(agent.ProviderAnthropic), many))

	if got := lipgloss.Height(m.View().Content); got > height {
		t.Errorf("view is %d rows tall with the picker open, want <= %d", got, height)
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

// TestProgramPrintsThePromptAndTheReply drives the whole program, which is the
// only place the printing is real: Update hands back commands, and it is
// bubbletea that turns them into lines above the frame.
func TestProgramPrintsThePromptAndTheReply(t *testing.T) {
	a := newAgent(scriptedProvider{deltas: []string{"Hel", "lo there, ", "how can I help?"}}, nil)
	tm := teatest.NewTestModel(t, newModel(a, config.Config{}, defaultCommands(), nil, nil), teatest.WithInitialTermSize(80, 20))

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

// toolCallingProvider asks for one tool call, then answers.
type toolCallingProvider struct{ calls int }

func (p *toolCallingProvider) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, 2)
	response := agent.Response{
		Message:    agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "all done"}}},
		StopReason: agent.StopReasonEndTurn,
	}
	if p.calls == 0 {
		toolUse := agent.ToolUseBlock{ID: "toolu_1", Name: "read", Input: json.RawMessage(`{"path":"api/handler.go"}`)}
		response = agent.Response{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{toolUse}},
			StopReason: agent.StopReasonToolUse,
		}
	} else {
		// Assistant text reaches the screen as deltas: Landed skips it
		events <- agent.TextDeltaEvent{Text: "all done"}
	}
	p.calls++
	events <- agent.ResponseEvent{Response: response}
	close(events)
	return events
}

// A subdirectory's instructions reach the model inside a tool's output, which
// the transcript does not show, so the user is told separately. The notice
// comes after the tool that found them, before the reply they shaped.
func TestProgramSaysWhichInstructionsAToolAttached(t *testing.T) {
	attached := instructions.Attach("/repo", []instructions.File{{Path: "/repo/api/AGENTS.md", Content: "api rules"}})
	read := agent.Tool{
		Name:    "read",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) { return "package api" + attached, nil },
	}
	m := newModel(newAgent(&toolCallingProvider{}, []agent.Tool{read}), config.Config{}, defaultCommands(), nil, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 20))

	tm.Type("look at the handler")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		text := ansi.Strip(string(out))
		notice := strings.Index(text, "found api/AGENTS.md, added its instructions")
		reply := strings.Index(text, "all done")
		return notice >= 0 && reply > notice
	}, teatest.WithDuration(5*time.Second))

	if err := tm.Quit(); err != nil {
		t.Fatalf("quitting the program: %v", err)
	}
}

// TestSelectingAModelSaysSo covers the only feedback there is: the switch
// changes nothing on screen by itself, since what is already printed stays.
func TestSelectingAModelSaysSo(t *testing.T) {
	m := openPicker(t, newPickerModel(t, keyed(agent.ProviderAnthropic), testModels))

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

func TestHeaderSpansTheTerminal(t *testing.T) {
	const width = 72

	got := newTestModel().intro(width)

	if lipgloss.Width(got) != width {
		t.Errorf("header is %d columns wide, want the full %d:\n%s", lipgloss.Width(got), width, got)
	}
	if !strings.Contains(got, banner) {
		t.Errorf("header does not carry the title %q:\n%s", banner, got)
	}
}

// What the model was told before the conversation is invisible otherwise, so
// the intro names every file it came from.
func TestIntroNamesTheInstructionFiles(t *testing.T) {
	m := newTestModel()
	m.instructionFiles = []string{"../AGENTS.md", "CLAUDE.md"}

	got := m.intro(80)

	for _, file := range m.instructionFiles {
		if !strings.Contains(got, "instructions from "+file) {
			t.Errorf("intro does not name %s:\n%s", file, got)
		}
	}
}

func TestIntroWithoutInstructionsNamesNone(t *testing.T) {
	if got := newTestModel().intro(80); strings.Contains(got, "instructions") {
		t.Errorf("intro mentions instructions there are none of:\n%s", got)
	}
}

// TestTheFirstSizePrintsNothing covers why the intro is printed before the
// program starts: a print that arrives before Bubble Tea has drawn its first
// frame climbs the whole terminal height and overwrites the lines already on
// screen, which belong to the shell.
func TestTheFirstSizePrintsNothing(t *testing.T) {
	m := newTestModel()
	m.firstStart = true

	_, cmd := updateCmd(t, m.showProviders(), tea.WindowSizeMsg{Width: 80, Height: 20})

	if cmd != nil {
		t.Errorf("the first WindowSizeMsg printed %q, want nothing printed before the first frame", printed(t, cmd))
	}
}

func TestResizingPrintsNothing(t *testing.T) {
	m := newSizedModel(t)

	_, cmd := updateCmd(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	if cmd != nil {
		t.Errorf("resizing printed %q, want nothing", printed(t, cmd))
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

// A terminal delivers a paste as one message of its own rather than as key
// presses, and a prompt is as likely to be pasted as typed.
func TestAPasteLandsInTheInput(t *testing.T) {
	m := newSizedModel(t)

	m = update(t, m, tea.PasteMsg{Content: "explain this function"})

	if got := m.input.Value(); got != "explain this function" {
		t.Errorf("input = %q, want the pasted text", got)
	}
}

// Pasted into a list, the text filters it, as typing it would.
func TestAPasteFiltersTheOpenList(t *testing.T) {
	m := openPicker(t, newPickerModel(t, keyed(agent.ProviderAnthropic, agent.ProviderOpenAI), testModels))

	m = update(t, m, tea.PasteMsg{Content: "two"})

	if got := m.modelList.Matches(); len(got) != 1 || got[0].ID != "model-two" {
		t.Errorf("matches = %v, want only model-two", got)
	}
}

// The config view has the keyboard and no input on screen, so a paste there
// must not land in the input unseen.
func TestAPasteIsIgnoredWhileTheConfigViewIsOpen(t *testing.T) {
	m := newSizedModel(t)
	m.configVisible = true

	m = update(t, m, tea.PasteMsg{Content: "hidden"})

	if got := m.input.Value(); got != "" {
		t.Errorf("input = %q, want nothing", got)
	}
}
