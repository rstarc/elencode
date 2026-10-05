package commands

import "testing"

// The provider and any flag after it are the TUI's to read, as the CLI's
// arguments are, so both read them one way.
func TestLoginCommandCarriesItsArguments(t *testing.T) {
	cmd := NewLoginCommand().Execute("chatgpt --device")
	if cmd == nil {
		t.Fatal("/login produced no command")
	}
	msg, ok := cmd().(LoginMsg)
	if !ok {
		t.Fatalf("/login produced %T, want LoginMsg", cmd())
	}
	if msg.Arg != "chatgpt --device" {
		t.Errorf("Arg = %q, want the whole argument", msg.Arg)
	}
}

func TestLogoutCommandCarriesTheProvider(t *testing.T) {
	cmd := NewLogoutCommand().Execute("chatgpt")
	if cmd == nil {
		t.Fatal("/logout produced no command")
	}
	msg, ok := cmd().(LogoutMsg)
	if !ok {
		t.Fatalf("/logout produced %T, want LogoutMsg", cmd())
	}
	if msg.Provider != "chatgpt" {
		t.Errorf("Provider = %q, want chatgpt", msg.Provider)
	}
}
