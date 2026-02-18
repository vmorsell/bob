package main

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const bobSystemPrompt = `You are Bob, a helpful assistant for a software team. You communicate via Slack.
Keep responses concise and practical — this is a chat interface, not a document.
Use Slack-compatible markdown when formatting is helpful.`

type AnthropicLLM struct {
	client anthropic.Client
}

func NewAnthropicLLM(apiKey string) *AnthropicLLM {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicLLM{client: client}
}

func (a *AnthropicLLM) Respond(ctx context.Context, messages []Message) (string, error) {
	params := make([]anthropic.MessageParam, len(messages))
	for i, msg := range messages {
		block := anthropic.NewTextBlock(msg.Content)
		switch msg.Role {
		case RoleUser:
			params[i] = anthropic.NewUserMessage(block)
		case RoleAssistant:
			params[i] = anthropic.NewAssistantMessage(block)
		}
	}

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_5,
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: bobSystemPrompt},
		},
		Messages: params,
	})
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}

	for _, block := range resp.Content {
		if block.Text != "" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic: empty response")
}
