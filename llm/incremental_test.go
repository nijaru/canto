package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePartialJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", ``, `{}`},
		{"open brace", `{`, `{}`},
		{"key start", `{"a`, `{"a":null}`},
		{"key complete", `{"a"`, `{"a":null}`},
		{"colon", `{"a":`, `{"a":null}`},
		{"string start", `{"a":"b`, `{"a":"b"}`},
		{"string complete", `{"a":"b"`, `{"a":"b"}`},
		{"nested object", `{"a":{"b":"c`, `{"a":{"b":"c"}}`},
		{"array", `{"a":[1,2,`, `{"a":[1,2]}`},
		{"array string", `{"a":["b","c`, `{"a":["b","c"]}`},
		{"escaped quote", `{"a":"b\"`, `{"a":"b\""}`},
		{"boolean partial", `{"a":tr`, `{"a":true}`},
		{"boolean partial false", `{"a":fa`, `{"a":false}`},
		{"null partial", `{"a":nu`, `{"a":null}`},
		{"trailing comma object", `{"a":"b",`, `{"a":"b"}`},
		{"trailing comma array", `{"a":[1,`, `{"a":[1]}`},
		{"partial literal after colon", `{"a":tr`, `{"a":true}`},
		{"literal boundary only after delimiters", `{"key":nu`, `{"key":null}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParsePartialJSON(tt.input)
			assert.Equal(t, tt.expected, string(got), "structural repair failed")

			// Verify it actually unmarshals
			var m map[string]any
			err := json.Unmarshal(got, &m)
			assert.NoError(t, err, "failed to unmarshal repaired JSON: %s", got)
		})
	}
}

func TestPartialArguments(t *testing.T) {
	call := Call{
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{
			Arguments: `{"arg": "val`,
		},
	}

	args, err := call.PartialArguments()
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"arg": "val"}, args)
}
