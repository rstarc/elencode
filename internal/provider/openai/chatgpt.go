package openai

import (
	"context"
	"net/http"

	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
)

// chatGPTBaseURL is the backend that serves a ChatGPT sign-in.
// It serves the Responses API, so everything but the request's edges is the
// same client: what differs is who is billed, and that is decided by the
// token rather than by anything in the body.
const chatGPTBaseURL = "https://chatgpt.com/backend-api/codex/"

// chatGPTInstructions is sent because the backend refuses a request without
// instructions, which the API treats as optional. Kept to what the agent loop
// would otherwise leave unsaid: the conversation carries the rest.
const chatGPTInstructions = "You are elencode, a coding agent running in the user's terminal. " +
	"Use the tools you are given to read, edit and run code in the working directory."

// Credentials is the ChatGPT login, asked for on every request because it is
// renewed while the session runs. *chatgpt.Source is the real one.
type Credentials interface {
	Credentials(ctx context.Context) (accessToken, accountID string, err error)
}

// NewChatGPT is a client spending a ChatGPT plan's Codex allowance instead of
// API credits.
func NewChatGPT(creds Credentials, thinking bool, effort agent.Effort) *Client {
	return newChatGPTWithOptions(creds, thinking, effort)
}

// newChatGPTWithOptions is NewChatGPT with extra SDK options, which tests use
// to point the client at a stub backend.
func newChatGPTWithOptions(creds Credentials, thinking bool, effort agent.Effort, opts ...option.RequestOption) *Client {
	opts = append([]option.RequestOption{
		option.WithBaseURL(chatGPTBaseURL),
		option.WithMiddleware(chatGPTAuth(creds)),
	}, opts...)
	return &Client{responses: responses.NewResponseService(withoutEnvironment(opts)...), thinking: thinking, effort: effort, chatGPT: true}
}

// chatGPTAuth puts the login on a request just before it is sent. A middleware
// rather than a fixed header, so a renewed token is picked up mid-session.
func chatGPTAuth(creds Credentials) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		token, account, err := creds.Credentials(req.Context())
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("ChatGPT-Account-ID", account)
		req.Header.Set("originator", chatgpt.Originator)
		return next(req)
	}
}

// usageLimitTypes are the backend's names for a plan's allowance running out.
// They arrive as a 429, but the window they wait on resets in hours, which no
// backoff of the agent's can sit out.
var usageLimitTypes = map[string]bool{
	"usage_limit_reached": true,
	"usage_not_included":  true,
}
