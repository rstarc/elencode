package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rstarc/elencode/internal/agent"

	"github.com/rstarc/elencode/internal/instructions"
)

// instructionNames is how the intro names the files the instructions came
// from: the project's relative to the working directory, and the user's own in
// full, since it lives nowhere near the project.
func instructionNames(files []instructions.File, workingDir, userDir string) []string {
	var names []string
	for _, file := range files {
		name := file.Path
		if filepath.Dir(file.Path) != userDir {
			if relative, err := filepath.Rel(workingDir, file.Path); err == nil {
				name = relative
			}
		}
		if file.Truncated {
			name += " (truncated)"
		}
		names = append(names, name)
	}
	return names
}

// unreadableNote opens the line the wrapper adds when a directory's
// instructions cannot be read. The TUI finds the line by it to tell the user.
const unreadableNote = "[elencode: could not read the instructions for "

// subdirectoryInstructions attach the instructions of a directory below the
// working directory to the output of a tool that touches a file there, the
// first time one does. The working directory's own were read at startup, into
// the system prompt.
type subdirectoryInstructions struct {
	dir string
	// conversation is what the model has been told so far. It is the record
	// of which files were attached: one that left it, with a model switch or a
	// turn rolled back, is attached again.
	conversation func() []agent.Message

	// The results of a response join the conversation only once all of its
	// calls have run, so until then attaching records what they attached.
	// response names the response by the ID of its first call.
	mu        sync.Mutex
	response  string
	attaching map[string]bool
}

// wrap returns tool attaching the instructions of the directories below the
// working directory that it touches. read, write and edit name their file as
// "path". bash names none as such, so its "command" is guessed at: each word,
// unquoted, that is the path of a file or directory below the working
// directory counts. Globs and paths inside flags are missed.
func (s *subdirectoryInstructions) wrap(tool agent.Tool) agent.Tool {
	execute := tool.Execute
	tool.Execute = func(ctx context.Context, input json.RawMessage) (string, error) {
		output, err := execute(ctx, input)
		if err != nil {
			return output, err
		}
		var named struct {
			Path    string `json:"path"`
			Command string `json:"command"`
		}
		// The tool accepted the input, so this cannot fail where it matters
		if err := json.Unmarshal(input, &named); err != nil {
			return output, nil
		}

		var touched []string
		if named.Path != "" {
			touched = append(touched, named.Path)
		}
		for _, word := range strings.Fields(named.Command) {
			word = strings.Trim(word, `"'`)
			word = strings.TrimPrefix(word, "./")
			word = strings.TrimSuffix(word, "/")
			// Also what keeps the guess inside the working directory
			if !fs.ValidPath(word) || word == "." {
				continue
			}
			info, err := os.Stat(filepath.Join(s.dir, word))
			if err != nil {
				continue
			}
			// Below reads down to the directory holding the file it is given,
			// so a directory is given as a path inside it
			if info.IsDir() {
				word += "/"
			}
			touched = append(touched, word)
		}

		conversation := s.conversation()
		var response string
		if len(conversation) > 0 {
			for _, block := range conversation[len(conversation)-1].Content {
				if call, ok := block.(agent.ToolUseBlock); ok {
					response = call.ID
					break
				}
			}
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.attaching == nil || response != s.response {
			s.response = response
			s.attaching = map[string]bool{}
		}
		attached := map[string]bool{}
		for name := range s.attaching {
			attached[name] = true
		}
		for _, message := range conversation {
			for _, block := range message.Content {
				if result, ok := block.(agent.ToolResultBlock); ok {
					for _, name := range instructions.Attached(result.Content) {
						attached[name] = true
					}
				}
			}
		}
		var fresh []instructions.File
		var failures string
		for _, path := range touched {
			found, err := instructions.Below(s.dir, path, attached)
			// What could be read is still attached
			if err != nil {
				// Kept to one line, which is how the TUI finds it
				failures += "\n\n" + unreadableNote + path + ": " + strings.ReplaceAll(err.Error(), "\n", "; ") + "]"
			}
			for _, file := range found {
				// Named as Attach names it, which is what Attached returns
				name, err := filepath.Rel(s.dir, file.Path)
				if err != nil {
					continue
				}
				// Also keeps two paths in one directory from attaching it twice
				attached[filepath.ToSlash(name)] = true
				s.attaching[filepath.ToSlash(name)] = true
				// Reading the file itself returned its text already
				if tool.Name == "read" && filepath.ToSlash(name) == named.Path {
					file.Content = "[the read tool's output above is this file, in full]"
					file.Truncated = false
				}
				fresh = append(fresh, file)
			}
		}
		return output + instructions.Attach(s.dir, fresh) + failures, nil
	}
	return tool
}
