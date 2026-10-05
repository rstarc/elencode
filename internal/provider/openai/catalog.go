package openai

import (
	"slices"

	"github.com/rstarc/elencode/internal/agent"
)

// models is what the picker offers for this provider, newest first.
//
// Written by hand rather than read from /v1/models, which lists audio, image,
// embedding, transcription and moderation models that cannot hold a
// conversation, names no display name to recognise a model by, and says
// nothing about which of them reason. That last part is the reason this list
// exists: it used to be guessed from an id prefix, and a guess that goes wrong
// sends a reasoning parameter to a model that rejects it.
//
// A model missing from this list is still reachable as "openai/<id>", which
// assumes no thinking. Deliberately absent: the -pro tiers and the codex
// variants, which this build has never been run against.
var models = []agent.Model{
	{ID: "gpt-5.6-luna", DisplayName: "GPT-5.6 Luna", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6 Sol", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.6-terra", DisplayName: "GPT-5.6 Terra", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.5", DisplayName: "GPT-5.5", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.4", DisplayName: "GPT-5.4", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.4-mini", DisplayName: "GPT-5.4 mini", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.4-nano", DisplayName: "GPT-5.4 nano", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.2", DisplayName: "GPT-5.2", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.1", DisplayName: "GPT-5.1", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5", DisplayName: "GPT-5", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5-mini", DisplayName: "GPT-5 mini", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5-nano", DisplayName: "GPT-5 nano", Thinking: agent.ThinkingEffort},
	{ID: "o4-mini", DisplayName: "o4-mini", Thinking: agent.ThinkingEffort},
	{ID: "o3", DisplayName: "o3", Thinking: agent.ThinkingEffort},
	{ID: "gpt-4.1", DisplayName: "GPT-4.1", Thinking: agent.ThinkingNone},
	{ID: "gpt-4o", DisplayName: "GPT-4o", Thinking: agent.ThinkingNone},
}

// defaultChatGPTModel is the newest of chatGPTModels.
const defaultChatGPTModel = "gpt-6.1-sol"

// chatGPTModels is what the ChatGPT backend serves to a signed-in account,
// newest first. A plan that does not include one gets the backend's refusal
// on the first message.
var chatGPTModels = []agent.Model{
	{ID: "gpt-6.1-sol", DisplayName: "GPT-6.1 Sol", Thinking: agent.ThinkingEffort},
	{ID: "gpt-6-astra", DisplayName: "GPT-6 Astra", Thinking: agent.ThinkingEffort},
	{ID: "gpt-6-sol", DisplayName: "GPT-6 Sol", Thinking: agent.ThinkingEffort},
	{ID: "gpt-6-luna", DisplayName: "GPT-6 Luna", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6 Sol", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.6-terra", DisplayName: "GPT-5.6 Terra", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.6-luna", DisplayName: "GPT-5.6 Luna", Thinking: agent.ThinkingEffort},
	{ID: "gpt-5.5", DisplayName: "GPT-5.5", Thinking: agent.ThinkingEffort},
}

// Catalog is every model this provider offers, in the order the picker lists
// them.
func Catalog() []agent.Model {
	return withProvider(models, agent.ProviderOpenAI)
}

// ChatGPTCatalog is every model the ChatGPT backend serves, in picker order.
func ChatGPTCatalog() []agent.Model {
	return withProvider(chatGPTModels, agent.ProviderChatGPT)
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

// ChatGPTDefault is the model a session opens on when the ChatGPT login is the
// provider it starts with and configuration names no model.
func ChatGPTDefault() agent.Model {
	model, _ := agent.FindModel(ChatGPTCatalog(), defaultChatGPTModel)
	return model
}
