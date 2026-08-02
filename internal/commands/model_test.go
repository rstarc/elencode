package commands

import "testing"

func TestModelCommandCarriesTheChosenID(t *testing.T) {
	tests := []struct {
		name, arg string
	}{
		// No argument asks for the picker; an argument names the model outright.
		{"picker", ""},
		{"named", "claude-opus-4-5"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := NewModelCommand().Execute(test.arg)
			if cmd == nil {
				t.Fatal("/model produced no command")
			}
			msg, ok := cmd().(ChooseModelMsg)
			if !ok {
				t.Fatalf("/model produced %T, want ChooseModelMsg", cmd())
			}
			if msg.ID != test.arg {
				t.Errorf("ID = %q, want %q", msg.ID, test.arg)
			}
		})
	}
}
