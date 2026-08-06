package anthropic

import (
	"slices"

	"github.com/rstarc/elencode/internal/agent"
)

// models is what the picker offers for this provider, newest first.
//
// Written by hand rather than read from /v1/models, which cannot say what a
// model is called well enough to pick from and costs a request at startup. The
// thinking mode is the part that matters: it was transcribed from the
// capabilities the endpoint reports, through the same precedence the code that
// read them used — effort beats adaptive beats budgeted. A wrong value here
// does not degrade, it fails the turn, since asking a model for a kind of
// thinking it does not accept is rejected outright.
//
// Ids are the undated aliases: a dated snapshot would pin a session to a model
// that eventually retires. A model missing from this list is still reachable
// as "anthropic/<id>", which assumes no thinking.
var models = []agent.Model{
	{ID: "claude-opus-5", DisplayName: "Claude Opus 5", Thinking: agent.ThinkingEffort},
	{ID: "claude-sonnet-5", DisplayName: "Claude Sonnet 5", Thinking: agent.ThinkingEffort},
	{ID: "claude-fable-5", DisplayName: "Claude Fable 5", Thinking: agent.ThinkingEffort},
	{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", Thinking: agent.ThinkingEffort},
	{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7", Thinking: agent.ThinkingEffort},
	{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6", Thinking: agent.ThinkingEffort},
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", Thinking: agent.ThinkingEffort},
	{ID: "claude-opus-4-5", DisplayName: "Claude Opus 4.5", Thinking: agent.ThinkingEffort},
	{ID: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", Thinking: agent.ThinkingBudgeted},
	{ID: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5", Thinking: agent.ThinkingBudgeted},
}

// defaultModel is used when configuration names none.
const defaultModel = "claude-haiku-4-5"

// Catalog is every model this provider offers, in the order the picker lists
// them. A copy, because the caller concatenates it with another provider's.
func Catalog() []agent.Model {
	catalog := slices.Clone(models)
	for i := range catalog {
		catalog[i].Provider = agent.ProviderAnthropic
	}
	return catalog
}

// Default is the model a session opens on when configuration names none.
func Default() agent.Model {
	model, _ := agent.FindModel(Catalog(), defaultModel)
	return model
}
