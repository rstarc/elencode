package commands

import "testing"

func TestQuitCommandAsksToExit(t *testing.T) {
	cmd := NewQuitCommand().Execute("")
	if cmd == nil {
		t.Fatal("/quit produced no command")
	}
	if _, ok := cmd().(QuitMsg); !ok {
		t.Errorf("/quit produced %T, want QuitMsg", cmd())
	}
}
