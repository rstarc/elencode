package commands

import "testing"

func TestConfigCommandOpensTheConfigView(t *testing.T) {
	cmd := NewConfigCommand().Execute("")
	if cmd == nil {
		t.Fatal("/config produced no command")
	}
	if _, ok := cmd().(ShowConfigMsg); !ok {
		t.Errorf("/config produced %T, want ShowConfigMsg", cmd())
	}
}
