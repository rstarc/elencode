package agent

import "strings"

// ProviderName says which API serves a model. A type rather than a string
// because it is half of a model's identity and travels with it everywhere —
// including into a provider's own Request, which must never read it: a client
// knows only its own API, which is the whole point of the boundary.
type ProviderName string

const (
	ProviderAnthropic ProviderName = "anthropic"
	ProviderOpenAI    ProviderName = "openai"
)

// Providers is every provider elencode can talk to, in the order it prefers
// them when nothing else decides — which is only ever at startup, when a key
// was found for more than one and the config named no model.
var Providers = []ProviderName{ProviderAnthropic, ProviderOpenAI}

// Model is one model a provider offers, as shown in the picker
type Model struct {
	Provider    ProviderName
	ID          string
	DisplayName string
	Thinking    ThinkingMode
}

// Qualified is how a model is written where the provider matters: what a user
// types after /model to reach past the catalog, and what the config file
// stores, where a bare id would not say who to ask for it.
func (m Model) Qualified() string { return string(m.Provider) + "/" + m.ID }

// ThinkingMode is how a model can be asked to reason, if at all. Models differ,
// and asking for the wrong kind is rejected outright rather than ignored, so
// the request has to be built from what the model actually accepts.
type ThinkingMode string

const (
	// ThinkingNone: the model cannot be asked to reason
	ThinkingNone ThinkingMode = ""
	// ThinkingAdaptive: the model decides for itself how much to reason
	ThinkingAdaptive ThinkingMode = "adaptive"
	// ThinkingBudgeted: the model reasons within a token budget the caller sets
	ThinkingBudgeted ThinkingMode = "budgeted"
	// ThinkingEffort: the model reasons at a discrete effort level the caller
	// picks (OpenAI reasoning_effort; Anthropic OutputConfig.Effort).
	ThinkingEffort ThinkingMode = "effort"
)

// FindModel resolves a name to one of models, or to a model outside them.
//
// A bare id has to be one of models, matched however it was capitalised: it is
// the only way to know who serves it. A qualified "provider/id" says that
// outright, so it resolves even for a model released since this build — with
// nothing assumed about it beyond its name. Not reasoning is the safe
// assumption there: a request is rejected for asking for the wrong kind of
// thinking, never for asking for none.
func FindModel(models []Model, name string) (Model, bool) {
	prefix, id, qualified := strings.Cut(name, "/")
	if !qualified {
		return findByID(models, ProviderName(""), name)
	}

	provider := ProviderName(prefix)
	if provider != ProviderAnthropic && provider != ProviderOpenAI {
		return Model{}, false
	}
	if known, ok := findByID(models, provider, id); ok {
		return known, true
	}
	return Model{Provider: provider, ID: id, DisplayName: id}, true
}

// findByID looks up id among models, optionally restricted to one provider. An
// empty provider matches any, which is what a bare id asks for.
func findByID(models []Model, provider ProviderName, id string) (Model, bool) {
	for _, candidate := range models {
		if provider != "" && candidate.Provider != provider {
			continue
		}
		if strings.EqualFold(candidate.ID, id) {
			return candidate, true
		}
	}
	return Model{}, false
}
