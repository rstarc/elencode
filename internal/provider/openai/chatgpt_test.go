package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/openai/openai-go/option"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
)

// fakeCredentials hands out a fixed login, or fails, and counts the asking.
type fakeCredentials struct {
	mu      sync.Mutex
	token   string
	account string
	err     error
	calls   int
}

func (f *fakeCredentials) Credentials(context.Context) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.token, f.account, f.err
}

// backend stands in for chatgpt.com/backend-api/codex: it records the path and
// headers of each request alongside the body the stub records.
type backend struct {
	*stub
	mu      sync.Mutex
	paths   []string
	headers []http.Header
}

func newBackend(t *testing.T, turns ...string) (*backend, string) {
	b := &backend{stub: &stub{t: t, turns: turns}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.paths = append(b.paths, r.URL.Path)
		b.headers = append(b.headers, r.Header.Clone())
		b.mu.Unlock()
		b.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return b, server.URL
}

func (b *backend) header(i int) http.Header {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.headers[i]
}

func (b *backend) path(i int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.paths[i]
}

var completedHello = sse(
	`{"type":"response.output_text.delta","delta":"Hello"}`,
	`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}]}}`,
)

func helloRequest() agent.Request {
	return agent.Request{
		Model:     agent.Model{Provider: agent.ProviderChatGPT, ID: "gpt-6-sol", Thinking: agent.ThinkingEffort},
		Effort:    agent.EffortHigh,
		MaxTokens: 100,
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}
}

func streamChatGPT(t *testing.T, creds Credentials, base string, thinking bool) []agent.Event {
	t.Helper()
	c := newChatGPTWithOptions(creds, thinking, option.WithBaseURL(base))
	return collect(t, c.Stream(context.Background(), helloRequest()))
}

func TestChatGPTStreamsAReply(t *testing.T) {
	_, base := newBackend(t, completedHello)

	events := streamChatGPT(t, &fakeCredentials{token: "access", account: "acct"}, base, false)

	last, ok := events[len(events)-1].(agent.ResponseEvent)
	if !ok {
		t.Fatalf("last event = %#v, want a ResponseEvent", events[len(events)-1])
	}
	if last.Response.StopReason != agent.StopReasonEndTurn {
		t.Errorf("stop reason = %q", last.Response.StopReason)
	}
}

// The base URL is the Codex backend's, which serves the Responses API under
// its own prefix rather than under /v1.
func TestChatGPTTalksToTheCodexBackend(t *testing.T) {
	if chatGPTBaseURL != "https://chatgpt.com/backend-api/codex/" {
		t.Errorf("chatGPTBaseURL = %q", chatGPTBaseURL)
	}

	b, base := newBackend(t, completedHello)
	streamChatGPT(t, &fakeCredentials{token: "access", account: "acct"}, base+"/backend-api/codex/", false)

	if got := b.path(0); got != "/backend-api/codex/responses" {
		t.Errorf("path = %q, want /backend-api/codex/responses", got)
	}
}

func TestChatGPTSendsTheLoginOnEveryRequest(t *testing.T) {
	b, base := newBackend(t, completedHello)

	streamChatGPT(t, &fakeCredentials{token: "the-access-token", account: "the-account"}, base, false)

	header := b.header(0)
	want := map[string]string{
		"Authorization":      "Bearer the-access-token",
		"ChatGPT-Account-ID": "the-account",
		"originator":         "codex_cli_rs",
	}
	for key, value := range want {
		if got := header.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
}

// The token is asked for per request, not once per client: it is renewed
// while the session runs.
func TestChatGPTAsksForTheLoginPerRequest(t *testing.T) {
	_, base := newBackend(t, completedHello, completedHello)
	creds := &fakeCredentials{token: "access", account: "acct"}
	c := newChatGPTWithOptions(creds, false, option.WithBaseURL(base))

	collect(t, c.Stream(context.Background(), helloRequest()))
	collect(t, c.Stream(context.Background(), helloRequest()))

	if creds.calls != 2 {
		t.Errorf("asked for credentials %d times, want 2", creds.calls)
	}
}

// The SDK reads OPENAI_API_KEY and friends from the environment by default.
// None of it belongs on a request to chatgpt.com: the key is a secret for a
// different service, and the organisation headers are not the backend's.
func TestChatGPTKeepsTheAPIEnvironmentOffTheRequest(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-must-not-leak")
	t.Setenv("OPENAI_ORG_ID", "org-must-not-leak")
	t.Setenv("OPENAI_PROJECT_ID", "proj-must-not-leak")
	b, base := newBackend(t, completedHello)

	streamChatGPT(t, &fakeCredentials{token: "access", account: "acct"}, base, false)

	header := b.header(0)
	if got := header.Get("Authorization"); got != "Bearer access" {
		t.Errorf("Authorization = %q, want the ChatGPT token", got)
	}
	for _, key := range []string{"OpenAI-Organization", "OpenAI-Project"} {
		if got := header.Get(key); got != "" {
			t.Errorf("%s = %q, want it absent", key, got)
		}
	}
}

// The backend rejects max_output_tokens, and requires instructions, which the
// API would have taken as optional.
func TestChatGPTShapesTheRequestForTheBackend(t *testing.T) {
	b, base := newBackend(t, completedHello)

	streamChatGPT(t, &fakeCredentials{token: "access", account: "acct"}, base, false)

	body := b.body(0)
	if _, ok := body["max_output_tokens"]; ok {
		t.Errorf("max_output_tokens = %v, want it absent", body["max_output_tokens"])
	}
	if instructions, _ := body["instructions"].(string); instructions == "" {
		t.Errorf("instructions = %v, want some", body["instructions"])
	}
	if body["store"] != false {
		t.Errorf("store = %v, want false", body["store"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	if body["model"] != "gpt-6-sol" {
		t.Errorf("model = %v", body["model"])
	}
}

func TestChatGPTAsksForReasoningLikeTheAPIClient(t *testing.T) {
	b, base := newBackend(t, completedHello)

	streamChatGPT(t, &fakeCredentials{token: "access", account: "acct"}, base, true)

	body := b.body(0)
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Errorf("reasoning = %v, want high effort", body["reasoning"])
	}
	include, _ := json.Marshal(body["include"])
	if string(include) != `["reasoning.encrypted_content"]` {
		t.Errorf("include = %s", include)
	}
}

// The API-key client is unchanged by any of this.
func TestAPIClientStillSendsMaxOutputTokensAndNoInstructions(t *testing.T) {
	s, url := newStub(t, completedHello)
	c := newWithOptions("key", false, option.WithBaseURL(url))

	collect(t, c.Stream(context.Background(), helloRequest()))

	body := s.body(0)
	if body["max_output_tokens"] != float64(100) {
		t.Errorf("max_output_tokens = %v, want 100", body["max_output_tokens"])
	}
	if _, ok := body["instructions"]; ok {
		t.Errorf("instructions = %v, want none", body["instructions"])
	}
}

// A login the issuer turned down fails the same way on the next attempt, so
// the turn ends with what the user has to do about it instead of retrying.
func TestChatGPTSignOutIsNotRetried(t *testing.T) {
	b, base := newBackend(t)
	signedOut := fmt.Errorf("refreshing: %w", chatgpt.ErrSignedOut)

	events := streamChatGPT(t, &fakeCredentials{err: signedOut}, base, false)

	last, ok := events[len(events)-1].(agent.ErrorEvent)
	if !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", events[len(events)-1])
	}
	if !errors.Is(last.Err, chatgpt.ErrSignedOut) {
		t.Errorf("err = %v, want the sign-out", last.Err)
	}
	var retryable *agent.RetryableError
	if errors.As(last.Err, &retryable) {
		t.Errorf("err = %v, marked retryable", last.Err)
	}
	if b.requests() != 0 {
		t.Errorf("sent %d requests without credentials", b.requests())
	}
}

// Any other failure to renew — the issuer down, the network gone — says
// nothing about the login, and the next attempt may well get through.
func TestChatGPTTransientCredentialFailureIsRetried(t *testing.T) {
	_, base := newBackend(t)

	events := streamChatGPT(t, &fakeCredentials{err: errors.New("issuer: 502 Bad Gateway")}, base, false)

	_ = retryableError(t, events)
}

// streamChatGPTStatus answers the one request with status and an error body.
func streamChatGPTStatus(t *testing.T, status int, body string) []agent.Event {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)

	return streamChatGPT(t, &fakeCredentials{token: "access", account: "acct"}, server.URL, false)
}

// Running out of the plan's allowance is a 429, but not the kind a few
// seconds of backoff gets past: the window resets in hours.
func TestChatGPTUsageLimitIsNotRetried(t *testing.T) {
	for _, kind := range []string{"usage_limit_reached", "usage_not_included"} {
		events := streamChatGPTStatus(t, http.StatusTooManyRequests,
			`{"error":{"type":"`+kind+`","message":"You have hit your usage limit."}}`)

		last, ok := events[len(events)-1].(agent.ErrorEvent)
		if !ok {
			t.Fatalf("%s: last event = %#v, want an ErrorEvent", kind, events[len(events)-1])
		}
		var retryable *agent.RetryableError
		if errors.As(last.Err, &retryable) {
			t.Errorf("%s: err = %v, marked retryable", kind, last.Err)
		}
	}
}

// An ordinary rate limit is still worth waiting out.
func TestChatGPTRateLimitIsStillRetried(t *testing.T) {
	events := streamChatGPTStatus(t, http.StatusTooManyRequests, `{"error":{"type":"rate_limit_exceeded","message":"slow down"}}`)
	_ = retryableError(t, events)
}

func TestChatGPTCatalogNamesEveryModelOnceUnderThisProvider(t *testing.T) {
	seen := map[string]bool{}
	for _, model := range ChatGPTCatalog() {
		if seen[model.ID] {
			t.Errorf("%q is in the catalog twice", model.ID)
		}
		seen[model.ID] = true
		if model.Provider != agent.ProviderChatGPT {
			t.Errorf("%q names provider %q, want chatgpt", model.ID, model.Provider)
		}
		if model.DisplayName == "" {
			t.Errorf("%q has no display name", model.ID)
		}
	}
	if len(seen) == 0 {
		t.Error("the catalog is empty")
	}
}

func TestChatGPTDefaultIsInTheCatalog(t *testing.T) {
	found, ok := agent.FindModel(ChatGPTCatalog(), ChatGPTDefault().ID)
	if !ok || found != ChatGPTDefault() {
		t.Errorf("ChatGPTDefault = %+v, not in the catalog", ChatGPTDefault())
	}
}

func TestChatGPTCatalogCannotBeCorruptedByItsCaller(t *testing.T) {
	first := ChatGPTCatalog()
	first[0] = agent.Model{ID: "scribbled-over"}

	if ChatGPTCatalog()[0].ID == "scribbled-over" {
		t.Error("a caller's write reached the catalog")
	}
}

// The backend's own instructions are required whatever the project says, so
// the system prompt comes after them rather than in their place.
func TestChatGPTSendsTheSystemPromptAfterItsOwnInstructions(t *testing.T) {
	c := newChatGPTWithOptions(&fakeCredentials{token: "access", account: "acct"}, false)
	req := agent.Request{Model: agent.Model{ID: "gpt-6-sol"}, SystemPrompt: "Run make test."}

	p := c.params(req, nil)

	want := chatGPTInstructions + "\n\nRun make test."
	if got := p.Instructions.Value; got != want {
		t.Errorf("instructions = %q, want %q", got, want)
	}
}
