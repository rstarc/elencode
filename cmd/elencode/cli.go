package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
)

// cliCommand is something `elencode <name>` does instead of starting a
// session. Every one has a slash command of the same name, and every slash
// command has one of these unless slashOnly says why not: cli_test.go holds
// the two lists to that.
type cliCommand struct {
	Name string
	Run  func(args []string, out io.Writer) error
}

// slashOnly are the slash commands with no CLI equivalent, each because it
// only means something inside a session.
var slashOnly = []string{
	"quit", // outside a session there is nothing to quit
}

// cliCommands is every command the CLI knows. They all run before the config
// is loaded, so each loads what it needs — and signing in works without an
// API key.
func cliCommands() []cliCommand {
	return []cliCommand{
		{Name: "config", Run: configCLI},
		{Name: "model", Run: modelCLI},
		{Name: "login", Run: loginCLI},
		{Name: "logout", Run: logoutCLI},
		{Name: "version", Run: versionCLI},
	}
}

// runCLI runs the command args names, reporting whether there was one to run.
// No arguments is the session itself, which is the caller's to start.
func runCLI(args []string, out io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	for _, c := range cliCommands() {
		if c.Name == args[0] {
			return true, c.Run(args[1:], out)
		}
	}
	var names []string
	for _, c := range cliCommands() {
		names = append(names, c.Name)
	}
	return true, fmt.Errorf("unknown command %q (commands: %s)", args[0], strings.Join(names, ", "))
}

func versionCLI(_ []string, out io.Writer) error {
	bi, ok := debug.ReadBuildInfo()
	_, err := fmt.Fprintln(out, versionLine(version, bi, ok))
	return err
}

// configCLI is `elencode config`: what /config would show in a session
// started now, the model included.
func configCLI(_ []string, out io.Writer) error {
	cfg, _, _, err := loadSession(io.Discard)
	if err != nil {
		return err
	}
	printConfig(cfg, out)
	return nil
}

// modelCLI is `elencode model [id]`.
func modelCLI(args []string, out io.Writer) error {
	cfg, providers, current, err := loadSession(out)
	if err != nil {
		return err
	}
	return runModelCLI(cfg, providers, catalog(), current, strings.Join(args, " "), out)
}

// runModelCLI lists the models a session could use, marking the one it would
// start on, or with a name, makes that the one. There is no session to switch,
// so choosing a model is saving it for the next: what /model saves too.
func runModelCLI(cfg config.Config, providers providerSet, models []agent.Model, current agent.Model, name string, out io.Writer) error {
	if name == "" {
		for _, model := range reachableModels(models, providers) {
			marker := " "
			if model.Qualified() == current.Qualified() {
				marker = "*"
			}
			_, _ = fmt.Fprintf(out, "%s %-40s %s\n", marker, model.Qualified(), model.DisplayName)
		}
		return nil
	}

	chosen, err := resolveModel(models, providers, name)
	if err != nil {
		return err
	}
	cfg.Model = chosen.Qualified()
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving the model to %s: %w", cfg.Path, err)
	}
	_, _ = fmt.Fprintf(out, "The next session starts on %s.\n", chosen.Qualified())
	return nil
}
