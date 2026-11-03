// Package bedrock is agent.Provider for the models an AWS account runs on
// Amazon Bedrock, through the Converse API. Converse is one request shape for
// every model Bedrock serves; only reasoning is asked for in each vendor's own
// terms.
package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/auth/bearer"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/provider/retry"
)

// eventBuffer is the capacity of the Event channel returned by Stream. It
// decouples the speed we read from the network from the speed the consumer
// renders, without letting an unread stream grow without bound.
const eventBuffer = 64

type Client struct {
	client *bedrockruntime.Client
	// region is where requests go, and which inference profile a shipped
	// model runs through.
	region string
	// thinking asks for the model's reasoning. Fixed for the life of the
	// client, as for the other providers: it comes from the config file.
	thinking bool
}

// New is a client reaching Bedrock in region with a Bedrock API key.
func New(apiKey, region string, thinking bool) *Client {
	return newWithOptions(apiKey, region, thinking)
}

// newWithOptions is New with extra SDK options, which tests use to point the
// client at a stub server.
//
// The options are applied after the SDK's own defaults, which is what lets the
// key win: bedrockruntime.New reads AWS_BEARER_TOKEN_BEDROCK for itself, and
// which key a request carries is elencode's to say. Nothing else is read from
// the environment, since the client is not built from an aws.Config.
func newWithOptions(apiKey, region string, thinking bool, optFns ...func(*bedrockruntime.Options)) *Client {
	// Bounds the wait for a response to start, so a server that accepts the
	// connection and never answers fails eventually.
	httpClient := &http.Client{Transport: http.DefaultTransport}
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = transport.Clone()
		transport.ResponseHeaderTimeout = 10 * time.Minute
		httpClient.Transport = transport
	}

	withKey := func(o *bedrockruntime.Options) {
		o.BearerAuthTokenProvider = bearer.StaticTokenProvider{Token: bearer.Token{Value: apiKey}}
		o.AuthSchemePreference = []string{"httpBearerAuth"}
	}
	client := bedrockruntime.New(bedrockruntime.Options{
		Region:     region,
		HTTPClient: httpClient,
		// The agent retries, and tells the UI it is doing so
		Retryer: aws.NopRetryer{},
	}, append([]func(*bedrockruntime.Options){withKey}, optFns...)...)
	return &Client{client: client, region: region, thinking: thinking}
}

// Stream sends every event through a select on ctx.Done, so a consumer that
// abandons the turn does not leave this goroutine blocked on a send.
func (c *Client) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, eventBuffer)

	go func() {
		defer close(events)

		// Built before the request so a conversion failure surfaces as an
		// ErrorEvent instead of being discovered halfway through a stream.
		input, err := c.converseInput(req)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		output, err := c.client.ConverseStream(ctx, input)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: classify(err)})
			return
		}
		stream := output.GetStream()
		defer func() { _ = stream.Close() }()

		// Converse streams each content block by its index, and says nothing
		// more about it at the end: the response is what the deltas add up to.
		var parts []*part
		partAt := func(index *int32) *part {
			i := int(aws.ToInt32(index))
			for len(parts) <= i {
				parts = append(parts, &part{})
			}
			return parts[i]
		}
		var stopReason types.StopReason
		stopped := false

		for event := range stream.Events() {
			switch event := event.(type) {
			case *types.ConverseStreamOutputMemberContentBlockStart:
				if start, ok := event.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
					p := partAt(event.Value.ContentBlockIndex)
					p.kind = partToolUse
					p.toolUseID = aws.ToString(start.Value.ToolUseId)
					p.toolName = aws.ToString(start.Value.Name)
				}
			case *types.ConverseStreamOutputMemberContentBlockDelta:
				p := partAt(event.Value.ContentBlockIndex)
				live, err := p.add(event.Value.Delta)
				if err != nil {
					emit(ctx, events, agent.ErrorEvent{Err: err})
					return
				}
				if live != nil && !emit(ctx, events, live) {
					return
				}
			case *types.ConverseStreamOutputMemberMessageStop:
				stopReason = event.Value.StopReason
				stopped = true
			}
		}

		// Where an exception the API sent mid-stream surfaces, as well as a
		// connection that broke
		if err := stream.Err(); err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: classify(err)})
			return
		}
		if ctx.Err() != nil {
			emit(ctx, events, agent.ErrorEvent{Err: ctx.Err()})
			return
		}
		if !stopped {
			emit(ctx, events, agent.ErrorEvent{Err: &agent.RetryableError{Err: errors.New("bedrock: the response ended before the model had finished")}})
			return
		}

		reason, err := toStopReason(stopReason)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}
		blocks := make([]agent.Block, 0, len(parts))
		for _, p := range parts {
			if block, ok := p.block(); ok {
				blocks = append(blocks, block)
			}
		}
		emit(ctx, events, agent.ResponseEvent{Response: agent.Response{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: blocks},
			StopReason: reason,
		}})
	}()

	return events
}

type partKind int

const (
	partNone partKind = iota
	partText
	partReasoning
	partRedacted
	partToolUse
)

// part is one content block of the response as it streams in.
type part struct {
	kind      partKind
	text      strings.Builder // the text, the reasoning, or a tool's input JSON
	signature string
	redacted  []byte
	toolUseID string
	toolName  string
}

// add folds a delta into p, and returns the Event the UI paints from, if
// there is one: a signature or a tool's input says nothing to the reader.
func (p *part) add(delta types.ContentBlockDelta) (agent.Event, error) {
	switch delta := delta.(type) {
	case *types.ContentBlockDeltaMemberText:
		p.kind = partText
		p.text.WriteString(delta.Value)
		return agent.TextDeltaEvent{Text: delta.Value}, nil
	case *types.ContentBlockDeltaMemberToolUse:
		p.text.WriteString(aws.ToString(delta.Value.Input))
		return nil, nil
	case *types.ContentBlockDeltaMemberReasoningContent:
		switch reasoning := delta.Value.(type) {
		case *types.ReasoningContentBlockDeltaMemberText:
			p.kind = partReasoning
			p.text.WriteString(reasoning.Value)
			return agent.ThinkingDeltaEvent{Text: reasoning.Value}, nil
		case *types.ReasoningContentBlockDeltaMemberSignature:
			p.kind = partReasoning
			p.signature += reasoning.Value
			return nil, nil
		case *types.ReasoningContentBlockDeltaMemberRedactedContent:
			p.kind = partRedacted
			p.redacted = append(p.redacted, reasoning.Value...)
			return nil, nil
		}
	}
	// Citations, images and tool results stream only from features elencode
	// does not ask for
	return nil, fmt.Errorf("bedrock: unsupported content in the response (%T)", delta)
}

// block is the finished part, if it holds anything.
func (p *part) block() (agent.Block, bool) {
	switch p.kind {
	case partText:
		return agent.TextBlock{Text: p.text.String()}, true
	case partReasoning:
		return agent.ThinkingBlock{Thinking: p.text.String(), Signature: p.signature}, true
	case partRedacted:
		return agent.RedactedThinkingBlock{Data: base64.StdEncoding.EncodeToString(p.redacted)}, true
	case partToolUse:
		input := p.text.String()
		// A tool that takes no arguments may stream no input at all
		if input == "" {
			input = "{}"
		}
		return agent.ToolUseBlock{ID: p.toolUseID, Name: p.toolName, Input: json.RawMessage(input)}, true
	}
	return nil, false
}

// converseInput builds the request for one round of inference.
func (c *Client) converseInput(req agent.Request) (*bedrockruntime.ConverseStreamInput, error) {
	system, messages, err := toMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	input := &bedrockruntime.ConverseStreamInput{
		ModelId:  aws.String(c.modelID(req.Model)),
		System:   system,
		Messages: messages,
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens: aws.Int32(int32(min(req.MaxTokens, math.MaxInt32))),
		},
	}
	if fields := c.reasoningFields(req); fields != nil {
		input.AdditionalModelRequestFields = document.NewLazyDocument(fields)
	}
	// Converse refuses a tool configuration that has no tools in it
	if len(req.Tools) > 0 {
		input.ToolConfig = toolConfig(req.Tools)
	}
	return input, nil
}

// modelID is what Bedrock is asked to run: a shipped model with no in-region
// endpoint runs as the inference profile for this client's region, and
// anything else as it is written.
func (c *Client) modelID(model agent.Model) string {
	if !needsProfile(model.ID) {
		return model.ID
	}
	return profilePrefix(c.region) + "." + model.ID
}

// profilePrefix names the inference profile for region's geography. Profiles
// for the US and Europe are what every shipped model has; elsewhere the
// geographies and the models in them vary, so the global profile, which
// every one of them has, is the one that works.
func profilePrefix(region string) string {
	switch {
	case strings.HasPrefix(region, "us-"):
		return "us"
	case strings.HasPrefix(region, "eu-"):
		return "eu"
	default:
		return "global"
	}
}

// reasoningFields asks the model to reason in its vendor's own terms, which
// Converse passes on untouched. Nil asks for nothing: a model is never refused
// for that, and models that always reason do so anyway.
func (c *Client) reasoningFields(req agent.Request) map[string]any {
	if !c.thinking || req.Model.Thinking != agent.ThinkingEffort {
		return nil
	}

	switch {
	case strings.HasPrefix(req.Model.ID, "anthropic."):
		// Summarized, because the reasoning is rendered: Claude otherwise
		// returns reasoning whose text is empty. The effort says how hard to
		// reason, not whether the reasoning comes back.
		fields := map[string]any{"thinking": map[string]any{"type": "adaptive", "display": "summarized"}}
		// An unset effort sends none: the API's own default applies
		if req.Effort != agent.EffortNone {
			fields["output_config"] = map[string]any{"effort": string(req.Effort)}
		}
		return fields
	case strings.HasPrefix(req.Model.ID, "openai."):
		if req.Effort == agent.EffortNone {
			return nil
		}
		return map[string]any{"reasoning_effort": toGPTOSSEffort(req.Effort)}
	}
	return nil
}

// toGPTOSSEffort clamps to the levels gpt-oss accepts: low, medium and high.
func toGPTOSSEffort(e agent.Effort) string {
	switch e {
	case agent.EffortLow:
		return "low"
	case agent.EffortMedium:
		return "medium"
	default:
		return "high"
	}
}

func toolConfig(tools []agent.Tool) *types.ToolConfiguration {
	config := &types.ToolConfiguration{}
	for _, tool := range tools {
		properties := map[string]any{}
		for name, property := range tool.InputSchema.Properties {
			properties[name] = map[string]any{"type": property.Type, "description": property.Description}
		}
		required := []any{}
		for _, name := range tool.InputSchema.Required {
			required = append(required, name)
		}
		schema := map[string]any{"type": "object", "properties": properties, "required": required}

		config.Tools = append(config.Tools, &types.ToolMemberToolSpec{Value: types.ToolSpecification{
			Name:        aws.String(tool.Name),
			Description: aws.String(tool.Description),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(schema)},
		}})
	}
	return config
}

// toMessages converts the conversation. Converse keeps the system prompt out
// of the messages, so a system message is returned apart from the rest.
func toMessages(msgs []agent.Message) ([]types.SystemContentBlock, []types.Message, error) {
	var system []types.SystemContentBlock
	messages := make([]types.Message, 0, len(msgs))

	for _, msg := range msgs {
		if msg.Role == agent.RoleSystem {
			for _, block := range msg.Content {
				text, ok := block.(agent.TextBlock)
				if !ok {
					return nil, nil, fmt.Errorf("cannot send block of type %T as the system prompt", block)
				}
				system = append(system, &types.SystemContentBlockMemberText{Value: text.Text})
			}
			continue
		}

		var role types.ConversationRole
		switch msg.Role {
		case agent.RoleUser:
			role = types.ConversationRoleUser
		case agent.RoleAssistant:
			role = types.ConversationRoleAssistant
		default:
			return nil, nil, fmt.Errorf("unknown message role %q", msg.Role)
		}

		content := make([]types.ContentBlock, 0, len(msg.Content))
		for _, block := range msg.Content {
			switch block := block.(type) {
			case agent.TextBlock:
				// Converse refuses a blank text block
				if block.Text == "" {
					continue
				}
				content = append(content, &types.ContentBlockMemberText{Value: block.Text})
			case agent.ThinkingBlock:
				// Only Claude signs its reasoning, and only signed reasoning
				// can be checked. The rest is not wanted back: DeepSeek asks
				// for it to be left out.
				if block.Signature == "" {
					continue
				}
				content = append(content, &types.ContentBlockMemberReasoningContent{
					Value: &types.ReasoningContentBlockMemberReasoningText{Value: types.ReasoningTextBlock{
						Text:      aws.String(block.Thinking),
						Signature: aws.String(block.Signature),
					}},
				})
			case agent.RedactedThinkingBlock:
				data, err := base64.StdEncoding.DecodeString(block.Data)
				if err != nil {
					return nil, nil, fmt.Errorf("redacted reasoning that did not come from Bedrock: %w", err)
				}
				content = append(content, &types.ContentBlockMemberReasoningContent{
					Value: &types.ReasoningContentBlockMemberRedactedContent{Value: data},
				})
			case agent.ToolUseBlock:
				var input any
				if err := json.Unmarshal(block.Input, &input); err != nil {
					return nil, nil, fmt.Errorf("tool use %s has input that is not JSON: %w", block.ID, err)
				}
				content = append(content, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: aws.String(block.ID),
					Name:      aws.String(block.Name),
					Input:     document.NewLazyDocument(input),
				}})
			case agent.ToolResultBlock:
				status := types.ToolResultStatusSuccess
				if block.IsError {
					status = types.ToolResultStatusError
				}
				content = append(content, &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
					ToolUseId: aws.String(block.ToolUseID),
					Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: block.Content}},
					Status:    status,
				}})
			default:
				return nil, nil, fmt.Errorf("cannot send block of type %T to the API", block)
			}
		}
		messages = append(messages, types.Message{Role: role, Content: content})
	}
	return system, messages, nil
}

func toStopReason(reason types.StopReason) (agent.StopReason, error) {
	switch reason {
	case types.StopReasonEndTurn:
		return agent.StopReasonEndTurn, nil
	case types.StopReasonToolUse:
		return agent.StopReasonToolUse, nil
	case types.StopReasonMaxTokens, types.StopReasonModelContextWindowExceeded:
		return agent.StopReasonMaxTokens, nil
	case types.StopReasonStopSequence:
		return agent.StopReasonStopSequence, nil
	case types.StopReasonGuardrailIntervened, types.StopReasonContentFiltered:
		return agent.StopReasonRefusal, nil
	default:
		return "", fmt.Errorf("unknown stop reason %q in response", reason)
	}
}

// retryableCodes are Bedrock's names for a failure another identical request
// could get past. Needed beside the status: an exception sent once the stream
// is under way arrives with the 200 the stream opened with.
var retryableCodes = map[string]bool{
	"ThrottlingException":         true,
	"ServiceUnavailableException": true,
	"InternalServerException":     true,
	"ModelNotReadyException":      true,
	"ModelTimeoutException":       true,
	"ModelStreamErrorException":   true,
}

// classify says what went wrong in Bedrock's words, and marks it retryable
// when the failure was transient.
func classify(err error) error {
	// Cancellation reaches here as a transport failure, and would otherwise be
	// read as one worth retrying. The user asked for the turn to stop.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		// No answer from the API at all, so the request never reached it
		return &agent.RetryableError{Err: err}
	}
	described := &apiError{code: apiErr.ErrorCode(), message: apiErr.ErrorMessage(), err: err}

	var resp *http.Response
	status := 0
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) {
		resp = respErr.Response.Response
		status = respErr.HTTPStatusCode()
	}
	if retryableCodes[apiErr.ErrorCode()] || retry.RetryableStatus(status) {
		return &agent.RetryableError{Err: described, After: retry.After(resp)}
	}
	return described
}

// apiError is a failure Bedrock reported. The SDK's own message leads with the
// operation and a request id, which bury what Bedrock said.
type apiError struct {
	code    string
	message string
	err     error
}

func (e *apiError) Error() string { return fmt.Sprintf("bedrock: %s (%s)", e.message, e.code) }
func (e *apiError) Unwrap() error { return e.err }

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
