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

// moonshotModels is what Kimi's Anthropic-compatible API serves, newest first.
//
// Every one of them reasons, with or without being asked, and none is sent a
// thinking parameter (see NewMoonshot). Only kimi-k3 takes an effort level;
// the others are ThinkingNone because nothing about their reasoning can be set.
var moonshotModels = []agent.Model{
	{ID: "kimi-k3", DisplayName: "Kimi K3", Thinking: agent.ThinkingEffort},
	{ID: "kimi-k2.7-code", DisplayName: "Kimi K2.7 Code", Thinking: agent.ThinkingNone},
	{ID: "kimi-k2.7-code-highspeed", DisplayName: "Kimi K2.7 Code Highspeed", Thinking: agent.ThinkingNone},
	{ID: "kimi-k2.6", DisplayName: "Kimi K2.6", Thinking: agent.ThinkingNone},
}

// defaultMoonshotModel is the newest of moonshotModels.
const defaultMoonshotModel = "kimi-k3"

// Catalog is every model this provider offers, in the order the picker lists
// them.
func Catalog() []agent.Model {
	return withProvider(models, agent.ProviderAnthropic)
}

// MoonshotCatalog is every model Kimi's API serves, in picker order.
func MoonshotCatalog() []agent.Model {
	return withProvider(moonshotModels, agent.ProviderMoonshot)
}

// withProvider stamps provider onto a copy of list. A copy, because the caller
// concatenates it with another provider's.
func withProvider(list []agent.Model, provider agent.ProviderName) []agent.Model {
	catalog := slices.Clone(list)
	for i := range catalog {
		catalog[i].Provider = provider
	}
	return catalog
}

// Default is the model a session opens on when configuration names none.
func Default() agent.Model {
	model, _ := agent.FindModel(Catalog(), defaultModel)
	return model
}

// MoonshotDefault is the model a session opens on when Moonshot is the
// provider it starts with and configuration names no model.
func MoonshotDefault() agent.Model {
	model, _ := agent.FindModel(MoonshotCatalog(), defaultMoonshotModel)
	return model
}
