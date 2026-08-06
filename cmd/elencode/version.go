package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// developmentVersion is what an unstamped build reports. Real builds get the tag
// via -ldflags -X main.version=$(git describe --tags --always --dirty); see the
// Makefile.
const developmentVersion = "dev"

var version = developmentVersion

const shortCommitLen = 12

// versionLine renders the single line `elencode version` prints. Build info is a
// parameter rather than read here so the formatting is testable without a
// stamped binary.
func versionLine(stamped string, bi *debug.BuildInfo, ok bool) string {
	if !ok {
		bi = nil
	}

	settings := make(map[string]string)
	if bi != nil {
		for _, s := range bi.Settings {
			settings[s.Key] = s.Value
		}
	}

	commit := settings["vcs.revision"]
	if len(commit) > shortCommitLen {
		commit = commit[:shortCommitLen]
	}

	// The commit stands in for the version only when nothing better is known. It
	// is then dropped from the details, so it is not printed twice.
	fromCommit := false
	if stamped == developmentVersion || stamped == "" {
		switch {
		case bi != nil && bi.Main.Version != "" && bi.Main.Version != "(devel)":
			stamped = bi.Main.Version
		case commit != "":
			stamped, fromCommit = commit, true
			if settings["vcs.modified"] == "true" {
				stamped += "-dirty"
			}
		default:
			stamped = "unknown"
		}
	}

	var details []string
	if commit != "" && !fromCommit {
		details = append(details, "commit "+commit)
	}
	if built := settings["vcs.time"]; built != "" {
		details = append(details, "built "+built)
	}

	goVersion := runtime.Version()
	if bi != nil && bi.GoVersion != "" {
		goVersion = bi.GoVersion
	}
	details = append(details, goVersion)

	goos, goarch := settings["GOOS"], settings["GOARCH"]
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	details = append(details, goos+"/"+goarch)

	return fmt.Sprintf("elencode %s (%s)", stamped, strings.Join(details, ", "))
}
