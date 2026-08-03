package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/rstarc/elencode/internal/agent"
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
// so those become high rather than letting the request be rejected. The zero
// value falls to the API's own default, medium.
func toOpenAIEffort(e agent.Effort) shared.ReasoningEffort {
	switch e {
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
				"type":       "object",
				"properties": tool.InputSchema.Properties,
				"required":   tool.InputSchema.Required,
			},
		}
		out = append(out, responses.ToolUnionParam{OfFunction: &fn})
	}
	return out
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

		stream := c.client.Responses.NewStreaming(ctx, c.params(req, input))

		// Emit only what the UI needs to paint live. Everything else (tool
		// inputs, status) is recovered from the completed response, which
		// carries the full output.
		var final responses.Response
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
				final = event.Response
			}
		}

		if err := stream.Err(); err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
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
	// must not hand the agent truncated argument JSON to execute.
	if resp.Status == responses.ResponseStatusIncomplete && resp.IncompleteDetails.Reason == "max_output_tokens" {
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
