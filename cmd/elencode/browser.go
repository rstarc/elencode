package main

import (
	"os/exec"
	"runtime"
)

// openBrowser opens url in the user's browser. It does not wait for the
// browser: only whether it could be started is known, and the URL is printed
// either way for when it did not open.
func openBrowser(url string) error {
	argv := browserCommand(runtime.GOOS, url)
	// No stdout or stderr: an opener's chatter would land in the middle of the
	// session's frame.
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// browserCommand is the command that opens url on goos.
func browserCommand(goos, url string) []string {
	switch goos {
	case "darwin":
		return []string{"open", url}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", url}
	default:
		return []string{"xdg-open", url}
	}
}

// headless reports whether there is no browser here to open: an SSH session,
// whose browser is on the other end, or a Unix without a display server.
// Signing in then needs a code entered on another device instead.
func headless(getenv func(string) string, goos string) bool {
	if overSSH(getenv) {
		return true
	}
	if goos == "darwin" || goos == "windows" {
		return false
	}
	return getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == ""
}

// overSSH reports whether this is an SSH session, where the user's browser
// and clipboard are on the other end.
func overSSH(getenv func(string) string) bool {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if getenv(key) != "" {
			return true
		}
	}
	return false
}
