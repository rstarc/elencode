package anthropic

import (
	"context"

	"github.com/rstarc/elencode/internal/agent"

	sdk "github.com/anthropics/anthropic-sdk-go"
	// "github.com/anthropics/anthropic-sdk-go/option"
)

// type Provider interface {
// 	Process(ctx context.Context, req Request) (Response, error)
// }

type Client struct {
	client sdk.Client
	model  sdk.Model
}

func New() *Client {
	// TODO: model, client
	return &Client{client: sdk.NewClient(), model: sdk.ModelClaudeHaiku4_5}
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
	return &sdk.ToolParam{
		Name:        t.Name(),
		Description: sdk.String(t.Description()),
		InputSchema: t.InputSchema(),
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

func toMessages(m []agent.Message) []sdk.MessageParam {
	// TODO
	var messages []sdk.MessageParam
	return messages
}

func toBlocks(m *sdk.Message) []agent.Block {
	// TODO
	return []agent.Block{}
}

func toStopReason(reason sdk.StopReason) agent.StopReason {
	// TODO
	return agent.StopReason(reason)
}
