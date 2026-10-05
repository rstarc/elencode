package main

import (
	"fmt"
	"io"
	"runtime/debug"
)

// cliCommand is something `elencode <name>` does instead of starting a
// session.
type cliCommand struct {
	Name string
	Run  func(args []string, out io.Writer) error
}

// cliCommands is every command the CLI knows. They all run before the config
// is loaded, so each loads what it needs — and signing in works without an
// API key.
func cliCommands() []cliCommand {
	return []cliCommand{
		{Name: "login", Run: loginCLI},
		{Name: "logout", Run: logoutCLI},
		{Name: "version", Run: versionCLI},
	}
}

// runCLI runs the command args names, reporting whether there was one to run.
// Anything else is the session itself, which is the caller's to start.
func runCLI(args []string, out io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	for _, c := range cliCommands() {
		if c.Name == args[0] {
			return true, c.Run(args[1:], out)
		}
	}
	return false, nil
}

func versionCLI(_ []string, out io.Writer) error {
	bi, ok := debug.ReadBuildInfo()
	_, err := fmt.Fprintln(out, versionLine(version, bi, ok))
	return err
}
