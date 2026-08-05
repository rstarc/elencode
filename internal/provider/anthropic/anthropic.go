package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rstarc/elencode/internal/agent"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared"
)

// eventBuffer is the capacity of the Event channel returned by Stream. It
// decouples the speed we read from the network from the speed the consumer
// renders, without letting an unread stream grow without bound.
const eventBuffer = 64

// defaultModel is used when the config file names none.
const defaultModel = sdk.ModelClaudeHaiku4_5

type Client struct {
	client sdk.Client
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
	// Thinking stays off until Resolve says what this model accepts: asking for
	// the wrong kind is rejected outright, not ignored.
	return &Client{client: sdk.NewClient(opts...), thinking: thinking, effort: effort}
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
	params := sdk.MessageNewParams{
		MaxTokens: req.MaxTokens,
		Messages:  messages,
		Model:     sdk.Model(req.Model.ID),
		Tools:     toolParams(req.Tools),
		Thinking:  c.thinkingParam(req.Model),
	}

	// An unset effort sends no OutputConfig at all: the API defaults to high,
	// and filling in a level here would quietly reason at another one.
	if c.thinking && req.Model.Thinking == agent.ThinkingEffort && c.effort != agent.EffortNone {
		params.OutputConfig = sdk.OutputConfigParam{Effort: toAnthropicEffort(c.effort)}
	}
	return params
}

// toAnthropicEffort clamps to the levels the API accepts. A level it does not
// know falls to medium rather than erroring: config has already rejected the
// typos, so what is left is a level this build has not caught up with.
func toAnthropicEffort(e agent.Effort) sdk.OutputConfigEffort {
	switch e {
	case agent.EffortLow:
		return sdk.OutputConfigEffortLow
	case agent.EffortHigh:
		return sdk.OutputConfigEffortHigh
	case agent.EffortXHigh:
		return sdk.OutputConfigEffortXhigh
	case agent.EffortMax:
		return sdk.OutputConfigEffortMax
	default:
		return sdk.OutputConfigEffortMedium
	}
}

// thinkingParam asks for the kind of reasoning this model accepts, or for none.
func (c *Client) thinkingParam(model agent.Model) sdk.ThinkingConfigParamUnion {
	if !c.thinking {
		return sdk.ThinkingConfigParamUnion{}
	}

	switch model.Thinking {
	case agent.ThinkingEffort, agent.ThinkingAdaptive:
		// Summarized, because the reasoning is rendered: the API otherwise
		// returns thinking blocks whose text is empty, which would draw a
		// heading over nothing.
		//
		// Effort models ask for this too. OutputConfig.Effort says how hard to
		// reason, not whether the reasoning comes back; without the thinking
		// param there is nothing to render.
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
	case info.Capabilities.Effort.Supported:
		model.Thinking = agent.ThinkingEffort
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

		// noRetry, not a client-wide setting: only inference is retried by the
		// agent, so this is the one call whose retries would be doubled up.
		stream := c.client.Messages.NewStreaming(ctx, c.messageParams(req, messages), noRetry)
		// Close is the only thing that closes the response body — Next never
		// does, not even at the end of the stream — so without this every early
		// return below leaves a connection out of the pool until it times out.
		defer stream.Close()

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

		// The one place a request failure can surface: the SDK turns a mid-stream
		// error frame into the same API error a rejected request produces, so
		// both arrive here rather than needing separate handling.
		if err := stream.Err(); err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: classify(err)})
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

// noRetry hands retrying to the agent for the request it is applied to. The SDK
// retries too, but with a sleep that ignores ctx and no way to tell the UI,
// which makes ctrl+c look broken and turns each of the agent's own attempts
// into several requests.
var noRetry = option.WithMaxRetries(0)

// retryableTypes are the API's names for a failure another identical request
// could get past. Everything else — a rejected request, a bad key — would fail
// the same way again, and retrying only delays the explanation.
var retryableTypes = map[shared.ErrorType]bool{
	shared.ErrorTypeRateLimitError:  true,
	shared.ErrorTypeOverloadedError: true,
	shared.ErrorTypeAPIError:        true,
}

// classify marks err retryable when the failure was transient.
func classify(err error) error {
	// Cancellation reaches here as a transport failure, and would otherwise be
	// read as one worth retrying. The user asked for the turn to stop.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		// No response at all, so the request never reached the API. The SDK
		// treats that as a connection error and retries it.
		return &agent.RetryableError{Err: err}
	}

	if !retryableResponse(apiErr.Type(), apiErr.StatusCode, apiErr.Response) {
		return err
	}
	return &agent.RetryableError{Err: err, After: retryAfter(apiErr.Response)}
}

// retryableResponse mirrors the judgement the SDK makes when it retries for
// itself, which we switched off: the same statuses, and the same deference to
// x-should-retry, which is the API saying so outright and overrules everything
// else. Diverging would mean giving up on failures the vendor calls transient.
//
// The error type is consulted on top of that, and before the status, because an
// error the API reported mid-stream carries the 200 the stream opened with
// rather than a failure code. Judging by status alone would mark none of them,
// and an overload arriving once a turn is under way is exactly the case the
// agent cannot otherwise recover from.
func retryableResponse(errType shared.ErrorType, status int, resp *http.Response) bool {
	if resp != nil {
		switch resp.Header.Get("x-should-retry") {
		case "true":
			return true
		case "false":
			return false
		}
	}

	if retryableTypes[errType] {
		return true
	}

	return status == http.StatusRequestTimeout ||
		status == http.StatusConflict ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

// retryAfter reads how long the API asked us to wait. Zero means it said
// nothing and the agent should fall back to its own backoff.
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	// Milliseconds first: it is the more precise of the two when both are sent.
	if ms, err := strconv.Atoi(resp.Header.Get("Retry-After-Ms")); err == nil && ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 0
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
