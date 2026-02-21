package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const intentSystemPrompt = `You are a task parser for a software team's coding assistant. The assistant has access to a pre-configured GitHub organization — you do NOT need to ask for the org name, owner, or any credentials.

Given the Slack conversation, extract:
- repo: the repository name (just the short name, e.g. "letsmeet" — never owner/repo)
- task: a clear description of the coding work to do (implement, fix, review, refactor, etc.)
- question: a single clarifying question ONLY if you genuinely cannot identify the repo name or task at all

Respond with JSON only: {"repo":"...","task":"...","question":""}
Rules:
- If a repo name is mentioned, even informally, extract it. Do not ask to confirm it.
- If a task is implied (fix bugs, add feature, review code, etc.) describe it clearly.
- Set question only when truly stuck — never to ask about org, owner, access, or credentials.
- If question is set, leave repo and task empty.`

// Claude Haiku 4.5 pricing (USD per token).
const (
	haikuPriceInputPerToken      = 0.80 / 1_000_000
	haikuPriceOutputPerToken     = 4.00 / 1_000_000
	haikuPriceCacheReadPerToken  = 0.08 / 1_000_000
	haikuPriceCacheWritePerToken = 1.00 / 1_000_000
)

func computeIntentCost(input, output, cacheRead, cacheWrite int64) float64 {
	return float64(input)*haikuPriceInputPerToken +
		float64(output)*haikuPriceOutputPerToken +
		float64(cacheRead)*haikuPriceCacheReadPerToken +
		float64(cacheWrite)*haikuPriceCacheWritePerToken
}

// IntentResult holds the structured output of an intent parse.
type IntentResult struct {
	Repo     string `json:"repo"`
	Task     string `json:"task"`
	Question string `json:"question"`
	// Token usage for cost tracking.
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// ParseIntent calls Claude Haiku with the conversation to extract the task intent.
func ParseIntent(ctx context.Context, apiKey string, messages []Message) (IntentResult, error) {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))

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

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5_20251001,
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{
			{Text: intentSystemPrompt},
		},
		Messages: params,
	})
	if err != nil {
		return IntentResult{}, fmt.Errorf("intent: %w", err)
	}

	for _, block := range resp.Content {
		if block.Type == "text" {
			text := strings.TrimSpace(block.Text)
			// Strip markdown code block if present.
			text = strings.TrimPrefix(text, "```json")
			text = strings.TrimPrefix(text, "```")
			text = strings.TrimSuffix(text, "```")
			text = strings.TrimSpace(text)

			var result IntentResult
			if err := json.Unmarshal([]byte(text), &result); err != nil {
				return IntentResult{}, fmt.Errorf("intent: parse response %q: %w", text, err)
			}
			result.InputTokens = int64(resp.Usage.InputTokens)
			result.OutputTokens = int64(resp.Usage.OutputTokens)
			result.CacheReadTokens = int64(resp.Usage.CacheReadInputTokens)
			result.CacheWriteTokens = int64(resp.Usage.CacheCreationInputTokens)
			return result, nil
		}
	}

	return IntentResult{}, fmt.Errorf("intent: empty response")
}
