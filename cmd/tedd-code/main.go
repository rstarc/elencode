package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	// "github.com/anthropics/anthropic-sdk-go/option"
)

func main() {
	// TODO: Check for API Key
	client := anthropic.NewClient()
	// option.WithAPIKey("my-anthropic-api-key"), // defaults to os.LookupEnv("ANTHROPIC_API_KEY")

	// TODO: Define Tools

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

		// Evaluate response
		response, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
			MaxTokens: 1024,
			Messages:  sessionMessages,
			Model:     anthropic.ModelClaudeHaiku4_5,
		})

		if err != nil {
			log.Fatal(err)
		}

		// Add response to session
		sessionMessages = append(sessionMessages, response.ToParam())

		// Print output response text to user
		for _, block := range response.Content {
			if textBlock, ok := block.AsAny().(anthropic.TextBlock); ok {
				fmt.Println(textBlock.Text)
			}
		}

		fmt.Println()

	}
}
