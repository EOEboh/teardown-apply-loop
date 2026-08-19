package main

import "time"

// Config is the single place where model settings live.
// Swap the whole pipeline to another model by changing Model here (or --model).
type Config struct {
	// Model is the OpenAI model id used for BOTH the sketch call and the
	// model-based apply call. The whole point of the two-model split is that
	// this can be a small, cheap, fast model.
	Model string

	// ReasoningEffort is a gpt-5 family knob ("none", "minimal", "low",
	// "medium", "high", ...). Sketching and applying are mechanical tasks, so
	// we keep it low: we are paying for typing, not for thinking.
	ReasoningEffort string

	// MaxOutputTokens has to be big enough to hold a full file for the
	// model-based apply, plus reasoning tokens.
	MaxOutputTokens int64

	// Timeout bounds a single API call.
	Timeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		Model:           "gpt-5-nano",
		ReasoningEffort: "low",
		MaxOutputTokens: 32000,
		Timeout:         120 * time.Second,
	}
}
