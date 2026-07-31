package anthropic

import (
	"context"
	"log"

	"github.com/rstarc/elencode/internal/agent"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// type Provider interface {
// 	Process(ctx context.Context, req Request) (Response, error)
// }

type Client struct {
	client sdk.Client
	model  sdk.Model
}

func New(apiKey string) *Client {
	// TODO: model, client with functional options
	return &Client{client: sdk.NewClient(option.WithAPIKey(apiKey)), model: sdk.ModelClaudeHaiku4_5}
}

func (c *Client) Process(ctx context.Context, req agent.Request) (agent.Response, error) {
	tools := toolParams(req.Tools)

	message, err := c.client.Messages.New(ctx, sdk.MessageNewParams{
		MaxTokens: int64(req.MaxTokens),
		Messages:  toMessages(req.Messages),
		Model:     c.model,
		Tools:     tools,
	})

	if err != nil {
		return agent.Response{}, err
	}

	return agent.Response{
		Message:    agent.Message{Role: agent.RoleAssistant, Content: toBlocks(message)},
		StopReason: toStopReason(message.StopReason),
	}, nil

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

func toMessages(msgs []agent.Message) []sdk.MessageParam {
	// TODO
	messageParam := make([]sdk.MessageParam, 0, len(msgs))
	for _, msg := range msgs {

		blocks := make([]sdk.ContentBlockParamUnion, 0, len(msg.Content))
		for _, block := range msg.Content {
			switch block := block.(type) {
			case agent.TextBlock:
				blocks = append(blocks, sdk.NewTextBlock(block.Text))
			case agent.ToolUseBlock:
				blocks = append(blocks, sdk.NewToolUseBlock(block.ID, block.Input, block.Name))
			case agent.ToolResultBlock:
				blocks = append(blocks, sdk.NewToolResultBlock(block.ToolUseID, block.Content, block.IsError))
			default:
				log.Panicf("Unknown block variant %T present", block)
			}
		}

		messageParam = append(messageParam, sdk.MessageParam{Role: toSdkRole(msg.Role), Content: blocks})

	}
	return messageParam
}

// toBlocks converts an Anthropic SDK Message's Content to a slice of agent.Block structs
func toBlocks(msg *sdk.Message) []agent.Block {
	blocks := make([]agent.Block, 0, len(msg.Content))

	for _, block := range msg.Content {
		switch variant := block.AsAny().(type) {
		case sdk.TextBlock:
			blocks = append(blocks, agent.TextBlock{Text: variant.Text})
		// case sdk.ThinkingBlock:
		// case sdk.RedactedThinkingBlock:
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
			log.Panicf("Unknown block variant %T present", variant)
		}
	}

	return blocks
}

func toStopReason(sdkReason sdk.StopReason) agent.StopReason {
	var reason agent.StopReason
	switch sdkReason {
	case sdk.StopReasonEndTurn:
		reason = agent.StopReasonEndTurn
	case sdk.StopReasonMaxTokens:
		reason = agent.StopReasonMaxTokens
	case sdk.StopReasonStopSequence:
		reason = agent.StopReasonStopSequence
	case sdk.StopReasonToolUse:
		reason = agent.StopReasonToolUse
	case sdk.StopReasonPauseTurn:
		reason = agent.StopReasonPauseTurn
	case sdk.StopReasonRefusal:
		reason = agent.StopReasonRefusal
	default:
		log.Panicf("Received unknown stop reason %q from Anthropic SDK!", sdkReason)
	}
	return reason
}

func toSdkRole(agentRole agent.Role) sdk.MessageParamRole {
	var role sdk.MessageParamRole
	switch agentRole {
	case agent.RoleAssistant:
		role = sdk.MessageParamRoleAssistant
	case agent.RoleUser:
		role = sdk.MessageParamRoleUser
	case agent.RoleSystem:
		role = sdk.MessageParamRoleSystem
	default:
		log.Panicf("Received unknown role %q", role)
	}
	return role
}
