package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{
		GoVersion: "go1.26.1",
		Main:      debug.Module{Version: mainVersion},
		Settings:  settings,
	}
}

func TestVersionLinePrefersTheStampedVersion(t *testing.T) {
	line := versionLine("v1.2.3", buildInfo("(devel)",
		debug.BuildSetting{Key: "vcs.revision", Value: "d793654a1b2c3d4e"},
	), true)

	if !strings.Contains(line, "v1.2.3") {
		t.Errorf("stamped version missing from %q", line)
	}
}

// Without ldflags the tag is only knowable when the binary came from
// `go install module@version`; a plain `go build` leaves "(devel)".
func TestVersionLineFallsBackToTheModuleVersion(t *testing.T) {
	line := versionLine(developmentVersion, buildInfo("v0.4.0"), true)

	if !strings.Contains(line, "v0.4.0") {
		t.Errorf("module version missing from %q", line)
	}
}

func TestVersionLineFallsBackToTheCommit(t *testing.T) {
	line := versionLine(developmentVersion, buildInfo("(devel)",
		debug.BuildSetting{Key: "vcs.revision", Value: "d793654a1b2c3d4e5f60"},
	), true)

	if !strings.Contains(line, "d793654a1b2c") {
		t.Errorf("short commit missing from %q", line)
	}
	if strings.Contains(line, "d793654a1b2c3") {
		t.Errorf("commit not shortened in %q", line)
	}
}

func TestVersionLineMarksADirtyTree(t *testing.T) {
	line := versionLine(developmentVersion, buildInfo("(devel)",
		debug.BuildSetting{Key: "vcs.revision", Value: "d793654a1b2c3d4e"},
		debug.BuildSetting{Key: "vcs.modified", Value: "true"},
	), true)

	if !strings.Contains(line, "dirty") {
		t.Errorf("dirty tree not marked in %q", line)
	}
}

func TestVersionLineWithoutBuildInfo(t *testing.T) {
	line := versionLine(developmentVersion, nil, false)

	if !strings.Contains(line, "unknown") {
		t.Errorf("want an unknown version, got %q", line)
	}
}

func TestVersionLineReportsBuildDetails(t *testing.T) {
	line := versionLine("v1.2.3", buildInfo("(devel)",
		debug.BuildSetting{Key: "vcs.revision", Value: "d793654a1b2c3d4e"},
		debug.BuildSetting{Key: "vcs.time", Value: "2026-08-06T07:21:00Z"},
		debug.BuildSetting{Key: "GOOS", Value: "linux"},
		debug.BuildSetting{Key: "GOARCH", Value: "amd64"},
	), true)

	for _, want := range []string{"elencode", "d793654a1b2c", "2026-08-06T07:21:00Z", "go1.26.1", "linux/amd64"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q missing from %q", want, line)
		}
	}
}

// Absent build settings are dropped rather than printed empty, so a binary built
// outside a checkout still reports a well formed line.
func TestVersionLineIsAlwaysOneLine(t *testing.T) {
	tests := []struct {
		name    string
		version string
		info    *debug.BuildInfo
		ok      bool
	}{
		{"stamped", "v1.2.3", buildInfo("(devel)"), true},
		{"bare build info", developmentVersion, buildInfo(""), true},
		{"no build info", developmentVersion, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := versionLine(tt.version, tt.info, tt.ok)

			if strings.Contains(line, "\n") {
				t.Errorf("version output spans multiple lines: %q", line)
			}
			if strings.Contains(line, "()") || strings.Contains(line, ", ,") {
				t.Errorf("empty field rendered in %q", line)
			}
		})
	}
}
