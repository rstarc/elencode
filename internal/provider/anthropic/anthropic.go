package anthropic

import (
	"context"
	"fmt"

	"github.com/rstarc/elencode/internal/agent"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// eventBuffer is the capacity of the Event channel returned by Stream. It
// decouples the speed we read from the network from the speed the consumer
// renders, without letting an unread stream grow without bound.
const eventBuffer = 64

// defaultModel is used when the config file names none.
const defaultModel = sdk.ModelClaudeHaiku4_5

type Client struct {
	client sdk.Client
	// thinking asks for the model's reasoning. Fixed for the life of the
	// client: it comes from the config file and nothing changes it at runtime.
	thinking bool
}

func New(apiKey string, thinking bool) *Client {
	return newWithOptions(apiKey, thinking)
}

// newWithOptions is New with extra SDK options, which tests use to point the
// client at a stub server.
func newWithOptions(apiKey string, thinking bool, opts ...option.RequestOption) *Client {
	opts = append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	// Thinking stays off until Resolve says what this model accepts: asking for
	// the wrong kind is rejected outright, not ignored.
	return &Client{client: sdk.NewClient(opts...), thinking: thinking}
}

// DefaultModelID is the model used when configuration names none.
func DefaultModelID() string { return string(defaultModel) }

// Resolve looks up what model accepts, so the agent can put the correct
// thinking mode into each request.
func (c *Client) Resolve(ctx context.Context, modelID string) (agent.Model, error) {
	info, err := c.client.Models.Get(ctx, modelID, sdk.ModelGetParams{})
	if err != nil {
		return agent.Model{ID: modelID}, err
	}
	return toModel(*info), nil
}

// thinkingBudget is how many tokens a model of the older kind may reason with.
// Fixed rather than configurable: the setting is a yes or no, and a budget has
// to stay under the request's own token limit to leave room for an answer.
// Models that reason adaptively need no budget — they pace themselves.
const thinkingBudget = 2048

// messageParams builds the request for one round of inference
func (c *Client) messageParams(req agent.Request, messages []sdk.MessageParam) sdk.MessageNewParams {
	return sdk.MessageNewParams{
		MaxTokens: req.MaxTokens,
		Messages:  messages,
		Model:     sdk.Model(req.Model.ID),
		Tools:     toolParams(req.Tools),
		Thinking:  c.thinkingParam(req.Model),
	}
}

// thinkingParam asks for the kind of reasoning this model accepts, or for none.
func (c *Client) thinkingParam(model agent.Model) sdk.ThinkingConfigParamUnion {
	if !c.thinking {
		return sdk.ThinkingConfigParamUnion{}
	}

	switch model.Thinking {
	case agent.ThinkingAdaptive:
		// Summarized, because the reasoning is rendered: the API otherwise
		// returns thinking blocks whose text is empty, which would draw a
		// heading over nothing.
		return sdk.ThinkingConfigParamUnion{OfAdaptive: &sdk.ThinkingConfigAdaptiveParam{
			Display: sdk.ThinkingConfigAdaptiveDisplaySummarized,
		}}
	case agent.ThinkingBudgeted:
		return sdk.ThinkingConfigParamOfEnabled(thinkingBudget)
	default:
		return sdk.ThinkingConfigParamUnion{}
	}
}

// Models lists every model the API offers, newest first, following pagination
// so a model past the first page is still selectable.
func (c *Client) Models(ctx context.Context) ([]agent.Model, error) {
	var models []agent.Model

	pager := c.client.Models.ListAutoPaging(ctx, sdk.ModelListParams{})
	for pager.Next() {
		models = append(models, toModel(pager.Current()))
	}
	if err := pager.Err(); err != nil {
		return nil, err
	}
	return models, nil
}

// toModel reads what a model is and what it accepts. The thinking kinds are
// mutually exclusive in practice — a model takes the adaptive kind or the
// budgeted one, not both — and adaptive is preferred where there is a choice,
// since the model then decides how much reasoning the turn is worth.
func toModel(info sdk.ModelInfo) agent.Model {
	model := agent.Model{ID: info.ID, DisplayName: info.DisplayName}

	switch thinking := info.Capabilities.Thinking; {
	case thinking.Types.Adaptive.Supported:
		model.Thinking = agent.ThinkingAdaptive
	case thinking.Types.Enabled.Supported:
		model.Thinking = agent.ThinkingBudgeted
	}
	return model
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
		messages, err := toMessages(req.Messages)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		stream := c.client.Messages.NewStreaming(ctx, c.messageParams(req, messages))

		// The SDK has no GetFinalMessage; Accumulate folds each event into
		// message, rebuilding what a non-streaming call would have returned.
		message := sdk.Message{}
		for stream.Next() {
			event := stream.Current()

			if err := message.Accumulate(event); err != nil {
				emit(ctx, events, agent.ErrorEvent{Err: err})
				return
			}

			// Emit only what the UI needs to paint live. Everything else
			// (tool inputs, usage, stop reason) is recovered from message.
			if delta, ok := event.AsAny().(sdk.ContentBlockDeltaEvent); ok {
				if live, ok := deltaEvent(delta); ok {
					if !emit(ctx, events, live) {
						return
					}
				}
			}
		}

		if err := stream.Err(); err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		blocks, err := toBlocks(&message)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		stopReason, err := toStopReason(message.StopReason)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		emit(ctx, events, agent.ResponseEvent{Response: agent.Response{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: blocks},
			StopReason: stopReason,
		}})
	}()

	return events
}

// deltaEvent converts a streamed delta into the Event the UI paints from, and
// reports whether there is anything to paint: a signature says nothing to the
// reader, and a tool's input is only worth showing once it has parsed.
func deltaEvent(delta sdk.ContentBlockDeltaEvent) (agent.Event, bool) {
	switch variant := delta.Delta.AsAny().(type) {
	case sdk.TextDelta:
		return agent.TextDeltaEvent{Text: variant.Text}, true
	case sdk.ThinkingDelta:
		return agent.ThinkingDeltaEvent{Text: variant.Thinking}, true
	default:
		return nil, false
	}
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

func toolParam(t agent.Tool) *sdk.ToolParam {

	toolSchema := t.InputSchema

	return &sdk.ToolParam{
		Name:        t.Name,
		Description: sdk.String(t.Description),
		InputSchema: sdk.ToolInputSchemaParam{
			Properties: toolSchema.Properties,
			Required:   toolSchema.Required,
		},
	}
}

func toolParams(t []agent.Tool) []sdk.ToolUnionParam {
	tools := []sdk.ToolUnionParam{}

	for _, tool := range t {
		t := sdk.ToolUnionParam{
			OfTool: toolParam(tool),
		}
		tools = append(tools, t)
	}
	return tools
}

func toMessages(msgs []agent.Message) ([]sdk.MessageParam, error) {
	messageParam := make([]sdk.MessageParam, 0, len(msgs))
	for _, msg := range msgs {

		blocks := make([]sdk.ContentBlockParamUnion, 0, len(msg.Content))
		for _, block := range msg.Content {
			switch block := block.(type) {
			case agent.TextBlock:
				blocks = append(blocks, sdk.NewTextBlock(block.Text))
			case agent.ThinkingBlock:
				blocks = append(blocks, sdk.NewThinkingBlock(block.Signature, block.Thinking))
			case agent.RedactedThinkingBlock:
				blocks = append(blocks, sdk.NewRedactedThinkingBlock(block.Data))
			case agent.ToolUseBlock:
				blocks = append(blocks, sdk.NewToolUseBlock(block.ID, block.Input, block.Name))
			case agent.ToolResultBlock:
				blocks = append(blocks, sdk.NewToolResultBlock(block.ToolUseID, block.Content, block.IsError))
			default:
				return nil, fmt.Errorf("cannot send block of type %T to the API", block)
			}
		}

		role, err := toSdkRole(msg.Role)
		if err != nil {
			return nil, err
		}
		messageParam = append(messageParam, sdk.MessageParam{Role: role, Content: blocks})

	}
	return messageParam, nil
}

// toBlocks converts an Anthropic SDK Message's Content to a slice of agent.Block
// structs. Variants we do not handle yet are an error rather than a panic: the
// API may start returning one at any time, and the caller can render an error.
func toBlocks(msg *sdk.Message) ([]agent.Block, error) {
	blocks := make([]agent.Block, 0, len(msg.Content))

	for _, block := range msg.Content {
		switch variant := block.AsAny().(type) {
		case sdk.TextBlock:
			blocks = append(blocks, agent.TextBlock{Text: variant.Text})
		case sdk.ThinkingBlock:
			blocks = append(blocks, agent.ThinkingBlock{Thinking: variant.Thinking, Signature: variant.Signature})
		case sdk.RedactedThinkingBlock:
			blocks = append(blocks, agent.RedactedThinkingBlock{Data: variant.Data})
		case sdk.ToolUseBlock:
			blocks = append(blocks, agent.ToolUseBlock{ID: variant.ID, Name: variant.Name, Input: variant.Input})
		// case sdk.ServerToolUseBlock:
		// case sdk.WebSearchToolResultBlock:
		// case sdk.WebFetchToolResultBlock:
		// case sdk.CodeExecutionToolResultBlock:
		// case sdk.BashCodeExecutionToolResultBlock:
		// case sdk.TextEditorCodeExecutionToolResultBlock:
		// case sdk.ToolSearchToolResultBlock:
		// case sdk.ContainerUploadBlock:
		default:
			// Type, not %T: an unrecognised type string makes AsAny return nil,
			// which would otherwise report a useless "<nil>".
			return nil, fmt.Errorf("unsupported content block type %q in response", block.Type)
		}
	}

	return blocks, nil
}

func toStopReason(sdkReason sdk.StopReason) (agent.StopReason, error) {
	switch sdkReason {
	case sdk.StopReasonEndTurn:
		return agent.StopReasonEndTurn, nil
	case sdk.StopReasonMaxTokens:
		return agent.StopReasonMaxTokens, nil
	case sdk.StopReasonStopSequence:
		return agent.StopReasonStopSequence, nil
	case sdk.StopReasonToolUse:
		return agent.StopReasonToolUse, nil
	case sdk.StopReasonPauseTurn:
		return agent.StopReasonPauseTurn, nil
	case sdk.StopReasonRefusal:
		return agent.StopReasonRefusal, nil
	default:
		return "", fmt.Errorf("unknown stop reason %q in response", sdkReason)
	}
}

func toSdkRole(agentRole agent.Role) (sdk.MessageParamRole, error) {
	switch agentRole {
	case agent.RoleAssistant:
		return sdk.MessageParamRoleAssistant, nil
	case agent.RoleUser:
		return sdk.MessageParamRoleUser, nil
	case agent.RoleSystem:
		return sdk.MessageParamRoleSystem, nil
	default:
		return "", fmt.Errorf("unknown message role %q", agentRole)
	}
}
