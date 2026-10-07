package commands

import "testing"

// The provider and any flag after it are the TUI's to read, as the CLI's
// arguments are, so both read them one way.
func TestConnectCommandCarriesItsArguments(t *testing.T) {
	cmd := NewConnectCommand().Execute("chatgpt --device")
	if cmd == nil {
		t.Fatal("/connect produced no command")
	}
	msg, ok := cmd().(ConnectMsg)
	if !ok {
		t.Fatalf("/connect produced %T, want ConnectMsg", cmd())
	}
	if msg.Arg != "chatgpt --device" {
		t.Errorf("Arg = %q, want the whole argument", msg.Arg)
	}
}

func TestDisconnectCommandCarriesTheProvider(t *testing.T) {
	cmd := NewDisconnectCommand().Execute("chatgpt")
	if cmd == nil {
		t.Fatal("/disconnect produced no command")
	}
	msg, ok := cmd().(DisconnectMsg)
	if !ok {
		t.Fatalf("/disconnect produced %T, want DisconnectMsg", cmd())
	}
	if msg.Provider != "chatgpt" {
		t.Errorf("Provider = %q, want chatgpt", msg.Provider)
	}
}

// What these commands used to be called still works: login and logout are
// what a sign-in has been called for as long as there have been sign-ins.
func TestConnectAndDisconnectAnswerToTheirOldNames(t *testing.T) {
	if got := NewConnectCommand().Aliases; len(got) != 1 || got[0] != "login" {
		t.Errorf("connect aliases = %v, want login", got)
	}
	if got := NewDisconnectCommand().Aliases; len(got) != 1 || got[0] != "logout" {
		t.Errorf("disconnect aliases = %v, want logout", got)
	}
}
