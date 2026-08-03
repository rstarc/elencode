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

func TestThinkingBlockCarriesID(t *testing.T) {
	b := ThinkingBlock{Thinking: "t", Signature: "s", ID: "rs_1"}
	if b.ID != "rs_1" {
		t.Fatalf("ID not carried: %q", b.ID)
	}
	if ThinkingEffort != ThinkingMode("effort") {
		t.Fatalf("ThinkingEffort = %q", ThinkingEffort)
	}
}
