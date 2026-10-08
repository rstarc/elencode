package main

import (
	"os"
	"os/exec"
	"os/user"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// shellPromptMsg carries the rendered shell prompt shown under the input
type shellPromptMsg string

// readShellPrompt looks up what the user's bash prompt shows. It runs as a
// command because asking git for the branch is I/O, and it runs again after
// every turn because the agent's tools may have switched the branch.
func readShellPrompt() tea.Msg {
	// Read from the system rather than $USER and $HOME, which elencode only
	// reads through config.Env
	current, err := user.Current()
	if err != nil {
		return shellPromptMsg("")
	}
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	dir, _ := os.Getwd()
	// Fails outside a repository and on a detached HEAD, which then shows no branch
	branch, _ := exec.Command("git", "symbolic-ref", "--short", "HEAD").Output()
	return shellPromptMsg(renderShellPrompt(current.Username, host, current.HomeDir, dir, strings.TrimSpace(string(branch))))
}

// renderShellPrompt lays the prompt out the way the user's PS1 does:
// user@host, the directory in bold blue and the branch in bold green.
func renderShellPrompt(user, host, home, dir, branch string) string {
	if dir == home {
		dir = "~"
	} else if strings.HasPrefix(dir, home+"/") {
		dir = "~" + strings.TrimPrefix(dir, home)
	}
	prompt := user + "@" + host + ": " + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Blue).Render(dir)
	if branch != "" {
		prompt += lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Green).Render(" (" + branch + ")")
	}
	return prompt
}
