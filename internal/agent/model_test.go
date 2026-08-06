package agent

import "testing"

var catalog = []Model{
	{Provider: ProviderAnthropic, ID: "claude-x", DisplayName: "Claude X", Thinking: ThinkingEffort},
	{Provider: ProviderOpenAI, ID: "gpt-x", DisplayName: "GPT X", Thinking: ThinkingEffort},
}

func TestQualifiedNamesTheProviderAndTheID(t *testing.T) {
	m := Model{Provider: ProviderAnthropic, ID: "claude-x"}
	if got := m.Qualified(); got != "anthropic/claude-x" {
		t.Errorf("Qualified = %q, want %q", got, "anthropic/claude-x")
	}
}

func TestFindModelResolvesABareID(t *testing.T) {
	got, ok := FindModel(catalog, "gpt-x")
	if !ok {
		t.Fatal(`FindModel(catalog, "gpt-x") did not find it`)
	}
	if got != catalog[1] {
		t.Errorf("FindModel = %+v, want %+v", got, catalog[1])
	}
}

// The id is what the user types after /model, and a model id is not worth
// failing over a capital.
func TestFindModelMatchesCaseInsensitively(t *testing.T) {
	got, ok := FindModel(catalog, "Claude-X")
	if !ok {
		t.Fatal(`FindModel(catalog, "Claude-X") did not find it`)
	}
	if got != catalog[0] {
		t.Errorf("FindModel = %+v, want %+v", got, catalog[0])
	}
}

func TestFindModelRejectsAnUnknownID(t *testing.T) {
	if _, ok := FindModel(catalog, "gpt-nonexistent"); ok {
		t.Error("FindModel accepted an id the catalog does not know")
	}
}

// The escape hatch: a model released after the catalog was last edited is still
// reachable by naming its provider. Nothing is known about it beyond that, so
// it is assumed not to reason — asking for the wrong kind of thinking is
// rejected outright, while asking for none never is.
func TestFindModelAcceptsAQualifiedModelOutsideTheCatalog(t *testing.T) {
	got, ok := FindModel(catalog, "openai/gpt-nonexistent")
	if !ok {
		t.Fatal("FindModel rejected a qualified model outside the catalog")
	}

	want := Model{Provider: ProviderOpenAI, ID: "gpt-nonexistent", DisplayName: "gpt-nonexistent"}
	if got != want {
		t.Errorf("FindModel = %+v, want %+v", got, want)
	}
}

func TestFindModelRejectsAnUnknownProviderPrefix(t *testing.T) {
	if _, ok := FindModel(catalog, "acme/gpt-x"); ok {
		t.Error("FindModel accepted a provider that does not exist")
	}
}

// What the catalog knows beats what the qualified form can say on its own,
// which is only ever the id.
func TestFindModelPrefersTheCatalogEntryForAQualifiedKnownModel(t *testing.T) {
	got, ok := FindModel(catalog, "anthropic/claude-x")
	if !ok {
		t.Fatal("FindModel did not find a qualified catalog model")
	}
	if got != catalog[0] {
		t.Errorf("FindModel = %+v, want the catalog entry %+v", got, catalog[0])
	}
}

// A qualified name naming the wrong provider is not the catalog model: the
// entry belongs to whoever serves it.
func TestFindModelRejectsAKnownIDUnderTheWrongProvider(t *testing.T) {
	got, ok := FindModel(catalog, "openai/claude-x")
	if !ok {
		t.Fatal("FindModel rejected a qualified model")
	}
	if got.Provider != ProviderOpenAI || got.Thinking != ThinkingNone {
		t.Errorf("FindModel = %+v, want an openai model that does not reason", got)
	}
}
