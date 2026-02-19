package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const bobSystemPrompt = `You are Bob, a helpful assistant for a software team. You communicate via Slack.
Keep responses concise and practical — this is a chat interface, not a document.
Use Slack formatting when helpful: *bold*, _italic_, inline code with backticks. Do not use markdown like **bold** — it will not render.
Do not use emojis.

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
	client     anthropic.Client
	tools      []anthropic.ToolUnionParam
	toolFn     map[string]func(ctx context.Context, input json.RawMessage) (string, error)
	hub        *Hub
	onJobStart func(ctx context.Context, jobID string)
}

func NewAnthropicLLM(apiKey string, tools []Tool, hub *Hub, onJobStart func(context.Context, string)) *AnthropicLLM {
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
		client:     client,
		tools:      sdkTools,
		toolFn:     toolFn,
		hub:        hub,
		onJobStart: onJobStart,
	}
}

func (a *AnthropicLLM) Respond(ctx context.Context, messages []Message) (string, error) {
	// Extract task text from the last user message for the monitoring job.
	taskText := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			taskText = messages[i].Content
			if len(taskText) > 200 {
				taskText = taskText[:200]
			}
			break
		}
	}

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

	jobID := ""
	startTime := time.Now()

	for iter := range maxToolIterations {
		// Emit LLMCall before each API call (only after job is created).
		if jobID != "" {
			a.hub.Emit(jobID, EventLLMCall, map[string]any{"iteration": iter})
		}

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
			a.hub.Emit(jobID, EventJobError, map[string]any{"error": err.Error()})
			return "", fmt.Errorf("anthropic: %w", err)
		}

		summary := summarizeLLMResponse(resp)

		// If not tool_use, this is the final turn — job_completed carries the
		// summary, so skip emitting a redundant llm_response for this iteration.
		if resp.StopReason != anthropic.StopReasonToolUse {
			if jobID != "" {
				a.hub.Emit(jobID, EventJobCompleted, map[string]any{
					"final_response":    summary,
					"total_duration_ms": time.Since(startTime).Milliseconds(),
				})
			}
			for _, block := range resp.Content {
				if block.Type == "text" {
					return block.Text, nil
				}
			}
			return "", fmt.Errorf("anthropic: empty response")
		}

		// First tool_use: create the monitoring job.
		if jobID == "" {
			jobID = generateJobID()
			channel, _ := ctx.Value(ctxKeyChannel).(string)
			threadTS, _ := ctx.Value(ctxKeyThreadTS).(string)
			slackThreadURL := ""
			if channel != "" && threadTS != "" {
				slackThreadURL = fmt.Sprintf("https://slack.com/archives/%s/p%s",
					channel, strings.ReplaceAll(threadTS, ".", ""))
			}
			a.hub.Emit(jobID, EventJobStarted, map[string]any{
				"task":             taskText,
				"slack_thread_url": slackThreadURL,
				"channel":          channel,
				"thread_ts":        threadTS,
			})
			if a.onJobStart != nil {
				a.onJobStart(ctx, jobID)
			}
			// Re-emit LLMCall and LLMResponse for this iteration now that we have a jobID.
			a.hub.Emit(jobID, EventLLMCall, map[string]any{"iteration": iter})
			a.hub.Emit(jobID, EventLLMResponse, map[string]any{
				"stop_reason": string(resp.StopReason),
				"summary":     summary,
			})
		}

		// Append the assistant's response (including tool_use blocks) to the conversation.
		params = append(params, resp.ToParam())

		// Execute each tool call with monitoring context.
		toolCtx := WithJobID(ctx, jobID)
		toolCtx = WithHub(toolCtx, a.hub)

		var toolResults []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			variant, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok {
				continue
			}

			fn, exists := a.toolFn[variant.Name]
			if !exists {
				a.hub.Emit(jobID, EventToolCompleted, map[string]any{
					"tool_name":      variant.Name,
					"is_error":       true,
					"result_preview": "unknown tool: " + variant.Name,
					"duration_ms":    int64(0),
				})
				toolResults = append(toolResults,
					anthropic.NewToolResultBlock(variant.ID, fmt.Sprintf("unknown tool: %s", variant.Name), true))
				continue
			}

			log.Printf("tool call: %s(%s)", variant.Name, string(variant.Input))
			a.hub.Emit(jobID, EventToolStarted, map[string]any{
				"tool_name": variant.Name,
				"input":     string(variant.Input),
			})

			toolStart := time.Now()
			result, err := fn(toolCtx, variant.Input)
			durationMs := time.Since(toolStart).Milliseconds()

			if err != nil {
				log.Printf("tool error: %s: %v", variant.Name, err)
				a.hub.Emit(jobID, EventToolCompleted, map[string]any{
					"tool_name":      variant.Name,
					"is_error":       true,
					"result_preview": truncate(err.Error(), 300),
					"duration_ms":    durationMs,
				})
				toolResults = append(toolResults,
					anthropic.NewToolResultBlock(variant.ID, err.Error(), true))
				continue
			}

			log.Printf("tool result: %s: %s", variant.Name, truncate(result, 200))
			a.hub.Emit(jobID, EventToolCompleted, map[string]any{
				"tool_name":      variant.Name,
				"is_error":       false,
				"result_preview": truncate(result, 300),
				"duration_ms":    durationMs,
			})
			toolResults = append(toolResults,
				anthropic.NewToolResultBlock(variant.ID, result, false))
		}

		params = append(params, anthropic.NewUserMessage(toolResults...))
	}

	a.hub.Emit(jobID, EventJobError, map[string]any{
		"error": fmt.Sprintf("exceeded max tool iterations (%d)", maxToolIterations),
	})
	return "", fmt.Errorf("anthropic: exceeded max tool iterations (%d)", maxToolIterations)
}

// summarizeLLMResponse returns a short text summary of a model response.
func summarizeLLMResponse(resp *anthropic.Message) string {
	var toolNames []string
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			if b.Text != "" {
				return truncate(b.Text, 100)
			}
		case anthropic.ToolUseBlock:
			toolNames = append(toolNames, b.Name)
		}
	}
	if len(toolNames) > 0 {
		return "tool:" + strings.Join(toolNames, ",")
	}
	return string(resp.StopReason)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
