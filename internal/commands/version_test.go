package commands

import "testing"

func TestVersionCommandAsksForTheVersion(t *testing.T) {
	cmd := NewVersionCommand().Execute("")
	if cmd == nil {
		t.Fatal("/version produced no command")
	}
	if _, ok := cmd().(ShowVersionMsg); !ok {
		t.Errorf("/version produced %T, want ShowVersionMsg", cmd())
	}
}
