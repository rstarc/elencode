package commands

import "testing"

func TestEffortCommandCarriesTheChosenLevel(t *testing.T) {
	tests := []struct {
		name, arg string
	}{
		// No argument asks for the slider; an argument names the level outright.
		{"slider", ""},
		{"named", "high"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := NewEffortCommand().Execute(test.arg)
			if cmd == nil {
				t.Fatal("/effort produced no command")
			}
			msg, ok := cmd().(ChooseEffortMsg)
			if !ok {
				t.Fatalf("/effort produced %T, want ChooseEffortMsg", cmd())
			}
			if msg.Level != test.arg {
				t.Errorf("Level = %q, want %q", msg.Level, test.arg)
			}
		})
	}
}
