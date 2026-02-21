package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// OrchestratorResult is the outcome of an orchestration run.
type OrchestratorResult struct {
	Text  string // text reply for clarifying questions or errors
	IsJob bool   // true if a monitoring job was started
	PRURL string // set if a pull request was created
}

// Orchestrator drives the deterministic coding workflow.
type Orchestrator struct {
	anthropicKey    string
	githubOwner     string
	githubToken     string
	claudeCodeToken string
	hub             *Hub
	notifier        *SlackNotifier
	onJobStart      func(ctx context.Context, jobID string)
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(anthropicKey, githubOwner, githubToken, claudeCodeToken string, hub *Hub, notifier *SlackNotifier, onJobStart func(context.Context, string)) *Orchestrator {
	return &Orchestrator{
		anthropicKey:    anthropicKey,
		githubOwner:     githubOwner,
		githubToken:     githubToken,
		claudeCodeToken: claudeCodeToken,
		hub:             hub,
		notifier:        notifier,
		onJobStart:      onJobStart,
	}
}

// Orchestrate runs the full workflow: parse intent → verify repo → clone → implement → PR.
func (o *Orchestrator) Orchestrate(ctx context.Context, messages []Message) (OrchestratorResult, error) {
	// Step 1: parse intent with a single Haiku call.
	intent, err := ParseIntent(ctx, o.anthropicKey, messages)
	if err != nil {
		return OrchestratorResult{}, fmt.Errorf("parse intent: %w", err)
	}
	log.Printf("orchestrator: intent: repo=%q task=%q question=%q", intent.Repo, intent.Task, intent.Question)

	// Clarification needed.
	if intent.Question != "" {
		return OrchestratorResult{Text: intent.Question}, nil
	}

	if intent.Repo == "" || intent.Task == "" {
		return OrchestratorResult{Text: "I couldn't determine the repository or task from your message. Could you please specify which repository you'd like me to work on and what changes you'd like me to make?"}, nil
	}

	// Step 2: verify repo exists via GitHub API.
	if _, err := FindRepo(ctx, o.githubToken, o.githubOwner, intent.Repo); err != nil {
		return OrchestratorResult{Text: fmt.Sprintf("I couldn't find the repository *%s* in the GitHub organization. Please check the repository name and try again.", intent.Repo)}, nil
	}

	// Step 3: create job and notify.
	jobID := generateJobID()
	channel, _ := ctx.Value(ctxKeyChannel).(string)
	threadTS, _ := ctx.Value(ctxKeyThreadTS).(string)
	slackThreadURL := ""
	if channel != "" && threadTS != "" {
		slackThreadURL = fmt.Sprintf("https://slack.com/archives/%s/p%s",
			channel, strings.ReplaceAll(threadTS, ".", ""))
	}

	o.hub.Emit(jobID, EventJobStarted, map[string]any{
		"task":             intent.Task,
		"slack_thread_url": slackThreadURL,
		"channel":          channel,
		"thread_ts":        threadTS,
	})

	jobCtx := WithJobID(ctx, jobID)
	jobCtx = WithHub(jobCtx, o.hub)
	if o.onJobStart != nil {
		o.onJobStart(jobCtx, jobID)
	}

	// Emit the intent call's token usage and cost now that we have a jobID.
	intentCost := computeIntentCost(intent.InputTokens, intent.OutputTokens, intent.CacheReadTokens, intent.CacheWriteTokens)
	o.hub.Emit(jobID, EventLLMResponse, map[string]any{
		"stop_reason":         "end_turn",
		"summary":             "intent parsed",
		"input_tokens":        intent.InputTokens,
		"output_tokens":       intent.OutputTokens,
		"cache_read_tokens":   intent.CacheReadTokens,
		"cache_write_tokens":  intent.CacheWriteTokens,
		"cost_usd":            intentCost,
	})

	startTime := time.Now()

	// Step 4: clone repo.
	log.Printf("orchestrator: cloning %s", intent.Repo)
	o.hub.Emit(jobID, EventToolStarted, map[string]any{"tool_name": "clone_repo", "input": intent.Repo})
	cloneStart := time.Now()
	if err := CloneRepo(jobCtx, o.githubOwner, o.githubToken, intent.Repo); err != nil {
		o.hub.Emit(jobID, EventToolCompleted, map[string]any{
			"tool_name":      "clone_repo",
			"is_error":       true,
			"result_preview": err.Error(),
			"duration_ms":    time.Since(cloneStart).Milliseconds(),
		})
		o.hub.Emit(jobID, EventJobError, map[string]any{
			"error":             err.Error(),
			"total_duration_ms": time.Since(startTime).Milliseconds(),
			"total_cost_usd":    intentCost,
		})
		return OrchestratorResult{IsJob: true, Text: fmt.Sprintf("I ran into an error cloning the repository: %s", err.Error())}, nil
	}
	o.hub.Emit(jobID, EventToolCompleted, map[string]any{
		"tool_name":      "clone_repo",
		"is_error":       false,
		"result_preview": "cloned successfully",
		"duration_ms":    time.Since(cloneStart).Milliseconds(),
	})

	// Step 5: implement changes.
	log.Printf("orchestrator: implementing changes in %s", intent.Repo)
	o.hub.Emit(jobID, EventToolStarted, map[string]any{"tool_name": "implement_changes", "input": intent.Task})
	implStart := time.Now()
	state, err := ImplementChanges(jobCtx, o.claudeCodeToken, o.notifier, intent.Repo, intent.Task)
	implDurationMs := time.Since(implStart).Milliseconds()
	if err != nil {
		o.hub.Emit(jobID, EventToolCompleted, map[string]any{
			"tool_name":      "implement_changes",
			"is_error":       true,
			"result_preview": truncate(err.Error(), 300),
			"duration_ms":    implDurationMs,
		})
		o.hub.Emit(jobID, EventJobError, map[string]any{
			"error":             err.Error(),
			"total_duration_ms": time.Since(startTime).Milliseconds(),
			"total_cost_usd":    intentCost,
		})
		return OrchestratorResult{IsJob: true, Text: fmt.Sprintf("Claude Code encountered an error: %s", err.Error())}, nil
	}
	o.hub.Emit(jobID, EventToolCompleted, map[string]any{
		"tool_name":      "implement_changes",
		"is_error":       false,
		"result_preview": truncate(state.Message, 300),
		"duration_ms":    implDurationMs,
	})

	switch state.Status {
	case "needs_information":
		o.hub.Emit(jobID, EventJobCompleted, map[string]any{
			"final_response":    state.Message,
			"total_duration_ms": time.Since(startTime).Milliseconds(),
			"total_cost_usd":    intentCost,
		})
		return OrchestratorResult{IsJob: true, Text: state.Message}, nil
	case "error":
		o.hub.Emit(jobID, EventJobError, map[string]any{
			"error":             state.Message,
			"total_duration_ms": time.Since(startTime).Milliseconds(),
			"total_cost_usd":    intentCost,
		})
		return OrchestratorResult{IsJob: true, Text: fmt.Sprintf("Claude Code reported an error: %s", state.Message)}, nil
	}

	// status == "completed" — create PR.
	log.Printf("orchestrator: creating pull request for %s", intent.Repo)
	branch := taskBranchName(intent.Task)
	title := intent.Task
	if len(title) > 72 {
		title = title[:72]
	}
	o.hub.Emit(jobID, EventToolStarted, map[string]any{"tool_name": "create_pull_request", "input": intent.Repo})
	prStart := time.Now()
	prURL, err := CreatePullRequest(jobCtx, o.githubOwner, o.githubToken, intent.Repo, title, branch, state.Message)
	prDurationMs := time.Since(prStart).Milliseconds()
	if err != nil {
		o.hub.Emit(jobID, EventToolCompleted, map[string]any{
			"tool_name":      "create_pull_request",
			"is_error":       true,
			"result_preview": err.Error(),
			"duration_ms":    prDurationMs,
		})
		o.hub.Emit(jobID, EventJobError, map[string]any{
			"error":             err.Error(),
			"total_duration_ms": time.Since(startTime).Milliseconds(),
			"total_cost_usd":    intentCost,
		})
		return OrchestratorResult{IsJob: true, Text: fmt.Sprintf("Changes were implemented but I couldn't create the pull request: %s", err.Error())}, nil
	}
	o.hub.Emit(jobID, EventToolCompleted, map[string]any{
		"tool_name":      "create_pull_request",
		"is_error":       false,
		"result_preview": prURL,
		"duration_ms":    prDurationMs,
	})

	o.hub.Emit(jobID, EventJobCompleted, map[string]any{
		"final_response":    state.Message,
		"pr_url":            prURL,
		"total_duration_ms": time.Since(startTime).Milliseconds(),
		"total_cost_usd":    intentCost,
	})

	return OrchestratorResult{IsJob: true, PRURL: prURL}, nil
}

// taskBranchName generates a git-safe branch name from a task description.
func taskBranchName(task string) string {
	slug := strings.ToLower(task)
	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			b.WriteRune('-')
		}
	}
	s := b.String()
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if len(s) > 50 {
		s = s[:50]
	}
	return "bob/" + s
}
