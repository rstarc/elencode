package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	llm "github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/provider/anthropic"
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
	client := anthropic.New()

	// Define Agent

	// TODO: Use os.OpenRoot instead
	root := os.DirFS(".")
	agent := llm.NewAgent(root)

	var sessionMessages []llm.Message
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
		// userMessage := anthropic.NewUserMessage(anthropic.NewTextBlock(userInput))
		userMessage := llm.Message{
			Role:    llm.RoleUser,
			Content: []llm.Block{llm.TextBlock{Text: userInput}},
		}
		sessionMessages = append(sessionMessages, userMessage)

		// Evaluate response and resolve tool calls until response is returned
		for {
			response, err := client.Process(ctx,
				llm.Request{
					MaxTokens: 4096,
					Tools:     []llm.Tool{}, // TODO
					Messages:  sessionMessages,
				},
			)

			if err != nil {
				log.Fatal(err)
			}

			// Add response to session
			sessionMessages = append(sessionMessages, response.Message)

			// Check if the output is ready for the user
			if response.StopReason != llm.StopReasonToolUse {
				// Print output response text to user
				for _, block := range response.Message.Content {
					// TODO: Fix block type conversion
					if textBlock, ok := block.(llm.TextBlock); ok {
						fmt.Println(textBlock.Text)
					}
				}
				fmt.Println()

				// break inner loop, return to prompt
				break
			}

			// Evaluate tool use
			var toolResults []llm.Block
			for _, block := range response.Message.Content {
				if toolUseBlock, ok := block.(llm.ToolUseBlock); ok {
					result, err := agent.UseTool(ctx, toolUseBlock.Name, toolUseBlock.Input)
					toolResults = append(toolResults, llm.NewToolResultBlock(toolUseBlock.ID, result, err != nil))
				}
			}

			// Add tool result
			sessionMessages = append(sessionMessages, llm.NewUserMessage(toolResults))
		}

	}
}
