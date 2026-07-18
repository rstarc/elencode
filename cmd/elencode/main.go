package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/rstarc/elencode/internal/agent"
)

const ANTHROPIC_API_KEY_ENV_VAR_NAME = "ANTHROPIC_API_KEY"

func main() {

	// Check for API Key
	if _, ok := os.LookupEnv(ANTHROPIC_API_KEY_ENV_VAR_NAME); !ok {
		fmt.Printf("API Key Environment Variable (%s) not set, exiting\n", ANTHROPIC_API_KEY_ENV_VAR_NAME)
		os.Exit(1)
	}

	ctx := context.Background()

	// Initialize Client
	client := anthropic.NewClient()

	// Define Agent

	// TODO: Use os.OpenRoot instead
	root := os.DirFS(".")
	agent := agent.NewAgent(root)
	tools := agent.AnthropicTools()

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
			response, err := client.Messages.New(ctx, anthropic.MessageNewParams{
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
					result, err := agent.UseTool(ctx, toolUseBlock.Name, toolUseBlock.Input)
					toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUseBlock.ID, result, err != nil))
				}
			}

			// Add tool result
			sessionMessages = append(sessionMessages, anthropic.NewUserMessage(toolResults...))
		}

	}
}
