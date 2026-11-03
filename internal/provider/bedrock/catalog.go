package bedrock

import (
	"github.com/rstarc/elencode/internal/agent"
)

// entry is a model this provider offers, and whether Bedrock runs it only
// through an inference profile.
type entry struct {
	model agent.Model
	// profile is set for a model with no in-region endpoint: it is sent as
	// the inference profile for the client's region (see profileID).
	profile bool
}

// models is what the picker offers for this provider, newest first.
//
// Written by hand rather than read from ListFoundationModels, which lists
// every model in the region whether or not the account may use it, says
// nothing about which of them reason, and is not reached with a Bedrock API
// key. Ids are Bedrock's own, without a profile's prefix: which prefix applies
// depends on the region, not the model.
//
// The thinking mode also says how reasoning is asked for, which differs by
// vendor: see reasoningFields. DeepSeek-R1 always reasons and takes no
// setting, so it is ThinkingNone. Claude Haiku 5.5 is ThinkingNone until it
// is known which kind of thinking it accepts on Bedrock: asking for the wrong
// kind fails the turn.
var models = []entry{
	{model: agent.Model{ID: "anthropic.claude-opus-5-5", DisplayName: "Claude Opus 5.5", Thinking: agent.ThinkingEffort}, profile: true},
	{model: agent.Model{ID: "anthropic.claude-opus-5", DisplayName: "Claude Opus 5", Thinking: agent.ThinkingEffort}, profile: true},
	{model: agent.Model{ID: "anthropic.claude-sonnet-5", DisplayName: "Claude Sonnet 5", Thinking: agent.ThinkingEffort}, profile: true},
	{model: agent.Model{ID: "anthropic.claude-haiku-5-5", DisplayName: "Claude Haiku 5.5", Thinking: agent.ThinkingNone}, profile: true},
	{model: agent.Model{ID: "openai.gpt-oss-120b-1:0", DisplayName: "gpt-oss-120b", Thinking: agent.ThinkingEffort}},
	{model: agent.Model{ID: "openai.gpt-oss-20b-1:0", DisplayName: "gpt-oss-20b", Thinking: agent.ThinkingEffort}},
	{model: agent.Model{ID: "deepseek.r1-v1:0", DisplayName: "DeepSeek-R1", Thinking: agent.ThinkingNone}, profile: true},
}

// defaultModel is used when configuration names none.
const defaultModel = "anthropic.claude-sonnet-5"

// Catalog is every model this provider offers, in the order the picker lists
// them.
func Catalog() []agent.Model {
	catalog := make([]agent.Model, 0, len(models))
	for _, e := range models {
		model := e.model
		model.Provider = agent.ProviderBedrock
		catalog = append(catalog, model)
	}
	return catalog
}

// Default is the model a session opens on when configuration names none.
func Default() agent.Model {
	model, _ := agent.FindModel(Catalog(), string(agent.ProviderBedrock)+"/"+defaultModel)
	return model
}

// needsProfile reports whether id is a shipped model that runs only through
// an inference profile. An id named by hand is sent as it was written.
func needsProfile(id string) bool {
	for _, e := range models {
		if e.model.ID == id {
			return e.profile
		}
	}
	return false
}
