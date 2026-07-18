package agent

import "context"

type Provider interface {
	Process(ctx context.Context, req Request) (Response, error)
}

// Request represents a single Request we send to the provider's API
type Request struct {
	MaxTokens int
	Tools     []Tool
	Messages  []Message
}

// Response represents the Response we receive from a provider's API
type Response struct {
	Message    Message    // Message with Role Assistant
	StopReason StopReason // Why inference has stopped
}

type StopReason string
