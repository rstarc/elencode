package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rstarc/elencode/internal/agent"
)

// moonshotRequest streams one turn through a Moonshot client that hands its
// request to a recorder rather than the network, and returns that request
// with its body decoded.
func moonshotRequest(t *testing.T, thinking bool, effort agent.Effort, model agent.Model) (*http.Request, map[string]any) {
	t.Helper()
	rec := &recorder{}

	c := newMoonshotWithOptions("configured-key", thinking, effort, option.WithHTTPClient(rec))
	collectEvents(t, c.Stream(context.Background(), agent.Request{
		Model:     model,
		MaxTokens: 100,
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))

	req := rec.request()
	if req == nil {
		t.Fatal("no request was sent")
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("reading the request body: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("request body %s: %v", raw, err)
	}
	return req, body
}

var kimiK3 = agent.Model{Provider: agent.ProviderMoonshot, ID: "kimi-k3", Thinking: agent.ThinkingEffort}

func TestMoonshotTalksToTheAnthropicCompatibleEndpoint(t *testing.T) {
	req, _ := moonshotRequest(t, false, agent.EffortNone, kimiK3)

	if got := req.URL.String(); got != "https://api.moonshot.ai/anthropic/v1/messages" {
		t.Errorf("request went to %s, want https://api.moonshot.ai/anthropic/v1/messages", got)
	}
}

// Kimi documents the key as a bearer token, and Anthropic's own header as
// nothing at all.
func TestMoonshotSendsTheKeyAsABearerToken(t *testing.T) {
	req, _ := moonshotRequest(t, false, agent.EffortNone, kimiK3)

	if got := req.Header.Get("Authorization"); got != "Bearer configured-key" {
		t.Errorf("Authorization = %q, want the configured key as a bearer token", got)
	}
	if got := req.Header.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want none", got)
	}
}

// Kimi's Messages API documents no thinking parameter, and its models reason
// without one.
func TestMoonshotNeverSendsAThinkingParameter(t *testing.T) {
	for _, mode := range []agent.ThinkingMode{agent.ThinkingEffort, agent.ThinkingAdaptive, agent.ThinkingBudgeted} {
		model := agent.Model{Provider: agent.ProviderMoonshot, ID: "kimi-x", Thinking: mode}
		_, body := moonshotRequest(t, true, agent.EffortHigh, model)

		if thinking, ok := body["thinking"]; ok {
			t.Errorf("%s model: thinking = %v, want it left out", mode, thinking)
		}
	}
}

func TestMoonshotSendsTheClampedEffort(t *testing.T) {
	_, body := moonshotRequest(t, true, agent.EffortMedium, kimiK3)

	config, _ := body["output_config"].(map[string]any)
	if got := config["effort"]; got != "high" {
		t.Errorf("output_config.effort = %v, want high", got)
	}
}

func TestMoonshotLeavesEffortOutWhenThinkingIsOff(t *testing.T) {
	_, body := moonshotRequest(t, false, agent.EffortMedium, kimiK3)

	if config, ok := body["output_config"]; ok {
		t.Errorf("output_config = %v, want it left out", config)
	}
}

// Kimi accepts low, high and max. A level between two of them goes up to the
// next, since Kimi's own default is the highest.
func TestToMoonshotEffortClampsToKimisLevels(t *testing.T) {
	tests := map[agent.Effort]sdk.OutputConfigEffort{
		agent.EffortLow:    sdk.OutputConfigEffortLow,
		agent.EffortMedium: sdk.OutputConfigEffortHigh,
		agent.EffortHigh:   sdk.OutputConfigEffortHigh,
		agent.EffortXHigh:  sdk.OutputConfigEffortMax,
		agent.EffortMax:    sdk.OutputConfigEffortMax,
	}
	for in, want := range tests {
		if got := toMoonshotEffort(in); got != want {
			t.Errorf("toMoonshotEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

// moonshotCheckKeyAgainst runs a Moonshot client's CheckKey against a server
// that stands in for api.moonshot.ai, answering every request with status.
func moonshotCheckKeyAgainst(t *testing.T, status int) (asked, auth string, err error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, `{"object":"list","data":[]}`)
	}))
	defer server.Close()

	c := newMoonshotWithOptions("configured-key", false, agent.EffortNone, option.WithBaseURL(server.URL+"/anthropic"))
	err = c.CheckKey(context.Background())
	return asked, auth, err
}

// The Anthropic-compatible endpoint has no model listing: /anthropic/v1/models
// is a 404 for any key. The OpenAI-compatible one beside it has.
func TestMoonshotChecksTheKeyByListingModels(t *testing.T) {
	asked, auth, err := moonshotCheckKeyAgainst(t, http.StatusOK)
	if err != nil {
		t.Fatalf("CheckKey: %v", err)
	}
	if asked != "/v1/models" {
		t.Errorf("asked for %s, want /v1/models", asked)
	}
	if auth != "Bearer configured-key" {
		t.Errorf("Authorization = %q, want the configured key as a bearer token", auth)
	}
}

func TestMoonshotReportsARejectedKey(t *testing.T) {
	_, _, err := moonshotCheckKeyAgainst(t, http.StatusUnauthorized)
	if !errors.Is(err, agent.ErrKeyRejected) {
		t.Errorf("err = %v, want ErrKeyRejected", err)
	}
}
