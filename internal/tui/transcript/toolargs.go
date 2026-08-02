package transcript

import (
	"bytes"
	"encoding/json"
	"strings"
)

// formatToolArguments makes the first argument compact and positional, while
// naming the rest so optional arguments remain understandable in the transcript.
func formatToolArguments(input json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(input))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "(" + strings.TrimSpace(string(input)) + ")"
	}

	var arguments []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "(" + strings.TrimSpace(string(input)) + ")"
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "(" + strings.TrimSpace(string(input)) + ")"
		}
		keyName, ok := key.(string)
		if !ok {
			return "(" + strings.TrimSpace(string(input)) + ")"
		}
		formatted := formatToolArgument(value)
		if len(arguments) > 0 {
			formatted = keyName + "=" + formatted
		}
		arguments = append(arguments, formatted)
	}

	return "(" + strings.Join(arguments, ",") + ")"
}

func formatToolArgument(value json.RawMessage) string {
	var text string
	if len(value) > 0 && value[0] == '"' && json.Unmarshal(value, &text) == nil {
		return text
	}

	var compact bytes.Buffer
	if json.Compact(&compact, value) == nil {
		return compact.String()
	}
	return strings.TrimSpace(string(value))
}
