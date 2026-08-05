package transcript

import (
	"strings"

	"github.com/rstarc/elencode/internal/agent"
)

// Entry is one settled item in the transcript. It is a value rather than the
// string it rendered to, so a session can be laid out again at a new width: the
// program owns the transcript, the terminal is only where it currently happens
// to be drawn.
type Entry interface {
	Render(width int) string
}

// HeaderEntry titles the session
type HeaderEntry struct{ Title string }

func (e HeaderEntry) Render(width int) string { return Header(e.Title, width) }

// MessageEntry is one turn, from either side
type MessageEntry struct{ Message agent.Message }

func (e MessageEntry) Render(width int) string { return Message(e.Message, width) }

// NoticeEntry is something the program did rather than something either side said
type NoticeEntry struct{ Text string }

func (e NoticeEntry) Render(width int) string { return Notice(e.Text, width) }

// ErrorEntry is a failed turn
type ErrorEntry struct{ Err error }

func (e ErrorEntry) Render(width int) string { return Error(e.Err, width) }

// BlockEntry is a single block settled on its own, which is how a streamed
// answer enters the transcript: the message it belongs to has not arrived yet.
type BlockEntry struct {
	Block agent.Block
	Role  agent.Role
}

func (e BlockEntry) Render(width int) string { return Block(e.Block, e.Role, width) }

// Document is everything the session has said, in order.
type Document []Entry

// Append adds an entry to the end of the document
func (d *Document) Append(entry Entry) { *d = append(*d, entry) }

// Render lays the whole document out at width. Entries that render empty are
// dropped rather than contributing a blank line, as Message does with its blocks.
func (d Document) Render(width int) string {
	var rendered []string
	for _, entry := range d {
		if row := entry.Render(width); row != "" {
			rendered = append(rendered, row)
		}
	}
	return strings.Join(rendered, "\n")
}
