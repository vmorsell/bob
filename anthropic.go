package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const bobSystemPrompt = `You are Bob, a helpful assistant for a software team. You communicate via Slack.
Keep responses concise and practical — this is a chat interface, not a document.
Use Slack-compatible markdown when formatting is helpful.

You have access to the team's GitHub organization. You can search for repositories, clone them,
implement code changes, and create pull requests.

Available tools and typical workflow:
1. list_repos — Search for repositories in the org.
2. clone_repo — Clone a repository to your workspace.
3. implement_changes — Use Claude Code CLI to implement code changes in a cloned repo. The repo must be cloned first.
4. create_pull_request — Commit, push, and open a PR from the changes made by implement_changes.

When asked to implement something, follow this flow: clone_repo → implement_changes → create_pull_request.
Always share the PR link in your response.`

const maxToolIterations = 15

type AnthropicLLM struct {
	client anthropic.Client
	tools  []anthropic.ToolUnionParam
	toolFn map[string]func(ctx context.Context, input json.RawMessage) (string, error)
}

func NewAnthropicLLM(apiKey string, tools []Tool) *AnthropicLLM {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))

	sdkTools := make([]anthropic.ToolUnionParam, len(tools))
	toolFn := make(map[string]func(ctx context.Context, input json.RawMessage) (string, error), len(tools))

	for i, t := range tools {
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: t.Schema,
		}
		sdkTools[i] = anthropic.ToolUnionParam{OfTool: &tp}
		toolFn[t.Name] = t.Execute
	}

	return &AnthropicLLM{
		client: client,
		tools:  sdkTools,
		toolFn: toolFn,
	}
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

	for range maxToolIterations {
		resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.ModelClaudeSonnet4_5,
			MaxTokens: 4096,
			System: []anthropic.TextBlockParam{
				{Text: bobSystemPrompt},
			},
			Messages: params,
			Tools:    a.tools,
		})
		if err != nil {
			return "", fmt.Errorf("anthropic: %w", err)
		}

		// If the model didn't request tool use, extract text and return.
		if resp.StopReason != anthropic.StopReasonToolUse {
			for _, block := range resp.Content {
				if block.Type == "text" {
					return block.Text, nil
				}
			}
			return "", fmt.Errorf("anthropic: empty response")
		}

		// Append the assistant's response (including tool_use blocks) to the conversation.
		params = append(params, resp.ToParam())

		// Execute each tool call and collect results.
		var toolResults []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			variant, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok {
				continue
			}

			fn, exists := a.toolFn[variant.Name]
			if !exists {
				toolResults = append(toolResults,
					anthropic.NewToolResultBlock(variant.ID, fmt.Sprintf("unknown tool: %s", variant.Name), true))
				continue
			}

			log.Printf("tool call: %s(%s)", variant.Name, string(variant.Input))

			result, err := fn(ctx, variant.Input)
			if err != nil {
				log.Printf("tool error: %s: %v", variant.Name, err)
				toolResults = append(toolResults,
					anthropic.NewToolResultBlock(variant.ID, err.Error(), true))
				continue
			}

			log.Printf("tool result: %s: %s", variant.Name, truncate(result, 200))
			toolResults = append(toolResults,
				anthropic.NewToolResultBlock(variant.ID, result, false))
		}

		params = append(params, anthropic.NewUserMessage(toolResults...))
	}

	return "", fmt.Errorf("anthropic: exceeded max tool iterations (%d)", maxToolIterations)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
