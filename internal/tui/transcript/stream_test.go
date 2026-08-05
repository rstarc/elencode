package transcript

import (
	"strings"
	"testing"

	"github.com/rstarc/elencode/internal/agent"
)

// wrappingText is long enough to wrap into several transcript rows at the
// widths these tests use.
var wrappingText = strings.Repeat("the quick brown fox jumps over the lazy dog ", 5)

func newStream() *Stream {
	s := &Stream{}
	s.SetWidth(80)
	return s
}

func TestDeltaPrintsRowsThatHaveSettled(t *testing.T) {
	s := newStream()

	got := s.Delta(wrappingText, false)

	rows := s.rows()
	if len(rows) < 3 {
		t.Fatalf("test text wraps to %d rows, want at least 3", len(rows))
	}
	// Every row but the last: the last one can still grow, so printing it would
	// put a half-written line in the scrollback.
	want := strings.Join(rows[:len(rows)-1], "\n")
	if got != want {
		t.Errorf("settled\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, rows[len(rows)-1]) {
		t.Error("settled the row that is still being written into")
	}
}

// TestPrintedReportsWhatReachedTheScrollback: a retry discards the stream, but
// rows already handed out belong to the terminal and cannot be taken back — the
// caller can only say so, and needs to know when there is something to say.
func TestPrintedReportsWhatReachedTheScrollback(t *testing.T) {
	s := newStream()

	if s.Printed() {
		t.Error("Printed() = true before anything streamed")
	}
	s.Delta("short", false)
	if s.Printed() {
		t.Error("Printed() = true for a row still being written into")
	}
	s.Delta(wrappingText, false)
	if !s.Printed() {
		t.Error("Printed() = false after rows settled into the scrollback")
	}
}

func TestDeltaPrintsEachRowOnce(t *testing.T) {
	s := newStream()
	s.Delta(wrappingText, false)

	// No new text, so nothing has settled since the last delta
	if got := s.Delta("", false); got != "" {
		t.Errorf("settled %q a second time, want nothing", got)
	}
}

func TestDeltaWaitsForARowToFill(t *testing.T) {
	s := newStream()

	// One unfinished row is not something to print yet
	if got := s.Delta("short", false); got != "" {
		t.Errorf("settled %q, want nothing until a row has settled", got)
	}
}

func TestEndPrintsWhatIsLeftInTheFrame(t *testing.T) {
	s := newStream()
	s.Delta(wrappingText, false)
	tail := s.Tail()

	got := s.End()

	if got != tail {
		t.Errorf("end of stream printed %q, want the trailing row %q", got, tail)
	}
	if s.Tail() != "" {
		t.Errorf("stream not forgotten: tail = %q", s.Tail())
	}
}

func TestEndOfAnEmptyStreamPrintsNothing(t *testing.T) {
	s := newStream()

	if got := s.End(); got != "" {
		t.Errorf("end of an empty stream printed %q, want nothing", got)
	}
}

func TestTailIsTheRowStillBeingWrittenInto(t *testing.T) {
	s := newStream()
	s.Delta(wrappingText, false)

	rows := s.rows()
	if got := s.Tail(); got != rows[len(rows)-1] {
		t.Errorf("tail = %q, want the last row %q", got, rows[len(rows)-1])
	}
}

// TestSwitchingKindFinishesTheBlock covers the model reasoning first and
// answering after: reasoning and the answer are separate blocks, so a fragment
// of one ends the other.
func TestSwitchingKindFinishesTheBlock(t *testing.T) {
	s := newStream()
	s.Delta("let me think", true)
	thinkingTail := s.Tail()

	got := s.Delta("the answer", false)

	if !strings.Contains(got, stripANSI(thinkingTail)) && !strings.Contains(stripANSI(got), stripANSI(thinkingTail)) {
		t.Errorf("switching to the answer printed %q, want the finished reasoning %q", got, thinkingTail)
	}
	if !strings.Contains(stripANSI(s.Tail()), "the answer") {
		t.Errorf("tail = %q, want it to hold the answer alone", s.Tail())
	}
	if strings.Contains(stripANSI(s.Tail()), "let me think") {
		t.Errorf("tail = %q, want the reasoning left behind in the scrollback", s.Tail())
	}
}

func TestReasoningAndAnswerAreStyledDifferently(t *testing.T) {
	thinking := newStream()
	thinking.Delta("same words", true)
	answer := newStream()
	answer.Delta("same words", false)

	if thinking.Tail() == answer.Tail() {
		t.Error("reasoning renders the same as the answer, want it set apart")
	}
}

func TestResetForgetsTheStream(t *testing.T) {
	s := newStream()
	s.Delta(wrappingText, false)

	s.Reset()

	if got := s.Tail(); got != "" {
		t.Errorf("tail = %q after Reset, want nothing", got)
	}
	// Nothing was carried over, so the next turn settles its own rows again
	if got := s.Delta(wrappingText, false); got == "" {
		t.Error("a reset stream settled nothing, want it to start over")
	}
}

// TestLandedLeavesOutTextAlreadyStreamed guards against the reply landing on
// screen twice: it was printed as it streamed, and the message that follows
// carries the same text.
func TestLandedLeavesOutTextAlreadyStreamed(t *testing.T) {
	s := newStream()
	s.Delta("Hello", false)

	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "Hello"}}}
	got := s.Landed(landed)

	if strings.Count(stripANSI(got), "Hello") > 1 {
		t.Errorf("message printed the streamed text again:\n%s", got)
	}
}

// TestLandedLeavesOutThinkingAlreadyStreamed is the thinking twin of the text
// case: reasoning reaches the scrollback as it streams, so the landed message
// must not repeat it.
func TestLandedLeavesOutThinkingAlreadyStreamed(t *testing.T) {
	s := newStream()
	s.Delta("let me check", true)

	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.ThinkingBlock{Thinking: "let me check", Signature: "sig"},
	}}
	got := s.Landed(landed)

	if strings.Count(stripANSI(got), "let me check") > 1 {
		t.Errorf("the landed message printed the reasoning again:\n%s", got)
	}
}

// TestLandedPrintsBlocksThatNeverStream covers tool calls: no delta carries
// them, so the landed message is the only chance to show them.
func TestLandedPrintsBlocksThatNeverStream(t *testing.T) {
	s := newStream()

	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.ToolUseBlock{ID: "toolu_1", Name: "read", Input: []byte(`{"path":"a.txt"}`)},
	}}

	if got := s.Landed(landed); !strings.Contains(stripANSI(got), "read") {
		t.Errorf("landed message did not print the tool use:\n%s", got)
	}
}
