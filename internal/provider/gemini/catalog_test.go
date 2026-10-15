package gemini

import (
	"testing"

	"github.com/rstarc/elencode/internal/agent"
)

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
		if model.Provider != agent.ProviderGemini {
			t.Errorf("%q names provider %q, want gemini", model.ID, model.Provider)
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

func TestCatalogCannotBeCorruptedByItsCaller(t *testing.T) {
	first := Catalog()
	first[0] = agent.Model{ID: "scribbled-over"}

	if Catalog()[0].ID == "scribbled-over" {
		t.Error("a caller's write reached the catalog")
	}
}
