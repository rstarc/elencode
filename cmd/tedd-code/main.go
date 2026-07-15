package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	// "github.com/anthropics/anthropic-sdk-go/option"
)

func pingHostTool(host pingInput) (string, error) {

	var cmd *exec.Cmd
	timeout := 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd = exec.CommandContext(ctx, "ping", host.Host)

	out, err := cmd.CombinedOutput()
	if err != nil {
		// ping returns non-zero on packet loss/unreachable — still return output
		if len(out) > 0 {
			return string(out), nil
		}
		return "", err
	}
	return string(out), nil

}

type pingInput struct {
	Host string `json:"host"`
}

func runTool(name string, input json.RawMessage) (string, error) {
	switch name {
	case "ping":
		var p pingInput
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("error parsing input: %v", err)
		}

		fmt.Printf("[%s %s]\n", name, input)
		result, err := pingHostTool(p)

		fmt.Printf("[>\n%s>]\n", result)
		return result, err
	default:
		return "unknown tool", fmt.Errorf("Unknown tool!")
	}
}

func main() {

	// Initialize Client
	// TODO: Check for API Key
	// TODO: Verify API Key
	client := anthropic.NewClient()
	// option.WithAPIKey("my-anthropic-api-key"), // defaults to os.LookupEnv("ANTHROPIC_API_KEY")

	// Define Tools
	tools := []anthropic.ToolUnionParam{
		{
			OfTool: &anthropic.ToolParam{
				Name:        "ping",
				Description: anthropic.String("Ping a host on the network"),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: map[string]any{
						"host": map[string]any{"type": "string", "description": "Hostname or IP address to ping"},
					},
					Required: []string{"host"},
				},
			},
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
				MaxTokens: 1024,
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
					result, err := runTool(toolUseBlock.Name, toolUseBlock.Input)
					toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUseBlock.ID, result, err != nil))
				}
			}

			// Add tool result
			sessionMessages = append(sessionMessages, anthropic.NewUserMessage(toolResults...))
		}

	}
}
