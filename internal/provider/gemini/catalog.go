package gemini

import (
	"slices"

	"github.com/rstarc/elencode/internal/agent"
)

// models is what the picker offers for this provider, newest first.
//
// Written by hand, as every provider's catalog is. Each of these takes a
// thinking level, which is what ThinkingEffort sends; the 2.5 models take a
// token budget instead, and are left out rather than sent the wrong kind. A
// model missing from this list is still reachable as "gemini/<id>", which
// assumes no thinking.
var models = []agent.Model{
	{ID: "gemini-4-argon", DisplayName: "Gemini 4 Argon", Thinking: agent.ThinkingEffort},
	{ID: "gemini-3.8-flash", DisplayName: "Gemini 3.8 Flash", Thinking: agent.ThinkingEffort},
	{ID: "gemini-3.5-flash-lite", DisplayName: "Gemini 3.5 Flash-Lite", Thinking: agent.ThinkingEffort},
	{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro Preview", Thinking: agent.ThinkingEffort},
}

// defaultModel is used when configuration names none.
const defaultModel = "gemini-4-argon"

// Catalog is every model this provider offers, in the order the picker lists
// them. A copy, because the caller concatenates it with another provider's.
func Catalog() []agent.Model {
	catalog := slices.Clone(models)
	for i := range catalog {
		catalog[i].Provider = agent.ProviderGemini
	}
	return catalog
}

// Default is the model a session opens on when Gemini is the provider and
// configuration names no model.
func Default() agent.Model {
	model, _ := agent.FindModel(Catalog(), defaultModel)
	return model
}
