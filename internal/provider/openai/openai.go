package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/provider/retry"
)

// eventBuffer is the capacity of the Event channel returned by Stream. It
// decouples the speed we read from the network from the speed the consumer
// renders, without letting an unread stream grow without bound.
const eventBuffer = 64

// defaultModel is used when the config file names none.
const defaultModel = "gpt-5"

// DefaultModelID is the model used when configuration names none.
func DefaultModelID() string { return defaultModel }

type Client struct {
	client openai.Client
	// thinking asks for the model's reasoning, and effort says how hard an
	// effort-based model should reason. Both are fixed for the life of the
	// client: they come from the config file and nothing changes them at runtime.
	thinking bool
	effort   agent.Effort
}

func New(apiKey string, thinking bool, effort agent.Effort) *Client {
	return newWithOptions(apiKey, thinking, effort)
}

// newWithOptions is New with extra SDK options, which tests use to point the
// client at a stub server.
func newWithOptions(apiKey string, thinking bool, effort agent.Effort, opts ...option.RequestOption) *Client {
	opts = append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	return &Client{client: openai.NewClient(opts...), thinking: thinking, effort: effort}
}

// params builds the request for one round of inference. Stateless by design:
// store is off and the full input is replayed every turn, so the agent keeps
// owning the context window rather than handing it to the server.
func (c *Client) params(req agent.Request, input responses.ResponseInputParam) responses.ResponseNewParams {
	p := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(req.Model.ID),
		MaxOutputTokens: openai.Int(req.MaxTokens),
		Store:           openai.Bool(false),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: input},
	}

	if len(req.Tools) > 0 {
		p.Tools = toTools(req.Tools)
	}

	// Gated twice: the config's switch and what the model itself accepts.
	// Include is what makes the reasoning re-submittable — without it the items
	// come back with no encrypted content and the next turn cannot replay them.
	if c.thinking && req.Model.Thinking == agent.ThinkingEffort {
		p.Reasoning = shared.ReasoningParam{Effort: toOpenAIEffort(c.effort), Summary: shared.ReasoningSummaryAuto}
		p.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
	}
	return p
}

// toOpenAIEffort clamps to the levels this API accepts: it has no xhigh or max,
// so those become high rather than letting the request be rejected. An unset
// effort is sent as nothing at all, leaving the API to use its own default.
func toOpenAIEffort(e agent.Effort) shared.ReasoningEffort {
	switch e {
	case agent.EffortNone:
		return ""
	case agent.EffortLow:
		return shared.ReasoningEffortLow
	case agent.EffortHigh, agent.EffortXHigh, agent.EffortMax:
		return shared.ReasoningEffortHigh
	default:
		return shared.ReasoningEffortMedium
	}
}

// toTools builds the params directly rather than with ToolParamOfFunction,
// which has no way to carry the description the model picks tools by.
func toTools(tools []agent.Tool) []responses.ToolUnionParam {
	out := make([]responses.ToolUnionParam, 0, len(tools))

	for _, tool := range tools {
		fn := responses.FunctionToolParam{
			Name:        tool.Name,
			Description: openai.String(tool.Description),
			Strict:      openai.Bool(false),
			Parameters: map[string]any{
				"type":       tool.InputSchema.Type,
				"properties": tool.InputSchema.Properties,
				"required":   tool.InputSchema.Required,
			},
		}
		out = append(out, responses.ToolUnionParam{OfFunction: &fn})
	}
	return out
}

// chatModelPrefixes are the families the picker offers. /v1/models also lists
// audio, image, embedding and moderation models, which cannot hold a
// conversation; showing them would make most of the picker unusable.
var chatModelPrefixes = []string{"gpt-5", "gpt-4.1", "gpt-4o", "o3", "o4"}

// reasoningModelPrefixes mark ids whose family reasons at an effort level.
// /v1/models returns no capabilities at all, so unlike the Anthropic provider
// this has to be a static table rather than something the API tells us.
var reasoningModelPrefixes = []string{"o1", "o3", "o4", "gpt-5"}

func hasAnyPrefix(id string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// modelFor describes a model from its id alone, which is all /v1/models gives.
func modelFor(id string) agent.Model {
	model := agent.Model{ID: id, DisplayName: id}
	if hasAnyPrefix(id, reasoningModelPrefixes) {
		model.Thinking = agent.ThinkingEffort
	}
	return model
}

// Resolve is a table lookup, not an API call: any id resolves, and one the
// table does not know simply runs without reasoning, so the config can still
// name a model this build has not heard of.
func (c *Client) Resolve(ctx context.Context, modelID string) (agent.Model, error) {
	return modelFor(modelID), nil
}

// Models lists the models that can hold a conversation, following pagination so
// a model past the first page is still selectable.
func (c *Client) Models(ctx context.Context) ([]agent.Model, error) {
	var models []agent.Model

	pager := c.client.Models.ListAutoPaging(ctx)
	for pager.Next() {
		if id := pager.Current().ID; hasAnyPrefix(id, chatModelPrefixes) {
			models = append(models, modelFor(id))
		}
	}
	if err := pager.Err(); err != nil {
		return nil, err
	}
	return models, nil
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
		input, err := toInput(req.Messages)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		// noRetry, not a client-wide setting: only inference is retried by the
		// agent, so this is the one call whose retries would be doubled up.
		stream := c.client.Responses.NewStreaming(ctx, c.params(req, input), noRetry)
		// Close is the only thing that closes the response body — Next never
		// does, not even at the end of the stream — so without this every early
		// return below leaves a connection out of the pool until it times out.
		defer stream.Close()

		// Emit only what the UI needs to paint live. Everything else (tool
		// inputs, status) is recovered from the terminal response, which
		// carries the full output.
		var final responses.Response
		var haveFinal bool
		for stream.Next() {
			switch event := stream.Current().AsAny().(type) {
			case responses.ResponseTextDeltaEvent:
				if !emit(ctx, events, agent.TextDeltaEvent{Text: event.Delta}) {
					return
				}
			case responses.ResponseReasoningSummaryTextDeltaEvent:
				if !emit(ctx, events, agent.ThinkingDeltaEvent{Text: event.Delta}) {
					return
				}
			case responses.ResponseCompletedEvent:
				final, haveFinal = event.Response, true
			case responses.ResponseIncompleteEvent:
				// A legitimate ending, not a failure: this is what hitting the
				// token limit looks like, and the partial output still counts.
				// stopReason reads it as max_tokens.
				final, haveFinal = event.Response, true
			case responses.ResponseFailedEvent:
				err := fmt.Errorf("response failed: %s (%s)", event.Response.Error.Message, event.Response.Error.Code)
				emit(ctx, events, agent.ErrorEvent{Err: classifyCode(string(event.Response.Error.Code), err)})
				return
			case responses.ResponseErrorEvent:
				err := fmt.Errorf("stream error: %s (%s)", event.Message, event.Code)
				emit(ctx, events, agent.ErrorEvent{Err: classifyCode(event.Code, err)})
				return
			}
		}

		if err := stream.Err(); err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: classify(err)})
			return
		}

		// Without a terminal event there is no output to report. Reporting an
		// empty end_turn instead would make the reply vanish with no
		// explanation, and the agent loop would treat the turn as finished.
		if !haveFinal {
			emit(ctx, events, agent.ErrorEvent{Err: errors.New("stream ended without a terminal response")})
			return
		}

		blocks, err := toBlocks(final)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		emit(ctx, events, agent.ResponseEvent{Response: agent.Response{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: blocks},
			StopReason: stopReason(final),
		}})
	}()

	return events
}

// noRetry hands retrying to the agent for the request it is applied to. The SDK
// retries too, but with a sleep that ignores ctx and no way to tell the UI,
// which makes ctrl+c look broken and turns each of the agent's own attempts
// into several requests.
var noRetry = option.WithMaxRetries(0)

// retryableCodes are the API's names for a failure that another identical
// request could get past. Everything else — a rejected prompt, a bad request —
// would fail the same way again, and retrying only delays the explanation.
var retryableCodes = map[string]bool{
	"rate_limit_exceeded": true,
	"server_error":        true,
}

// classify marks err retryable when the failure was transient, leaving the
// agent to decide what to do about it.
func classify(err error) error {
	// Cancellation reaches here as a transport failure, and would otherwise be
	// read as one worth retrying. The user asked for the turn to stop.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		// No response at all, so the request never reached the API. The SDK
		// treats that as a connection error and retries it.
		return &agent.RetryableError{Err: err}
	}
	if !retryableResponse(apiErr.StatusCode, apiErr.Response) {
		return err
	}
	return &agent.RetryableError{Err: err, After: retry.After(apiErr.Response)}
}

// retryableResponse is the shared judgement and nothing else: this API reports
// a failed request with a status, so there is no vendor-specific case to add
// on top of the header override.
func retryableResponse(status int, resp *http.Response) bool {
	if override, ok := retry.HeaderOverride(resp); ok {
		return override
	}
	return retry.RetryableStatus(status)
}

// classifyCode marks a failure reported inside the stream, where there is no
// status code to read and the API names the reason instead. These arrive over a
// 200 the SDK already accepted, so nothing below the agent can retry them.
func classifyCode(code string, err error) error {
	if !retryableCodes[code] {
		return err
	}
	return &agent.RetryableError{Err: err}
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

func toRole(role agent.Role) (responses.EasyInputMessageRole, error) {
	switch role {
	case agent.RoleUser:
		return responses.EasyInputMessageRoleUser, nil
	case agent.RoleAssistant:
		return responses.EasyInputMessageRoleAssistant, nil
	case agent.RoleSystem:
		return responses.EasyInputMessageRoleSystem, nil
	default:
		return "", fmt.Errorf("unknown message role %q", role)
	}
}

// toInput reshapes agent messages into Responses input items. This is a regroup
// rather than a 1:1 map: the Responses API has no message-with-blocks shape, so
// a single agent message can become several input items.
func toInput(msgs []agent.Message) (responses.ResponseInputParam, error) {
	var items responses.ResponseInputParam

	for _, msg := range msgs {
		role, err := toRole(msg.Role)
		if err != nil {
			return nil, err
		}

		for _, block := range msg.Content {
			switch block := block.(type) {
			case agent.TextBlock:
				items = append(items, responses.ResponseInputItemUnionParam{OfMessage: &responses.EasyInputMessageParam{
					Role:    role,
					Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(block.Text)},
				}})
			case agent.ThinkingBlock:
				// Nothing to replay: with thinking off the request asks for no
				// encrypted content, but the model still reasons and returns the
				// items. Under store:false a bare id resolves to nothing, so
				// sending one back fails the request outright.
				if block.Signature == "" {
					continue
				}
				// Assistant blocks arrive in the order the model produced them,
				// so a reasoning item lands before the function call it led to,
				// which is the order the API demands on resubmission.
				//
				// Summary is a required field: always a non-nil slice, or
				// omitzero drops it and the API rejects the item.
				summary := []responses.ResponseReasoningItemSummaryParam{}
				if block.Thinking != "" {
					summary = append(summary, responses.ResponseReasoningItemSummaryParam{Text: block.Thinking})
				}
				items = append(items, responses.ResponseInputItemUnionParam{OfReasoning: &responses.ResponseReasoningItemParam{
					ID:               block.ID,
					EncryptedContent: openai.String(block.Signature),
					Summary:          summary,
				}})
			case agent.ToolUseBlock:
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(string(block.Input), block.ID, block.Name))
			case agent.ToolResultBlock:
				// A tool result is its own input item, detached from the message
				// carrying it. function_call_output has no error flag, so a
				// failure is marked in the text: otherwise the model reads it as
				// a result.
				output := block.Content
				if block.IsError {
					output = "ERROR: " + output
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(block.ToolUseID, output))
			default:
				return nil, fmt.Errorf("cannot send block of type %T to the API", block)
			}
		}
	}

	return items, nil
}

// toBlocks converts the completed Response's output to agent blocks. Variants
// we do not handle yet are an error rather than a panic: the API may start
// returning one at any time, and the caller can render an error.
func toBlocks(resp responses.Response) ([]agent.Block, error) {
	var blocks []agent.Block

	for _, item := range resp.Output {
		switch variant := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			for _, part := range variant.Content {
				switch content := part.AsAny().(type) {
				case responses.ResponseOutputText:
					blocks = append(blocks, agent.TextBlock{Text: content.Text})
				case responses.ResponseOutputRefusal:
					// Shown as text so the transcript says why the turn ended.
					blocks = append(blocks, agent.TextBlock{Text: content.Refusal})
				default:
					// Type, not %T: an unrecognised type string makes AsAny
					// return nil, which would report a useless "<nil>".
					return nil, fmt.Errorf("unsupported output content part %q", part.Type)
				}
			}
		case responses.ResponseFunctionToolCall:
			blocks = append(blocks, agent.ToolUseBlock{ID: variant.CallID, Name: variant.Name, Input: json.RawMessage(variant.Arguments)})
		case responses.ResponseReasoningItem:
			// Both the id and the encrypted content are kept: re-sending the
			// item needs each, and the summary is all there is to render.
			blocks = append(blocks, agent.ThinkingBlock{
				Thinking:  joinSummary(variant.Summary),
				Signature: variant.EncryptedContent,
				ID:        variant.ID,
			})
		default:
			return nil, fmt.Errorf("unsupported output item type %q", item.Type)
		}
	}

	return blocks, nil
}

// stopReason derives why the turn ended: the Responses API has no single field
// for it. The order matters.
func stopReason(resp responses.Response) agent.StopReason {
	// Checked before the function-call scan: a response cut off mid tool call
	// must not hand the agent truncated argument JSON to execute. Every reason
	// ends the response early, so an unfamiliar one is read as the token limit
	// rather than falling through to the scan.
	if resp.Status == responses.ResponseStatusIncomplete {
		if resp.IncompleteDetails.Reason == "content_filter" {
			return agent.StopReasonRefusal
		}
		return agent.StopReasonMaxTokens
	}

	for _, item := range resp.Output {
		if _, ok := item.AsAny().(responses.ResponseFunctionToolCall); ok {
			return agent.StopReasonToolUse
		}
	}

	if hasRefusal(resp) {
		return agent.StopReasonRefusal
	}
	return agent.StopReasonEndTurn
}

// joinSummary flattens the reasoning summary parts into the one string a
// ThinkingBlock holds.
func joinSummary(parts []responses.ResponseReasoningItemSummary) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(part.Text)
	}
	return b.String()
}

func hasRefusal(resp responses.Response) bool {
	for _, item := range resp.Output {
		msg, ok := item.AsAny().(responses.ResponseOutputMessage)
		if !ok {
			continue
		}
		for _, part := range msg.Content {
			if _, ok := part.AsAny().(responses.ResponseOutputRefusal); ok {
				return true
			}
		}
	}
	return false
}
