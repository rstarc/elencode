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

// Printed reports whether any of this stream has already reached the
// scrollback, where the terminal owns it and it can no longer be taken back.
func (s Stream) Printed() bool { return s.printed > 0 }

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

	var block agent.Block = agent.TextBlock{Text: s.partial}
	if s.thinking {
		block = agent.ThinkingBlock{Thinking: s.partial}
	}
	return strings.Split(Block(block, agent.RoleAssistant, s.width), "\n")
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

// Landed returns what a message adds to the transcript once it arrives: what is
// left of the stream, and the blocks that never stream — tool uses, and
// reasoning the API only ever returns encrypted. Text and reasoning are already
// on screen by now, so repeating them would show them twice.
func (s *Stream) Landed(msg agent.Message) string {
	rendered := []string{}
	if rest := s.End(); rest != "" {
		rendered = append(rendered, rest)
	}

	for _, block := range msg.Content {
		if msg.Role == agent.RoleAssistant && streams(block) {
			continue
		}
		if row := Block(block, msg.Role, s.width); row != "" {
			rendered = append(rendered, row)
		}
	}
	return joinRows(rendered...)
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
