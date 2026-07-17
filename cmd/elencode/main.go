package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/rstarc/elencode/internal/tools"

	"github.com/anthropics/anthropic-sdk-go"
	// "github.com/anthropics/anthropic-sdk-go/option"
)

type Tool interface {
	Name() string
	Description() string
	InputSchema() anthropic.ToolInputSchemaParam
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

const ANTHROPIC_API_KEY_ENV_VAR_NAME = "ANTHROPIC_API_KEY"

func toolParam(t Tool) *anthropic.ToolParam {
	return &anthropic.ToolParam{
		Name:        t.Name(),
		Description: anthropic.String(t.Description()),
		InputSchema: t.InputSchema(),
	}

}

func main() {

	// Check for API Key
	if _, ok := os.LookupEnv(ANTHROPIC_API_KEY_ENV_VAR_NAME); !ok {
		fmt.Printf("API Key Environment Variable (%s) not set, exiting\n", ANTHROPIC_API_KEY_ENV_VAR_NAME)
		os.Exit(1)
	}

	ctx := context.Background()

	// Initialize Client
	client := anthropic.NewClient()

	// Define Tools

	// TODO: Use os.OpenRoot instead
	root := os.DirFS(".")
	readTool := tools.NewReadTool(root)

	toolMap := map[string]Tool{readTool.Name(): &readTool}

	tools := []anthropic.ToolUnionParam{
		{
			OfTool: toolParam(&readTool),
		},
	}

	var sessionMessages []anthropic.MessageParam
	scanner := bufio.NewScanner(os.Stdin)

	// REPL
	for {
		// Read input
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}
		if userInput == "exit" || userInput == "quit" {
			fmt.Println("goodbye")
			break
		}

		fmt.Println()

		// Add user message to session
		userMessage := anthropic.NewUserMessage(anthropic.NewTextBlock(userInput))
		sessionMessages = append(sessionMessages, userMessage)

		// Evaluate response and resolve tool calls until response is returned
		for {
			response, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
				MaxTokens: 4096,
				Messages:  sessionMessages,
				Model:     anthropic.ModelClaudeHaiku4_5,
				Tools:     tools,
			})

			if err != nil {
				log.Fatal(err)
			}

			// Add response to session
			sessionMessages = append(sessionMessages, response.ToParam())

			// Check if the output is ready for the user
			if response.StopReason != anthropic.StopReasonToolUse {
				// Print output response text to user
				for _, block := range response.Content {
					if textBlock, ok := block.AsAny().(anthropic.TextBlock); ok {
						fmt.Println(textBlock.Text)
					}
				}
				fmt.Println()

				// break inner loop, return to prompt
				break
			}

			// Evaluate tool use
			var toolResults []anthropic.ContentBlockParamUnion
			for _, block := range response.Content {
				if toolUseBlock, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
					fmt.Printf("[tool: %s]\n", toolUseBlock.Name)
					result, err := toolMap[toolUseBlock.Name].Execute(ctx, toolUseBlock.Input)
					toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUseBlock.ID, result, err != nil))
				}
			}

			// Add tool result
			sessionMessages = append(sessionMessages, anthropic.NewUserMessage(toolResults...))
		}

	}
}
