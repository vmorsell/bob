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
Never mention tool names (like implement_changes, run_tests, clone_repo) in messages to the user. Describe what you are doing in plain language instead.

You have access to the team's GitHub organization. You can search for repositories, clone them,
implement code changes, and create pull requests.

Available tools and typical workflow:
1. list_repos — Search for repositories in the org. Lightweight; does not start a job.
2. start_job — Start a monitoring job. Call this exactly once, after confirming the repo exists and the task is clear, before any other execution tools.
3. clone_repo — Clone a repository to your workspace.
4. implement_changes — Use Claude Code CLI to implement code changes in a cloned repo.
5. run_tests — Run a build or test command in a cloned repo to verify changes.
6. create_pull_request — Commit, push, and open a PR.

Work in two phases:

Phase 1 — Clarify (text replies + list_repos only):
Confirm the repo name and a specific task description. Use list_repos to verify the
repo exists. If it does not exist, tell the user and stop. Ask questions if anything
is unclear.

Phase 2 — Execute (once repo and task are confirmed):
start_job → clone_repo → implement_changes → create_pull_request
Call start_job exactly once at the start of Phase 2.
Always share the PR link in your response.`

const maxToolIterations = 15

type AnthropicLLM struct {
	client     anthropic.Client
	tools      []anthropic.ToolUnionParam
	toolFn     map[string]func(ctx context.Context, input json.RawMessage) (string, error)
	hub        *Hub
	onJobStart func(ctx context.Context, jobID string)
	notifier   *SlackNotifier
}

func NewAnthropicLLM(apiKey string, tools []Tool, hub *Hub, onJobStart func(context.Context, string), notifier *SlackNotifier) *AnthropicLLM {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))

	sdkTools := make([]anthropic.ToolUnionParam, 0, len(tools)+1)
	toolFn := make(map[string]func(ctx context.Context, input json.RawMessage) (string, error), len(tools))

	// start_job is handled inline in Respond(); add its definition here so the
	// model knows it exists, but do not register a toolFn for it.
	startJobTool := anthropic.ToolParam{
		Name:        "start_job",
		Description: anthropic.String("Start the monitoring job. Call this once after confirming the repo exists and the task is clear, before any other execution tools. Write a concise one-sentence task description."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "Concise one-sentence description of the work to be done.",
				},
			},
			Required: []string{"task"},
		},
	}
	sdkTools = append(sdkTools, anthropic.ToolUnionParam{OfTool: &startJobTool})

	for _, t := range tools {
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: t.Schema,
		}
		sdkTools = append(sdkTools, anthropic.ToolUnionParam{OfTool: &tp})
		toolFn[t.Name] = t.Execute
	}

	return &AnthropicLLM{
		client:     client,
		tools:      sdkTools,
		toolFn:     toolFn,
		hub:        hub,
		onJobStart: onJobStart,
		notifier:   notifier,
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

	jobID := ""
	startTime := time.Now()
	lastNotification := ""

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

		// Append the assistant's response (including tool_use blocks) to the conversation.
		params = append(params, resp.ToParam())

		// Pre-pass: handle start_job before any other tool so that subsequent
		// tools in the same response batch can emit events under the new jobID.
		for _, block := range resp.Content {
			variant, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok || variant.Name != "start_job" || jobID != "" {
				continue
			}
			var input struct {
				Task string `json:"task"`
			}
			json.Unmarshal(variant.Input, &input)
			jobID = generateJobID()
			channel, _ := ctx.Value(ctxKeyChannel).(string)
			threadTS, _ := ctx.Value(ctxKeyThreadTS).(string)
			slackThreadURL := ""
			if channel != "" && threadTS != "" {
				slackThreadURL = fmt.Sprintf("https://slack.com/archives/%s/p%s",
					channel, strings.ReplaceAll(threadTS, ".", ""))
			}
			a.hub.Emit(jobID, EventJobStarted, map[string]any{
				"task":             input.Task,
				"slack_thread_url": slackThreadURL,
				"channel":          channel,
				"thread_ts":        threadTS,
			})
			if a.onJobStart != nil {
				a.onJobStart(ctx, jobID)
			}
			// Backfill LLMCall and LLMResponse for this iteration.
			a.hub.Emit(jobID, EventLLMCall, map[string]any{"iteration": iter})
			a.hub.Emit(jobID, EventLLMResponse, map[string]any{
				"stop_reason": string(resp.StopReason),
				"summary":     summary,
			})
			break
		}

		// Stage-transition notification: post the model's reasoning to Slack when
		// entering a major execution stage.
		majorFallbacks := map[string]string{
			"implement_changes":   "Implementing changes...",
			"run_tests":           "Running tests...",
			"create_pull_request": "Creating pull request...",
		}
		if jobID != "" && a.notifier != nil {
			var reasoning string
			for _, block := range resp.Content {
				if tb, ok := block.AsAny().(anthropic.TextBlock); ok && tb.Text != "" {
					reasoning = strings.TrimSpace(tb.Text)
					break
				}
			}
			for _, block := range resp.Content {
				variant, ok := block.AsAny().(anthropic.ToolUseBlock)
				if !ok {
					continue
				}
				fallback, isMajor := majorFallbacks[variant.Name]
				if !isMajor {
					continue
				}
				msg := reasoning
				if msg == "" {
					msg = fallback
				} else {
					msg = truncate(msg, 400)
				}
				if msg == lastNotification {
					break
				}
				a.notifier.Notify(ctx, msg)
				lastNotification = msg
				break // one notification per LLM iteration
			}
		}

		// Execute each tool call with monitoring context.
		toolCtx := WithJobID(ctx, jobID)
		toolCtx = WithHub(toolCtx, a.hub)

		var toolResults []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			variant, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok {
				continue
			}

			// start_job was already handled in the pre-pass above.
			if variant.Name == "start_job" {
				toolResults = append(toolResults,
					anthropic.NewToolResultBlock(variant.ID, "Job started.", false))
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
