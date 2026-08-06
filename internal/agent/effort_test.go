package agent

import "testing"

func TestEffortWireValues(t *testing.T) {
	cases := map[Effort]string{
		EffortNone: "", EffortLow: "low", EffortMedium: "medium",
		EffortHigh: "high", EffortXHigh: "xhigh", EffortMax: "max",
	}
	for e, want := range cases {
		if string(e) != want {
			t.Errorf("Effort %q: got %q, want %q", e, string(e), want)
		}
	}
}

func TestParseEffortAcceptsEveryLevel(t *testing.T) {
	for _, want := range Efforts {
		t.Run(string(want), func(t *testing.T) {
			got, ok := ParseEffort(string(want))
			if !ok {
				t.Fatalf("ParseEffort(%q) rejected a level it lists", want)
			}
			if got != want {
				t.Errorf("ParseEffort(%q) = %q", want, got)
			}
		})
	}
}

// Unset is a level of its own: it means the API picks, and the two APIs do not
// pick the same one.
func TestParseEffortReadsUnsetAsNone(t *testing.T) {
	got, ok := ParseEffort("")
	if !ok {
		t.Fatal(`ParseEffort("") rejected an unset effort`)
	}
	if got != EffortNone {
		t.Errorf(`ParseEffort("") = %q, want none`, got)
	}
}

// A typo must be caught where it is read, rather than silently reasoning at
// some other level.
func TestParseEffortRejectsUnknownLevels(t *testing.T) {
	for _, in := range []string{"turbo", "LOW", " high"} {
		t.Run(in, func(t *testing.T) {
			if _, ok := ParseEffort(in); ok {
				t.Errorf("ParseEffort(%q) accepted an unknown level", in)
			}
		})
	}
}

func TestThinkingBlockCarriesID(t *testing.T) {
	b := ThinkingBlock{Thinking: "t", Signature: "s", ID: "rs_1"}
	if b.ID != "rs_1" {
		t.Fatalf("ID not carried: %q", b.ID)
	}
	if ThinkingEffort != ThinkingMode("effort") {
		t.Fatalf("ThinkingEffort = %q", ThinkingEffort)
	}
}
