package instructions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// write creates the file at path, and every directory leading to it.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// project is a temporary directory marked as the root of a git checkout.
func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFindReadsAgentsMD(t *testing.T) {
	root := project(t)
	write(t, filepath.Join(root, "AGENTS.md"), "Run make test.")

	files, err := Find(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}

	want := []File{{Path: filepath.Join(root, "AGENTS.md"), Content: "Run make test."}}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Find = %+v, want %+v", files, want)
	}
}

func TestFindFallsBackToClaudeMD(t *testing.T) {
	root := project(t)
	write(t, filepath.Join(root, "CLAUDE.md"), "Use tabs.")

	files, err := Find(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}

	want := []File{{Path: filepath.Join(root, "CLAUDE.md"), Content: "Use tabs."}}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Find = %+v, want %+v", files, want)
	}
}

// Many projects keep CLAUDE.md as a copy of, or a link to, AGENTS.md: reading
// both would say everything twice.
func TestFindPrefersAgentsMDOverClaudeMD(t *testing.T) {
	root := project(t)
	write(t, filepath.Join(root, "AGENTS.md"), "from agents")
	write(t, filepath.Join(root, "CLAUDE.md"), "from claude")

	files, err := Find(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}

	want := []File{{Path: filepath.Join(root, "AGENTS.md"), Content: "from agents"}}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Find = %+v, want %+v", files, want)
	}
}

func TestFindWalksFromTheProjectRootDownToDir(t *testing.T) {
	root := project(t)
	dir := filepath.Join(root, "services", "api")
	write(t, filepath.Join(root, "AGENTS.md"), "root")
	write(t, filepath.Join(root, "services", "CLAUDE.md"), "services")
	write(t, filepath.Join(dir, "AGENTS.md"), "api")

	files, err := Find(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}

	want := []File{
		{Path: filepath.Join(root, "AGENTS.md"), Content: "root"},
		{Path: filepath.Join(root, "services", "CLAUDE.md"), Content: "services"},
		{Path: filepath.Join(dir, "AGENTS.md"), Content: "api"},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Find = %+v, want %+v", files, want)
	}
}

func TestFindStopsAtTheProjectRoot(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "AGENTS.md"), "someone else's project")
	root := filepath.Join(outside, "project")
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main")

	files, err := Find(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 0 {
		t.Errorf("Find = %+v, want nothing from above the project root", files)
	}
}

// A worktree or a submodule has a .git file pointing at the repository, rather
// than a directory: it is a project root all the same.
func TestFindTreatsAGitFileAsTheProjectRoot(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "AGENTS.md"), "the main checkout")
	root := filepath.Join(outside, "worktree")
	write(t, filepath.Join(root, ".git"), "gitdir: ../.git/worktrees/worktree")
	write(t, filepath.Join(root, "AGENTS.md"), "the worktree")

	files, err := Find(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}

	want := []File{{Path: filepath.Join(root, "AGENTS.md"), Content: "the worktree"}}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Find = %+v, want %+v", files, want)
	}
}

// Outside a git checkout there is no telling where the project starts, so only
// the directory itself is read.
func TestFindOutsideAProjectReadsOnlyDir(t *testing.T) {
	parent := t.TempDir()
	write(t, filepath.Join(parent, "AGENTS.md"), "parent")
	dir := filepath.Join(parent, "child")
	write(t, filepath.Join(dir, "AGENTS.md"), "child")

	files, err := Find(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}

	want := []File{{Path: filepath.Join(dir, "AGENTS.md"), Content: "child"}}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Find = %+v, want %+v", files, want)
	}
}

// The user's own file says how they like to work anywhere; the project's say
// how this project works, so they come later and win.
func TestFindReadsTheUsersFileFirst(t *testing.T) {
	user := t.TempDir()
	write(t, filepath.Join(user, "AGENTS.md"), "user")
	root := project(t)
	write(t, filepath.Join(root, "AGENTS.md"), "project")

	files, err := Find(user, root)
	if err != nil {
		t.Fatal(err)
	}

	want := []File{
		{Path: filepath.Join(user, "AGENTS.md"), Content: "user"},
		{Path: filepath.Join(root, "AGENTS.md"), Content: "project"},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Find = %+v, want %+v", files, want)
	}
}

func TestFindFallsBackToTheUsersClaudeMD(t *testing.T) {
	user := t.TempDir()
	write(t, filepath.Join(user, "CLAUDE.md"), "user")

	files, err := Find(user, project(t))
	if err != nil {
		t.Fatal(err)
	}

	want := []File{{Path: filepath.Join(user, "CLAUDE.md"), Content: "user"}}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Find = %+v, want %+v", files, want)
	}
}

func TestFindSkipsEmptyFiles(t *testing.T) {
	root := project(t)
	write(t, filepath.Join(root, "AGENTS.md"), " \n\n")

	files, err := Find(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 0 {
		t.Errorf("Find = %+v, want an empty file skipped", files)
	}
}

// A file past the limit is cut short rather than left out: its opening is
// usually what matters most, and leaving it out would hide that it exists.
func TestFindCutsAFileShortAtTheLimit(t *testing.T) {
	root := project(t)
	write(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("a", MaxSize+100))

	files, err := Find(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 1 {
		t.Fatalf("Find = %d files, want 1", len(files))
	}
	if len(files[0].Content) != MaxSize || !files[0].Truncated {
		t.Errorf("read %d bytes, truncated %v; want %d and marked truncated", len(files[0].Content), files[0].Truncated, MaxSize)
	}
}

func TestFindCutsBetweenCharacters(t *testing.T) {
	root := project(t)
	// Three bytes each, and the limit is not a multiple of three
	write(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("€", MaxSize))

	files, err := Find(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}

	if !utf8.ValidString(files[0].Content) {
		t.Error("the cut split a character")
	}
	if len(files[0].Content) > MaxSize {
		t.Errorf("read %d bytes, want at most %d", len(files[0].Content), MaxSize)
	}
}

func TestFindLeavesAFileAtTheLimitWhole(t *testing.T) {
	root := project(t)
	write(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("a", MaxSize))

	files, err := Find(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}

	if files[0].Truncated {
		t.Error("a file of exactly the limit was marked truncated")
	}
}

func TestFindWithoutInstructionsFindsNothing(t *testing.T) {
	files, err := Find(t.TempDir(), project(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("Find = %+v, want nothing", files)
	}
}

func TestSystemPromptNamesEveryFileInOrder(t *testing.T) {
	prompt := SystemPrompt([]File{
		{Path: "/repo/AGENTS.md", Content: "root rules"},
		{Path: "/repo/api/AGENTS.md", Content: "api rules"},
	})

	rootAt := strings.Index(prompt, "/repo/AGENTS.md")
	rootRules := strings.Index(prompt, "root rules")
	apiAt := strings.Index(prompt, "/repo/api/AGENTS.md")
	apiRules := strings.Index(prompt, "api rules")
	if rootAt < 0 || rootRules < 0 || apiAt < 0 || apiRules < 0 {
		t.Fatalf("SystemPrompt left out a file or its path:\n%s", prompt)
	}
	if rootAt >= rootRules || rootRules >= apiAt || apiAt >= apiRules {
		t.Errorf("SystemPrompt does not give each file after its path, root first:\n%s", prompt)
	}
}

func TestSystemPromptSaysWhenAFileWasCutShort(t *testing.T) {
	prompt := SystemPrompt([]File{{Path: "/repo/AGENTS.md", Content: "rules", Truncated: true}})

	if !strings.Contains(prompt, "truncated") {
		t.Errorf("SystemPrompt does not say the file was cut short:\n%s", prompt)
	}
}

func TestSystemPromptWithoutFilesIsEmpty(t *testing.T) {
	if prompt := SystemPrompt(nil); prompt != "" {
		t.Errorf("SystemPrompt(nil) = %q, want nothing to send", prompt)
	}
}

func TestBelowReadsEachDirectoryDownToTheFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AGENTS.md"), "already read at startup")
	write(t, filepath.Join(dir, "services", "AGENTS.md"), "services")
	write(t, filepath.Join(dir, "services", "api", "CLAUDE.md"), "api")
	write(t, filepath.Join(dir, "services", "api", "v2", "handler.go"), "package v2")

	files, err := Below(dir, "services/api/v2/handler.go")
	if err != nil {
		t.Fatal(err)
	}

	want := []File{
		{Path: filepath.Join(dir, "services", "AGENTS.md"), Content: "services"},
		{Path: filepath.Join(dir, "services", "api", "CLAUDE.md"), Content: "api"},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Below = %+v, want %+v", files, want)
	}
}

func TestBelowAFileInDirFindsNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AGENTS.md"), "already read at startup")

	files, err := Below(dir, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("Below = %+v, want nothing", files)
	}
}

func TestBelowCutsAFileShortAtTheLimit(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "docs", "AGENTS.md"), strings.Repeat("a", MaxSize+1))

	files, err := Below(dir, "docs/index.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !files[0].Truncated {
		t.Errorf("Below = %d files, want the one, marked truncated", len(files))
	}
}

func TestAttachedReadsBackWhatAttachAdded(t *testing.T) {
	dir := "/repo"
	files := []File{
		{Path: "/repo/api/AGENTS.md", Content: "api rules"},
		{Path: "/repo/api/v2/CLAUDE.md", Content: "v2 rules", Truncated: true},
	}

	output := "package v2" + Attach(dir, files)

	if !strings.HasPrefix(output, "package v2") || !strings.Contains(output, "api rules") || !strings.Contains(output, "v2 rules") {
		t.Errorf("output = %q, want the tool's, then each file's instructions", output)
	}
	if !strings.Contains(output, "truncated") {
		t.Errorf("output = %q, want it to say a file was cut short", output)
	}
	// Named as the tools name files, relative to the working directory
	want := []string{"api/AGENTS.md", "api/v2/CLAUDE.md"}
	if got := Attached(output); !reflect.DeepEqual(got, want) {
		t.Errorf("Attached = %q, want %q", got, want)
	}
}

func TestAttachWithoutFilesAddsNothing(t *testing.T) {
	if got := Attach("/repo", nil); got != "" {
		t.Errorf("Attach = %q, want nothing", got)
	}
}

// A file the agent reads may itself hold something shaped like instructions.
// Only what Attach added counts.
func TestAttachedIgnoresInstructionsInTheToolsOwnOutput(t *testing.T) {
	output := "<instructions path=\"api/AGENTS.md\">\nnot really\n</instructions>"

	if got := Attached(output); len(got) != 0 {
		t.Errorf("Attached = %q, want nothing", got)
	}
}
