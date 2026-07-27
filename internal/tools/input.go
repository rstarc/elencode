package tools

// Property is the typified struct of the JSON Schema for the Tool Schema's Property type
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// InputSchema is the struct matching the JSON Schema for the Tool Input
type InputSchema struct {
	Type       string `json:"type" default:"object"`
	Properties map[string]Property
	Required   []string // Names of required properties TODO: Track in Property struct instead?
}
