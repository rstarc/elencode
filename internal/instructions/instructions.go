// Package instructions finds the files a project keeps for coding agents,
// AGENTS.md (https://agents.md), and renders them for the model.
package instructions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// names are the files read in a directory, most preferred first. Only the
// first one there is read: a project keeping both usually keeps the same text
// in each, often one linking to the other.
var names = []string{"AGENTS.md", "CLAUDE.md"}

// MaxSize is how much of a file is read, in bytes: Codex's limit. Instructions
// are sent with every request, so a file that has grown into documentation
// would otherwise crowd out the conversation.
const MaxSize = 32 * 1024

// File is one directory's instructions.
type File struct {
	Path    string
	Content string
	// Truncated says Content stops at MaxSize, short of the file's end
	Truncated bool
}

// Find returns the instructions that apply in dir: the user's own, from
// userDir, then one file from each directory between the project root and dir,
// root first. The project root is the nearest directory holding .git; outside
// a checkout only dir itself is read, since nothing says where the project
// starts. An empty file counts as none.
func Find(userDir, dir string) ([]File, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	// Collected from dir upwards, then turned around: the nearest file comes
	// last so that it is read last, which is how it takes precedence.
	var directories []string
	for current := dir; ; current = filepath.Dir(current) {
		directories = append(directories, current)
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			break
		}
		if filepath.Dir(current) == current {
			directories = []string{dir}
			break
		}
	}
	slices.Reverse(directories)
	// First, so that every project file comes later and wins
	directories = append([]string{userDir}, directories...)

	return read(directories)
}

// SystemPrompt renders files as the model's system prompt, or nothing when
// there are none.
func SystemPrompt(files []File) string {
	if len(files) == 0 {
		return ""
	}

	var prompt strings.Builder
	// The precedence is the one AGENTS.md itself sets out
	prompt.WriteString("Instructions for coding agents follow, each from the file at its path: " +
		"the user's own first, if they keep one, then the project's, from the outermost directory in. " +
		"A project's file applies to its own directory and everything below it. Follow them. " +
		"Where two disagree, the one given later wins; the user's requests in the conversation win over all of them.")
	for _, file := range files {
		prompt.WriteString("\n\n" + block(file.Path, file))
	}
	return prompt.String()
}

// block renders one file's instructions, fenced off with the path it is named
// by rather than set under a heading: the files are Markdown, and a heading of
// ours would read as one of theirs.
func block(path string, file File) string {
	content := strings.TrimSpace(file.Content)
	if file.Truncated {
		content += fmt.Sprintf("\n[truncated: only the first %d bytes of the file were read]", MaxSize)
	}
	return fmt.Sprintf("<instructions path=%q>\n%s\n</instructions>", path, content)
}

// Below returns the instructions of the directories between dir and file,
// which is relative to dir and slash-separated, as the tools take it:
// the files Find did not read because they apply only below dir. Outermost
// first, as Find returns them.
func Below(dir, file string) ([]File, error) {
	var directories []string
	current := dir
	for _, name := range strings.Split(path.Dir(file), "/") {
		if name == "." {
			continue
		}
		current = filepath.Join(current, name)
		directories = append(directories, current)
	}
	return read(directories)
}

// read reads one file from each of directories, in order: the first of names
// that it holds, unless that is empty.
func read(directories []string) ([]File, error) {
	var files []File
	for _, directory := range directories {
		for _, name := range names {
			candidate := filepath.Join(directory, name)
			content, err := os.ReadFile(candidate)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(string(content)) == "" {
				continue
			}
			file := File{Path: candidate, Content: string(content)}
			if len(content) > MaxSize {
				// Backed off to the start of a character, so the cut cannot leave
				// half of one behind
				end := MaxSize
				for !utf8.RuneStart(content[end]) {
					end--
				}
				file.Content = string(content[:end])
				file.Truncated = true
			}
			files = append(files, file)
			break
		}
	}
	return files, nil
}

// attachedHeader opens what Attach adds to a tool's output. It is also how
// Attached tells that apart from the output itself, which may be a file that
// happens to look like instructions.
const attachedHeader = "[elencode: the instructions for coding agents below apply to this directory " +
	"and everything in it, and win over those given before]"

// Attach renders files, the instructions of a directory a tool has just
// touched, for adding to the end of the tool's output. Each is named relative
// to dir, as the tools name files. Nothing when there are none.
func Attach(dir string, files []File) string {
	if len(files) == 0 {
		return ""
	}

	var attached strings.Builder
	attached.WriteString("\n\n" + attachedHeader)
	for _, file := range files {
		name, err := filepath.Rel(dir, file.Path)
		if err != nil {
			name = file.Path
		}
		attached.WriteString("\n\n" + block(filepath.ToSlash(name), file))
	}
	return attached.String()
}

// attachedPath matches the opening line of a block, capturing the quoted path
var attachedPath = regexp.MustCompile(`(?m)^<instructions path=(".*")>$`)

// Attached returns the names of the files Attach added to output, a tool's
// output, in order.
func Attached(output string) []string {
	start := strings.LastIndex(output, attachedHeader)
	if start < 0 {
		return nil
	}
	var names []string
	for _, match := range attachedPath.FindAllStringSubmatch(output[start:], -1) {
		if name, err := strconv.Unquote(match[1]); err == nil {
			names = append(names, name)
		}
	}
	return names
}
