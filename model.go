package main

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// Model is the only thing the pipeline knows about an LLM: text in, text out.
// Tests substitute a fake so they never touch the network or an API key.
type Model interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// OpenAIModel calls the OpenAI Responses API via the official Go SDK
// (github.com/openai/openai-go/v3). gpt-5-nano supports both /v1/responses and
// /v1/chat/completions; OpenAI recommends Responses for the gpt-5 family.
type OpenAIModel struct {
	client openai.Client
	cfg    Config
}

func NewOpenAIModel(cfg Config) (*OpenAIModel, error) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil, errors.New("OPENAI_API_KEY is not set (see .env.example)")
	}
	client := openai.NewClient(
		option.WithAPIKey(key),
		option.WithRequestTimeout(cfg.Timeout),
	)
	return &OpenAIModel{client: client, cfg: cfg}, nil
}

func (m *OpenAIModel) Complete(ctx context.Context, system, user string) (string, error) {
	resp, err := m.client.Responses.New(ctx, responses.ResponseNewParams{
		Model:           shared.ResponsesModel(m.cfg.Model),
		Instructions:    openai.String(system),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(user)},
		MaxOutputTokens: openai.Int(m.cfg.MaxOutputTokens),
		Reasoning:       shared.ReasoningParam{Effort: shared.ReasoningEffort(m.cfg.ReasoningEffort)},
	})
	if err != nil {
		return "", err
	}
	out := resp.OutputText()
	if strings.TrimSpace(out) == "" {
		return "", errors.New("model returned an empty response (it may have spent the whole budget on reasoning tokens)")
	}
	return out, nil
}

// stripFences removes a markdown code fence if the model wrapped its answer in
// one. Both prompts say "raw file content only"; small models ignore that maybe
// one time in ten, and a stray ``` line would corrupt the file.
func stripFences(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	// Drop leading blank lines.
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		return strings.Join(lines, "\n")
	}
	lines = lines[1:]
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
