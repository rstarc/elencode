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

// TestLandedBlocksLeavesOutTextAlreadyStreamed guards against the reply landing
// in the document twice: the stream settled it as its own entry, and the
// message that follows carries the same text.
func TestLandedBlocksLeavesOutTextAlreadyStreamed(t *testing.T) {
	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "Hello"}}}

	if entries := LandedBlocks(landed); len(entries) != 0 {
		t.Errorf("LandedBlocks kept %d streamed block(s), want none", len(entries))
	}
}

// TestLandedBlocksLeavesOutThinkingAlreadyStreamed is the thinking twin of the
// text case: reasoning settles as it streams, so the landed message must not
// carry it again.
func TestLandedBlocksLeavesOutThinkingAlreadyStreamed(t *testing.T) {
	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.ThinkingBlock{Thinking: "let me check", Signature: "sig"},
	}}

	if entries := LandedBlocks(landed); len(entries) != 0 {
		t.Errorf("LandedBlocks kept %d streamed block(s), want none", len(entries))
	}
}

// TestLandedBlocksKeepsBlocksThatNeverStream covers tool calls: no delta carries
// them, so the landed message is the only chance to record them.
func TestLandedBlocksKeepsBlocksThatNeverStream(t *testing.T) {
	landed := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.ToolUseBlock{ID: "toolu_1", Name: "read", Input: []byte(`{"path":"a.txt"}`)},
	}}

	entries := LandedBlocks(landed)
	if len(entries) != 1 {
		t.Fatalf("LandedBlocks returned %d entries, want the tool use", len(entries))
	}
	if got := stripANSI(entries[0].Render(80)); !strings.Contains(got, "read") {
		t.Errorf("landed tool use rendered as %q, want it to name the tool", got)
	}
}

// TestLandedBlocksKeepsEverythingAUserSaid covers the other role: nothing a user
// message carries ever streams, so all of it lands.
func TestLandedBlocksKeepsEverythingAUserSaid(t *testing.T) {
	landed := agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hello"}})

	if entries := LandedBlocks(landed); len(entries) != 1 {
		t.Errorf("LandedBlocks returned %d entries for a user message, want 1", len(entries))
	}
}

// TestPartialIsTheBlockInFlight covers what a width change needs: the block
// being streamed has to become a transcript entry before the document is laid
// out again, and only the stream knows what it is.
func TestPartialIsTheBlockInFlight(t *testing.T) {
	s := newStream()

	if block := s.Partial(); block != nil {
		t.Errorf("an idle stream has a partial block %#v, want none", block)
	}

	s.Delta("the answer", false)
	if block, ok := s.Partial().(agent.TextBlock); !ok || block.Text != "the answer" {
		t.Errorf("Partial() = %#v, want the text streamed so far", s.Partial())
	}

	s.Reset()
	s.Delta("let me check", true)
	if block, ok := s.Partial().(agent.ThinkingBlock); !ok || block.Thinking != "let me check" {
		t.Errorf("Partial() = %#v, want the reasoning streamed so far", s.Partial())
	}
}
