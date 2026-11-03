package bedrock

import (
	"testing"

	"github.com/rstarc/elencode/internal/agent"
)

func TestCatalogModelsBelongToBedrock(t *testing.T) {
	for _, model := range Catalog() {
		if model.Provider != agent.ProviderBedrock {
			t.Errorf("%s has provider %q, want %q", model.ID, model.Provider, agent.ProviderBedrock)
		}
	}
}

func TestCatalogIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, model := range Catalog() {
		if seen[model.ID] {
			t.Errorf("%s is listed twice", model.ID)
		}
		seen[model.ID] = true
	}
}

func TestDefaultIsInTheCatalog(t *testing.T) {
	model := Default()
	if _, ok := agent.FindModel(Catalog(), model.Qualified()); !ok || model.ID == "" {
		t.Errorf("Default() = %#v, want a model from the catalog", model)
	}
}
