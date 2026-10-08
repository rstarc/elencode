package anthropic

import (
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rstarc/elencode/internal/agent"
)

// moonshotBaseURL is Kimi's Anthropic-compatible API. It serves the Messages
// API, so everything but the request's edges is the same client: the key goes
// as a bearer token, no thinking parameter is sent, and effort has Kimi's
// levels.
const moonshotBaseURL = "https://api.moonshot.ai/anthropic/"

// moonshotModelsPath is where CheckKey lists models, relative to
// moonshotBaseURL. The Anthropic-compatible API has no listing of its own, so
// this steps out of /anthropic to the OpenAI-compatible API's: the same host
// and key.
const moonshotModelsPath = "../v1/models"

// NewMoonshot is a client for Moonshot AI's Kimi models.
func NewMoonshot(apiKey string, thinking bool) *Client {
	return newMoonshotWithOptions(apiKey, thinking)
}

// newMoonshotWithOptions is NewMoonshot with extra SDK options, which tests
// use to point the client at a stub server.
func newMoonshotWithOptions(apiKey string, thinking bool, opts ...option.RequestOption) *Client {
	opts = append([]option.RequestOption{
		option.WithBaseURL(moonshotBaseURL),
		option.WithAuthToken(apiKey),
	}, opts...)
	client := newClient(thinking, opts)
	client.moonshot = true
	return client
}

// toMoonshotEffort clamps to the levels Kimi accepts: low, high and max. A
// level between two of them goes up, since Kimi's own default is max.
func toMoonshotEffort(e agent.Effort) sdk.OutputConfigEffort {
	switch e {
	case agent.EffortLow:
		return sdk.OutputConfigEffortLow
	case agent.EffortMedium, agent.EffortHigh:
		return sdk.OutputConfigEffortHigh
	default:
		return sdk.OutputConfigEffortMax
	}
}
