package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/instructions"
)

func TestInstructionNames(t *testing.T) {
	files := []instructions.File{
		{Path: "/home/me/.config/elencode/AGENTS.md"},
		{Path: "/repo/AGENTS.md", Truncated: true},
		{Path: "/repo/api/CLAUDE.md"},
	}

	got := instructionNames(files, "/repo/api", "/home/me/.config/elencode")

	// The user's file in full: relative to the project it would be a climb
	// through every directory between the two
	want := []string{"/home/me/.config/elencode/AGENTS.md", "../AGENTS.md (truncated)", "CLAUDE.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("instructionNames = %q, want %q", got, want)
	}
}

// writeFile creates the file at path, and every directory leading to it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fileTool stands in for read, write and edit: it answers with output, or
// fails with err, and takes the same path the real ones do.
func fileTool(output string, err error) agent.Tool {
	return agent.Tool{
		Name: "read",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			return output, err
		},
	}
}

// subdirectoryProject is a working directory whose subdirectory api has
// instructions of its own.
func subdirectoryProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "root rules, read at startup")
	writeFile(t, filepath.Join(dir, "api", "AGENTS.md"), "api rules")
	writeFile(t, filepath.Join(dir, "api", "handler.go"), "package api")
	return dir
}

// noConversation is a conversation that has not attached anything yet
func noConversation() []agent.Message { return nil }

func touch(t *testing.T, tool agent.Tool, path string) (string, error) {
	t.Helper()
	input, _ := json.Marshal(map[string]string{"path": path})
	return tool.Execute(context.Background(), input)
}

func TestTouchingASubdirectoryAttachesItsInstructions(t *testing.T) {
	dir := subdirectoryProject(t)
	session := subdirectoryInstructions{dir: dir, conversation: noConversation}

	output, err := touch(t, session.wrap(fileTool("package api", nil)), "api/handler.go")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(output, "package api") || !strings.Contains(output, "api rules") {
		t.Errorf("output = %q, want the tool's, then the subdirectory's instructions", output)
	}
	if got, want := instructions.Attached(output), []string{"api/AGENTS.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("attached %q, want %q", got, want)
	}
}

func TestInstructionsInTheConversationAreNotAttachedAgain(t *testing.T) {
	dir := subdirectoryProject(t)
	earlier := instructions.Attach(dir, []instructions.File{{Path: filepath.Join(dir, "api", "AGENTS.md"), Content: "api rules"}})
	conversation := func() []agent.Message {
		return []agent.Message{agent.NewUserMessage([]agent.Block{agent.NewToolResultBlock("toolu_1", "package api"+earlier, false)})}
	}
	session := subdirectoryInstructions{dir: dir, conversation: conversation}

	output, _ := touch(t, session.wrap(fileTool("package api", nil)), "api/handler.go")

	if output != "package api" {
		t.Errorf("output = %q, want only the tool's", output)
	}
}

// What the model was told is the conversation, so instructions that left it,
// with a model switch or a turn rolled back, are attached again.
func TestInstructionsOutOfTheConversationAreAttachedAgain(t *testing.T) {
	dir := subdirectoryProject(t)
	tool := subdirectoryInstructions{dir: dir, conversation: noConversation}.wrap(fileTool("package api", nil))

	first, _ := touch(t, tool, "api/handler.go")
	second, _ := touch(t, tool, "api/handler.go")

	if len(instructions.Attached(first)) != 1 || len(instructions.Attached(second)) != 1 {
		t.Errorf("attached %q, then %q; want the file both times", instructions.Attached(first), instructions.Attached(second))
	}
}

func TestTouchingAFileInTheWorkingDirectoryAttachesNothing(t *testing.T) {
	dir := subdirectoryProject(t)
	session := subdirectoryInstructions{dir: dir, conversation: noConversation}

	output, _ := touch(t, session.wrap(fileTool("# readme", nil)), "README.md")

	if output != "# readme" {
		t.Errorf("output = %q, want only the tool's", output)
	}
}

// A tool that failed touched nothing, so its directory's instructions wait
// for one that does.
func TestAFailedToolAttachesNothing(t *testing.T) {
	dir := subdirectoryProject(t)
	session := subdirectoryInstructions{dir: dir, conversation: noConversation}
	failure := errors.New("no such file")

	output, err := touch(t, session.wrap(fileTool("", failure)), "api/missing.go")

	if output != "" || !errors.Is(err, failure) {
		t.Errorf("output %q, err %v; want the failure alone", output, err)
	}
}

// The tool did its work, so it still succeeds: the model hears that the
// instructions could not be read rather than that the file was not.
func TestUnreadableInstructionsAreReportedNotFailed(t *testing.T) {
	dir := t.TempDir()
	// A directory where the file should be cannot be read as one
	if err := os.MkdirAll(filepath.Join(dir, "api", "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	session := subdirectoryInstructions{dir: dir, conversation: noConversation}

	output, err := touch(t, session.wrap(fileTool("package api", nil)), "api/handler.go")
	if err != nil {
		t.Fatalf("err = %v, want the tool's success kept", err)
	}
	if !strings.HasPrefix(output, "package api") || !strings.Contains(output, "could not read") {
		t.Errorf("output = %q, want the tool's, then the failure to read the instructions", output)
	}
}

func runBash(t *testing.T, tool agent.Tool, command string) string {
	t.Helper()
	input, _ := json.Marshal(map[string]string{"command": command})
	output, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func bashTool() agent.Tool {
	return agent.Tool{
		Name: "bash",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "ok", nil
		},
	}
}

// bash names no file in its input, so the files are looked for in its command
func TestBashReachesTheDirectoriesItsCommandNames(t *testing.T) {
	commands := []string{
		"cat api/handler.go",
		`grep -n "func" 'api/handler.go'`,
		"cat ./api/handler.go",
		"cd api && go test",
		"ls api/",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			session := subdirectoryInstructions{dir: subdirectoryProject(t), conversation: noConversation}

			output := runBash(t, session.wrap(bashTool()), command)

			if got, want := instructions.Attached(output), []string{"api/AGENTS.md"}; !reflect.DeepEqual(got, want) {
				t.Errorf("attached %q, want %q", got, want)
			}
		})
	}
}

// Two paths in one directory reach its instructions twice, which are
// attached once
func TestBashNamingADirectoryTwiceAttachesItOnce(t *testing.T) {
	session := subdirectoryInstructions{dir: subdirectoryProject(t), conversation: noConversation}

	output := runBash(t, session.wrap(bashTool()), "diff api/handler.go api/")

	if got, want := instructions.Attached(output), []string{"api/AGENTS.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("attached %q, want %q", got, want)
	}
}

func TestBashNamingNothingBelowAttachesNothing(t *testing.T) {
	for _, command := range []string{"ls -la", "go test ./...", "cat missing/file.go", "cat ../outside.go"} {
		t.Run(command, func(t *testing.T) {
			session := subdirectoryInstructions{dir: subdirectoryProject(t), conversation: noConversation}

			if output := runBash(t, session.wrap(bashTool()), command); output != "ok" {
				t.Errorf("output = %q, want only the tool's", output)
			}
		})
	}
}
