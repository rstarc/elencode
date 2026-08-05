package transcript

import (
	"strings"

	"github.com/rstarc/elencode/internal/agent"
)

// Stream is the block currently being generated, held until it is settled
// enough to print. Wrapping is greedy, so more text can only extend the last
// row and never reflow the ones above it: every row but the last is final the
// moment it exists, and the last one belongs in the frame until the block ends.
//
// Rendered row indexes are not stable across widths, so a width change has to
// End the stream rather than carry it over.
type Stream struct {
	partial  string // the block being streamed, as text
	thinking bool   // whether that block is reasoning rather than the answer
	printed  int    // rows already handed out; the rest is still in the frame
	width    int
}

// SetWidth fits the stream to the terminal. The caller is expected to End the
// stream first: rows rendered at the old width cannot be counted at the new one.
func (s *Stream) SetWidth(width int) { s.width = width }

// Delta adds a fragment and returns whatever that settles. Reasoning and the
// answer are separate blocks, so a fragment of one ends the other: the model
// reasons first and answers after.
func (s *Stream) Delta(text string, thinking bool) string {
	var finished string
	if s.partial != "" && s.thinking != thinking {
		finished = s.End()
	}

	s.thinking = thinking
	s.partial += text
	return joinRows(finished, s.flush())
}

// End returns whatever has not been handed out yet, including the trailing row,
// and forgets it. Called when the text can no longer grow: the message landed,
// or the turn ended without it.
func (s *Stream) End() string {
	rows := s.rows()
	var rest string
	if s.printed < len(rows) {
		rest = strings.Join(rows[s.printed:], "\n")
	}
	s.Reset()
	return rest
}

// Reset drops the stream unprinted, for a turn that starts fresh
func (s *Stream) Reset() {
	s.partial, s.printed, s.thinking = "", 0, false
}

// Tail is the row the text is still being written into, the one part of the
// transcript that lives in the frame rather than in the scrollback.
func (s Stream) Tail() string {
	rows := s.rows()
	if len(rows) == 0 {
		return ""
	}
	return rows[len(rows)-1]
}

// rows renders what has been streamed so far as transcript rows
func (s Stream) rows() []string {
	if s.partial == "" {
		return nil
	}

	block := s.Partial()
	return strings.Split(Block(block, agent.RoleAssistant, s.width), "\n")
}

// Thinking reports whether the block in flight is reasoning rather than the
// answer. A fragment of the other kind ends it, which is when it settles.
func (s Stream) Thinking() bool { return s.thinking }

// Partial is the block being streamed, or nil when nothing is in flight. A
// width change settles it into the document, which holds blocks rather than the
// rows they rendered to.
func (s Stream) Partial() agent.Block {
	if s.partial == "" {
		return nil
	}
	if s.thinking {
		return agent.ThinkingBlock{Thinking: s.partial}
	}
	return agent.TextBlock{Text: s.partial}
}

// flush returns the rows that have settled since the last one, and records them
// as printed. The trailing row is held back: it can still grow.
func (s *Stream) flush() string {
	rows := s.rows()
	if len(rows) <= s.printed+1 {
		return ""
	}

	settled := rows[s.printed : len(rows)-1]
	s.printed = len(rows) - 1
	return strings.Join(settled, "\n")
}

// joinRows joins rendered rows, dropping the empty ones so nothing contributes
// a blank line to the transcript.
func joinRows(rows ...string) string {
	var present []string
	for _, row := range rows {
		if row != "" {
			present = append(present, row)
		}
	}
	return strings.Join(present, "\n")
}

// LandedBlocks returns the entries a message adds when it arrives: the blocks
// that never stream — tool uses, and reasoning the API only ever returns
// encrypted. Text and reasoning reached the document as the stream settled
// them, so including them here would hold them twice.
func LandedBlocks(msg agent.Message) []Entry {
	var entries []Entry
	for _, block := range msg.Content {
		if msg.Role == agent.RoleAssistant && streams(block) {
			continue
		}
		entries = append(entries, BlockEntry{Block: block, Role: msg.Role})
	}
	return entries
}

// streams reports whether a block reaches the screen as it is generated
func streams(block agent.Block) bool {
	switch block.(type) {
	case agent.TextBlock, agent.ThinkingBlock:
		return true
	default:
		return false
	}
}
