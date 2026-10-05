package main

import (
	"slices"
	"testing"
)

func TestBrowserCommandUsesEachPlatformsOpener(t *testing.T) {
	const url = "https://auth.example/oauth/authorize?a=1&b=2"
	tests := []struct {
		goos string
		want []string
	}{
		{"linux", []string{"xdg-open", url}},
		{"freebsd", []string{"xdg-open", url}},
		{"darwin", []string{"open", url}},
		// Not cmd /c start, which reads & as a command separator
		{"windows", []string{"rundll32", "url.dll,FileProtocolHandler", url}},
	}
	for _, test := range tests {
		if got := browserCommand(test.goos, url); !slices.Equal(got, test.want) {
			t.Errorf("browserCommand(%s) = %q, want %q", test.goos, got, test.want)
		}
	}
}

// env is a fake environment for headless.
func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestHeadlessOverSSH(t *testing.T) {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		for _, goos := range []string{"linux", "darwin"} {
			if !headless(env(map[string]string{key: "x", "DISPLAY": ":0"}), goos) {
				t.Errorf("%s on %s is not headless", key, goos)
			}
		}
	}
}

// Without a display server there is nothing for xdg-open to open a browser on.
func TestHeadlessOnLinuxWithoutADisplay(t *testing.T) {
	if !headless(env(nil), "linux") {
		t.Error("linux without a display is not headless")
	}
	for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY"} {
		if headless(env(map[string]string{key: "x"}), "linux") {
			t.Errorf("linux with %s is headless", key)
		}
	}
}

// macOS and Windows have a desktop without any of those variables.
func TestNotHeadlessOnALocalDesktop(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		if headless(env(nil), goos) {
			t.Errorf("a local %s session is headless", goos)
		}
	}
}

func TestOverSSH(t *testing.T) {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if !overSSH(env(map[string]string{key: "x"})) {
			t.Errorf("%s is not an SSH session", key)
		}
	}
	if overSSH(env(map[string]string{"DISPLAY": ":0"})) {
		t.Error("a local session is over SSH")
	}
}
