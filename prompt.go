package canto

import "github.com/nijaru/ion/llm"

// Prompt is typed host input for one model turn.
type Prompt = llm.Prompt

// TextPrompt creates a one-message user prompt.
func TextPrompt(text string) Prompt {
	return llm.TextPrompt(text)
}
