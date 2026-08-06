package anthropic

import (
	"testing"

	"github.com/rstarc/elencode/internal/agent"
)

// The catalog is written by hand, so the invariants the listing endpoint used
// to guarantee for free are the ones worth pinning.
func TestCatalogNamesEveryModelOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, model := range Catalog() {
		if seen[model.ID] {
			t.Errorf("%q is in the catalog twice", model.ID)
		}
		seen[model.ID] = true
	}
}

func TestCatalogEntriesAllNameThisProvider(t *testing.T) {
	for _, model := range Catalog() {
		if model.Provider != agent.ProviderAnthropic {
			t.Errorf("%q names provider %q, want anthropic", model.ID, model.Provider)
		}
		if model.DisplayName == "" {
			t.Errorf("%q has no display name to recognise it by", model.ID)
		}
	}
}

func TestDefaultIsInTheCatalog(t *testing.T) {
	found, ok := agent.FindModel(Catalog(), Default().ID)
	if !ok {
		t.Fatalf("the default model %q is not in the catalog", Default().ID)
	}
	if found != Default() {
		t.Errorf("Default = %+v, want the catalog entry %+v", Default(), found)
	}
}

// The caller concatenates this with another provider's, which would write into
// the catalog itself if it were handed the same backing array.
func TestCatalogCannotBeCorruptedByItsCaller(t *testing.T) {
	first := Catalog()
	first[0] = agent.Model{ID: "scribbled-over"}

	if Catalog()[0].ID == "scribbled-over" {
		t.Error("a caller's write reached the catalog")
	}
}
