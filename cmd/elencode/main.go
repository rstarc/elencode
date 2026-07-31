package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/tools"
)

const ANTHROPIC_API_KEY_ENV_VAR_NAME = "ANTHROPIC_API_KEY"

func main() {

	// Check for API Key
	if _, ok := os.LookupEnv(ANTHROPIC_API_KEY_ENV_VAR_NAME); !ok {
		fmt.Printf("API Key Environment Variable %q not set, exiting\n", ANTHROPIC_API_KEY_ENV_VAR_NAME)
		os.Exit(1)
	}

	ctx := context.Background()

	// Initialize agent
	provider := anthropic.New()

	// TODO: Use os.OpenRoot instead
	root := os.DirFS(".")
	tools := []agent.Tool{
		tools.NewReadTool(root),
		tools.NewWriteTool(root),
		tools.NewEditTool(root),
		tools.NewBashTool(root),
	}
	agentConfig := agent.New(provider, tools)

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

		// Add user message to context
		userMessage := agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: userInput}})
		agentConfig.ContextWindow = append(agentConfig.ContextWindow, userMessage)

		// Evaluate response and resolve tool calls until response is returned
		for {
			response, err := agentConfig.ProcessTurn(ctx)

			if err != nil {
				log.Fatal(err)
			}

			// Add response to context
			agentConfig.ContextWindow = append(agentConfig.ContextWindow, response.Message)

			// Print output response text to user
			lipgloss.Println(agent.RenderMessage(response.Message))

			// Check if the output is ready for the user
			if response.StopReason != agent.StopReasonToolUse {
				// break inner loop, return to prompt
				break
			}

			// Evaluate tool use
			var toolResults []agent.Block
			for _, block := range response.Message.Content {
				if toolUseBlock, ok := block.(agent.ToolUseBlock); ok {
					result, err := agentConfig.UseTool(ctx, toolUseBlock.Name, toolUseBlock.Input)
					toolResults = append(toolResults, agent.NewToolResultBlock(toolUseBlock.ID, result, err != nil))
				}
			}

			// Add tool result
			agentConfig.ContextWindow = append(agentConfig.ContextWindow, agent.NewUserMessage(toolResults))
		}
	}
}
