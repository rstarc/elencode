package gemini

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/provider/retry"
	"google.golang.org/genai"
)

// eventBuffer is the capacity of the Event channel returned by Stream. It
// decouples the speed we read from the network from the speed the consumer
// renders, without letting an unread stream grow without bound.
const eventBuffer = 64

const baseURL = "https://generativelanguage.googleapis.com/"

type Client struct {
	config genai.ClientConfig
	// thinking asks for the model's reasoning. Fixed for the life of the
	// client: it comes from the config file and nothing changes it at runtime.
	// How hard to reason is the request's, since /effort changes it.
	thinking bool
}

func New(apiKey string, thinking bool) *Client {
	return newWithBaseURL(apiKey, thinking, baseURL)
}

// newWithBaseURL is New reaching another server, which tests use to point the
// client at a stub.
//
// The SDK reads the environment for itself: a key, a base URL, and whether to
// talk to Vertex AI instead. Which key a request carries and where it goes is
// elencode's to say, so each of those is set here, which the SDK lets win.
func newWithBaseURL(apiKey string, thinking bool, url string) *Client {
	return &Client{
		config: genai.ClientConfig{
			APIKey:      apiKey,
			Backend:     genai.BackendGeminiAPI,
			HTTPOptions: genai.HTTPOptions{BaseURL: url},
		},
		thinking: thinking,
	}
}

// models is the SDK's client for one request. Built per request rather than
// in New, because building it can fail and New cannot: the failure surfaces
// where a failed request would.
func (c *Client) models(ctx context.Context) (*genai.Models, error) {
	// NewClient fills in what was left empty, so it is handed a copy
	config := c.config
	client, err := genai.NewClient(ctx, &config)
	if err != nil {
		return nil, err
	}
	return client.Models, nil
}

// CheckKey asks the API whether it accepts the client's key, by listing one
// model: a request that costs nothing. A refusal is agent.ErrKeyRejected; any
// other failure says nothing about the key.
func (c *Client) CheckKey(ctx context.Context) error {
	models, err := c.models(ctx)
	if err != nil {
		return err
	}
	_, err = models.List(ctx, &genai.ListModelsConfig{PageSize: 1})

	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	// Gemini answers a bad key with a 400, saying why in the details
	rejected := apiErr.Code == http.StatusUnauthorized || apiErr.Code == http.StatusForbidden
	for _, detail := range apiErr.Details {
		if detail["reason"] == "API_KEY_INVALID" {
			rejected = true
		}
	}
	if rejected {
		return fmt.Errorf("%w (%d %s)", agent.ErrKeyRejected, apiErr.Code, apiErr.Status)
	}
	return err
}

// generateConfig is everything about the request but its contents.
func (c *Client) generateConfig(req agent.Request) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{
		MaxOutputTokens: int32(req.MaxTokens),
		Tools:           toTools(req.Tools),
	}
	// Gated twice: the config's switch and what the model itself accepts. With
	// thinking off the model still reasons at its own default level, since it
	// cannot be told not to; none of it is shown.
	if c.thinking && req.Model.Thinking == agent.ThinkingEffort {
		config.ThinkingConfig = &genai.ThinkingConfig{IncludeThoughts: true, ThinkingLevel: toThinkingLevel(req.Effort)}
	}
	return config
}

// toThinkingLevel clamps to the levels the API accepts: it has no xhigh or max,
// so those become high. An unset effort is sent as nothing, leaving the API to
// use its own default.
func toThinkingLevel(e agent.Effort) genai.ThinkingLevel {
	switch e {
	case agent.EffortNone:
		return ""
	case agent.EffortLow:
		return genai.ThinkingLevelLow
	case agent.EffortMedium:
		return genai.ThinkingLevelMedium
	default:
		return genai.ThinkingLevelHigh
	}
}

func toTools(tools []agent.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	declarations := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		declarations = append(declarations, &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			ParametersJsonSchema: map[string]any{
				"type":       tool.InputSchema.Type,
				"properties": tool.InputSchema.Properties,
				"required":   tool.InputSchema.Required,
			},
		})
	}
	return []*genai.Tool{{FunctionDeclarations: declarations}}
}

// Stream sends every event through a select on ctx.Done. Buffering delays a
// blocked send but does not prevent one, so a send that is not cancellable
// leaks this goroutine whenever the consumer abandons the turn.
func (c *Client) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, eventBuffer)

	go func() {
		defer close(events)

		// Built before the request so a conversion failure surfaces as an
		// ErrorEvent instead of being discovered halfway through a stream.
		contents, err := toContents(req.Messages)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}
		models, err := c.models(ctx)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		// Parts arrive in the order the model produced them, a few at a time.
		// Text and thoughts are split across chunks and are joined back up
		// into the block they continue; a function call arrives whole.
		var blocks []agent.Block
		var finishReason genai.FinishReason
		for chunk, err := range models.GenerateContentStream(ctx, req.Model.ID, contents, c.generateConfig(req)) {
			if err != nil {
				emit(ctx, events, agent.ErrorEvent{Err: classify(err)})
				return
			}
			if len(chunk.Candidates) == 0 {
				if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
					emit(ctx, events, agent.ErrorEvent{Err: fmt.Errorf("gemini blocked the prompt (%s)", chunk.PromptFeedback.BlockReason)})
					return
				}
				continue
			}

			candidate := chunk.Candidates[0]
			if candidate.FinishReason != "" {
				finishReason = candidate.FinishReason
			}
			if candidate.Content == nil {
				continue
			}
			for _, part := range candidate.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					input, err := json.Marshal(part.FunctionCall.Args)
					if err != nil {
						emit(ctx, events, agent.ErrorEvent{Err: err})
						return
					}
					// A tool result finds its call by id, and Gemini may send none
					id := part.FunctionCall.ID
					if id == "" {
						id = "call_" + rand.Text()
					}
					blocks = append(blocks, agent.ToolUseBlock{
						ID:        id,
						Name:      part.FunctionCall.Name,
						Input:     input,
						Signature: base64.StdEncoding.EncodeToString(part.ThoughtSignature),
					})
				case part.Thought && part.Text != "":
					if !emit(ctx, events, agent.ThinkingDeltaEvent{Text: part.Text}) {
						return
					}
					if last, ok := lastBlock(blocks).(agent.ThinkingBlock); ok {
						last.Thinking += part.Text
						blocks[len(blocks)-1] = last
					} else {
						blocks = append(blocks, agent.ThinkingBlock{Thinking: part.Text})
					}
				case !part.Thought && part.Text != "":
					if !emit(ctx, events, agent.TextDeltaEvent{Text: part.Text}) {
						return
					}
					if last, ok := lastBlock(blocks).(agent.TextBlock); ok {
						last.Text += part.Text
						blocks[len(blocks)-1] = last
					} else {
						blocks = append(blocks, agent.TextBlock{Text: part.Text})
					}
				}
			}
		}

		emit(ctx, events, agent.ResponseEvent{Response: agent.Response{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: blocks},
			StopReason: stopReason(finishReason, blocks),
		}})
	}()

	return events
}

// lastBlock is the block a part may continue, or nil when there is none yet.
func lastBlock(blocks []agent.Block) agent.Block {
	if len(blocks) == 0 {
		return nil
	}
	return blocks[len(blocks)-1]
}

// stopReason derives why the turn ended. Gemini finishes a turn that calls a
// function with STOP, like any other, so the calls are what say it.
func stopReason(finishReason genai.FinishReason, blocks []agent.Block) agent.StopReason {
	switch finishReason {
	case genai.FinishReasonMaxTokens:
		return agent.StopReasonMaxTokens
	case genai.FinishReasonSafety, genai.FinishReasonProhibitedContent, genai.FinishReasonBlocklist,
		genai.FinishReasonSPII, genai.FinishReasonRecitation:
		return agent.StopReasonRefusal
	}
	for _, block := range blocks {
		if _, ok := block.(agent.ToolUseBlock); ok {
			return agent.StopReasonToolUse
		}
	}
	return agent.StopReasonEndTurn
}

// classify marks err retryable when the failure was transient.
func classify(err error) error {
	// Cancellation reaches here as a transport failure, and would otherwise be
	// read as one worth retrying. The user asked for the turn to stop.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		// No answer at all, so the request never reached the API
		return &agent.RetryableError{Err: err}
	}
	// The SDK keeps no headers with the error, so there is no Retry-After to
	// pass on: the agent's own backoff applies
	if retry.RetryableStatus(apiErr.Code) {
		return &agent.RetryableError{Err: err}
	}
	return err
}

// emit sends ev unless the consumer has abandoned the turn. It reports whether
// the send happened, so a caller with more to produce can stop early.
func emit(ctx context.Context, events chan<- agent.Event, ev agent.Event) bool {
	select {
	case events <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func toContents(msgs []agent.Message) ([]*genai.Content, error) {
	// A function's response has to name the function, which a tool result
	// does not: it is found from the call, which always comes first.
	toolNames := map[string]string{}

	contents := make([]*genai.Content, 0, len(msgs))
	for _, msg := range msgs {
		var role string
		switch msg.Role {
		case agent.RoleUser:
			role = genai.RoleUser
		case agent.RoleAssistant:
			role = genai.RoleModel
		default:
			return nil, fmt.Errorf("cannot send a %q message to the Gemini API", msg.Role)
		}

		var parts []*genai.Part
		for _, block := range msg.Content {
			switch block := block.(type) {
			case agent.TextBlock:
				parts = append(parts, &genai.Part{Text: block.Text})
			case agent.ThinkingBlock, agent.RedactedThinkingBlock:
				// Gemini takes its reasoning back only as the signature on the
				// call it led to
			case agent.ToolUseBlock:
				var args map[string]any
				if err := json.Unmarshal(block.Input, &args); err != nil {
					return nil, fmt.Errorf("the input of tool call %s: %w", block.ID, err)
				}
				signature, err := base64.StdEncoding.DecodeString(block.Signature)
				if err != nil {
					return nil, fmt.Errorf("the signature of tool call %s: %w", block.ID, err)
				}
				toolNames[block.ID] = block.Name
				parts = append(parts, &genai.Part{
					FunctionCall:     &genai.FunctionCall{ID: block.ID, Name: block.Name, Args: args},
					ThoughtSignature: signature,
				})
			case agent.ToolResultBlock:
				name, ok := toolNames[block.ToolUseID]
				if !ok {
					return nil, fmt.Errorf("no tool call %s for its result", block.ToolUseID)
				}
				key := "output"
				if block.IsError {
					key = "error"
				}
				parts = append(parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{
					ID:       block.ToolUseID,
					Name:     name,
					Response: map[string]any{key: block.Content},
				}})
			default:
				return nil, fmt.Errorf("cannot send block of type %T to the API", block)
			}
		}

		// A message of nothing but reasoning has nothing left to send, and
		// Gemini rejects a content without parts
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, &genai.Content{Role: role, Parts: parts})
	}
	return contents, nil
}
