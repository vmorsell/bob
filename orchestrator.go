package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// planMarker is the prefix used in formatted plan messages posted to Slack.
const planMarker = "\U0001f4cb *Plan*"

// OrchestratorResult is the outcome of an orchestration run.
type OrchestratorResult struct {
	Text           string        // text reply for clarifying questions or errors
	IsJob          bool          // true if a monitoring job was started
	PRURL          string        // set if a pull request was created
	PlanBlocks     []slack.Block // set when plan is generated (for Block Kit message)
	PlanText       string        // full plan text with marker (for MsgOptionText fallback)
	QuestionBlocks []slack.Block // set when clarification is needed (for Block Kit message)
	JobID          string        // job ID (for storing plan msg TS)
}

// Orchestrator drives the deterministic coding workflow.
type Orchestrator struct {
	anthropicKey    string
	githubOwner     string
	githubToken     string
	claudeCodeToken string
	hub             *Hub
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(anthropicKey, githubOwner, githubToken, claudeCodeToken string, hub *Hub) *Orchestrator {
	return &Orchestrator{
		anthropicKey:    anthropicKey,
		githubOwner:     githubOwner,
		githubToken:     githubToken,
		claudeCodeToken: claudeCodeToken,
		hub:             hub,
	}
}

// HandleNewRequest parses intent from a first mention and starts the planning session.
// onJobCreated is called with the job ID right after the job is created, before cloning or planning.
func (o *Orchestrator) HandleNewRequest(ctx context.Context, messages []Message, onJobCreated func(jobID string)) (OrchestratorResult, error) {
	intent, err := ParseIntent(ctx, o.anthropicKey, messages)
	if err != nil {
		return OrchestratorResult{}, fmt.Errorf("parse intent: %w", err)
	}
	log.Printf("orchestrator: intent: repo=%q task=%q question=%q", intent.Repo, intent.Task, intent.Question)

	if intent.Question != "" {
		return OrchestratorResult{Text: intent.Question}, nil
	}
	if intent.Repo == "" || intent.Task == "" {
		return OrchestratorResult{Text: "I couldn't determine the repository or task from your message. Could you please specify which repository you'd like me to work on and what changes you'd like me to make?"}, nil
	}

	// Verify repo exists via GitHub API.
	if _, err := FindRepo(ctx, o.githubToken, o.githubOwner, intent.Repo); err != nil {
		return OrchestratorResult{Text: fmt.Sprintf("I couldn't find the repository *%s* in the GitHub organization. Please check the repository name and try again.", intent.Repo)}, nil
	}

	channel, _ := ctx.Value(ctxKeyChannel).(string)
	threadTS, _ := ctx.Value(ctxKeyThreadTS).(string)

	jobID := o.createJob(intent, channel, threadTS)
	if onJobCreated != nil {
		onJobCreated(jobID)
	}
	jobCtx := WithJobID(ctx, jobID)
	jobCtx = WithHub(jobCtx, o.hub)

	// Emit intent cost.
	intentCost := computeIntentCost(intent.InputTokens, intent.OutputTokens, intent.CacheReadTokens, intent.CacheWriteTokens)
	o.hub.Emit(jobID, EventLLMResponse, map[string]any{
		"stop_reason":        "end_turn",
		"summary":            "intent parsed",
		"input_tokens":       intent.InputTokens,
		"output_tokens":      intent.OutputTokens,
		"cache_read_tokens":  intent.CacheReadTokens,
		"cache_write_tokens": intent.CacheWriteTokens,
		"cost_usd":           intentCost,
	})

	startTime := time.Now()
	repoDir := filepath.Join("/workspace", filepath.Base(intent.Repo))

	// Clone repo.
	log.Printf("orchestrator: cloning %s for planning", intent.Repo)
	o.hub.Emit(jobID, EventToolStarted, map[string]any{"tool_name": "clone_repo", "input": intent.Repo})
	cloneStart := time.Now()
	if err := CloneRepo(jobCtx, o.githubOwner, o.githubToken, intent.Repo); err != nil {
		o.hub.Emit(jobID, EventToolCompleted, map[string]any{
			"tool_name": "clone_repo", "is_error": true,
			"result_preview": err.Error(), "duration_ms": time.Since(cloneStart).Milliseconds(),
		})
		o.closeJob(ctx, jobID, EventJobError, map[string]any{
			"error": err.Error(), "total_duration_ms": time.Since(startTime).Milliseconds(), "total_cost_usd": intentCost,
		})
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: fmt.Sprintf("I ran into an error cloning the repository: %s", err.Error())}, nil
	}
	o.hub.Emit(jobID, EventToolCompleted, map[string]any{
		"tool_name": "clone_repo", "is_error": false,
		"result_preview": "cloned successfully", "duration_ms": time.Since(cloneStart).Milliseconds(),
	})

	// Reset repo to clean state.
	if err := resetRepo(jobCtx, repoDir); err != nil {
		o.closeJob(ctx, jobID, EventJobError, map[string]any{
			"error": err.Error(), "total_duration_ms": time.Since(startTime).Milliseconds(), "total_cost_usd": intentCost,
		})
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: fmt.Sprintf("Failed to reset repository: %s", err.Error())}, nil
	}

	// Run planning session.
	log.Printf("orchestrator: starting planning session for %s", intent.Repo)
	o.hub.Emit(jobID, EventToolStarted, map[string]any{"tool_name": "generate_plan", "input": intent.Task})
	planStart := time.Now()

	sr, err := RunSession(jobCtx, o.claudeCodeToken, o.hub, jobID, SessionOpts{
		RepoDir:        repoDir,
		Prompt:         fmt.Sprintf("## Task\n\n%s", intent.Task),
		SystemPrompt:   planSystemPrompt,
		PermissionMode: "plan",
	})
	planDurationMs := time.Since(planStart).Milliseconds()
	if err != nil {
		o.hub.Emit(jobID, EventToolCompleted, map[string]any{
			"tool_name": "generate_plan", "is_error": true,
			"result_preview": truncate(err.Error(), 300), "duration_ms": planDurationMs,
		})
		o.closeJob(ctx, jobID, EventJobError, map[string]any{
			"error": err.Error(), "total_duration_ms": time.Since(startTime).Milliseconds(), "total_cost_usd": intentCost,
		})
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: fmt.Sprintf("Claude Code encountered an error during planning: %s", err.Error())}, nil
	}
	o.hub.Emit(jobID, EventToolCompleted, map[string]any{
		"tool_name": "generate_plan", "is_error": false,
		"result_preview": truncate(sr.ResultText, 300), "duration_ms": planDurationMs,
	})

	return o.processSessionResult(ctx, jobID, sr, repoDir)
}

// HandleReply continues a planning session with user input (answer to question or plan feedback).
func (o *Orchestrator) HandleReply(ctx context.Context, jobID, userText string) (OrchestratorResult, error) {
	state, ok := o.hub.GetJobState(jobID)
	if !ok {
		return OrchestratorResult{}, fmt.Errorf("no state for job %s", jobID)
	}

	// If the user is giving feedback on an approved plan, transition back to planning.
	if state.Phase == PhaseAwaitingApproval {
		o.hub.SetPhase(jobID, PhasePlanning)
		o.hub.Emit(jobID, EventPlanSuperseded, nil)
	}

	repoDir := filepath.Join("/workspace", filepath.Base(state.Repo))

	jobCtx := WithJobID(ctx, jobID)
	jobCtx = WithHub(jobCtx, o.hub)

	log.Printf("orchestrator: resuming planning session %s for job %s", state.SessionID, jobID)
	o.hub.Emit(jobID, EventToolStarted, map[string]any{"tool_name": "generate_plan", "input": userText})
	planStart := time.Now()

	sr, err := RunSession(jobCtx, o.claudeCodeToken, o.hub, jobID, SessionOpts{
		RepoDir:        repoDir,
		Prompt:         userText,
		SessionID:      state.SessionID,
		PermissionMode: "plan",
		// No SystemPrompt on resume — already in session context.
	})
	planDurationMs := time.Since(planStart).Milliseconds()
	if err != nil {
		o.hub.Emit(jobID, EventToolCompleted, map[string]any{
			"tool_name": "generate_plan", "is_error": true,
			"result_preview": truncate(err.Error(), 300), "duration_ms": planDurationMs,
		})
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: fmt.Sprintf("Claude Code encountered an error: %s", err.Error())}, nil
	}
	o.hub.Emit(jobID, EventToolCompleted, map[string]any{
		"tool_name": "generate_plan", "is_error": false,
		"result_preview": truncate(sr.ResultText, 300), "duration_ms": planDurationMs,
	})

	// Update session ID if it changed.
	if sr.SessionID != "" {
		state.SessionID = sr.SessionID
	}

	return o.processSessionResult(ctx, jobID, sr, repoDir)
}

// HandleApproval runs implementation for an approved plan.
func (o *Orchestrator) HandleApproval(ctx context.Context, jobID string) (OrchestratorResult, error) {
	state, ok := o.hub.GetJobState(jobID)
	if !ok {
		return OrchestratorResult{}, fmt.Errorf("no state for job %s", jobID)
	}

	repoDir := filepath.Join("/workspace", filepath.Base(state.Repo))

	jobCtx := WithJobID(ctx, jobID)
	jobCtx = WithHub(jobCtx, o.hub)

	startTime := time.Now()

	// Reset repo to clean main before implementation.
	if err := resetRepo(jobCtx, repoDir); err != nil {
		o.hub.ClearImplementation(jobID)
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: fmt.Sprintf("Failed to reset repository: %s", err.Error())}, nil
	}

	prompt := fmt.Sprintf("## Task\n\n%s\n\n## Approved Plan\n\n%s", state.Task, state.PlanContent)

	log.Printf("orchestrator: starting implementation session for job %s", jobID)
	o.hub.Emit(jobID, EventToolStarted, map[string]any{"tool_name": "implement_changes", "input": state.Task})
	implStart := time.Now()

	sr, err := RunSession(jobCtx, o.claudeCodeToken, o.hub, jobID, SessionOpts{
		RepoDir:        repoDir,
		Prompt:         prompt,
		SystemPrompt:   executeSystemPrompt,
		PermissionMode: "acceptEdits",
		// Fresh session — no --resume.
	})
	implDurationMs := time.Since(implStart).Milliseconds()
	if err != nil {
		o.hub.Emit(jobID, EventToolCompleted, map[string]any{
			"tool_name": "implement_changes", "is_error": true,
			"result_preview": truncate(err.Error(), 300), "duration_ms": implDurationMs,
		})
		o.closeJob(ctx, jobID, EventJobError, map[string]any{
			"error": err.Error(), "total_duration_ms": time.Since(startTime).Milliseconds(),
		})
		o.hub.ClearImplementation(jobID)
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: fmt.Sprintf("Claude Code encountered an error: %s", err.Error())}, nil
	}
	o.hub.Emit(jobID, EventToolCompleted, map[string]any{
		"tool_name": "implement_changes", "is_error": false,
		"result_preview": truncate(sr.ResultText, 300), "duration_ms": implDurationMs,
	})

	if sr.IsError {
		o.closeJob(ctx, jobID, EventJobError, map[string]any{
			"error": sr.ResultText, "total_duration_ms": time.Since(startTime).Milliseconds(),
		})
		o.hub.ClearImplementation(jobID)
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: fmt.Sprintf("Claude Code reported an error: %s", sr.ResultText)}, nil
	}

	// Create PR.
	log.Printf("orchestrator: creating pull request for %s", state.Repo)
	branch := taskBranchName(state.Task)
	title := state.Task
	if len(title) > 72 {
		title = title[:72]
	}
	o.hub.Emit(jobID, EventToolStarted, map[string]any{"tool_name": "create_pull_request", "input": state.Repo})
	prStart := time.Now()
	prURL, err := CreatePullRequest(jobCtx, o.githubOwner, o.githubToken, state.Repo, title, branch, sr.ResultText)
	prDurationMs := time.Since(prStart).Milliseconds()
	if err != nil {
		o.hub.Emit(jobID, EventToolCompleted, map[string]any{
			"tool_name": "create_pull_request", "is_error": true,
			"result_preview": err.Error(), "duration_ms": prDurationMs,
		})
		o.closeJob(ctx, jobID, EventJobError, map[string]any{
			"error": err.Error(), "total_duration_ms": time.Since(startTime).Milliseconds(),
		})
		o.hub.ClearImplementation(jobID)
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: fmt.Sprintf("Changes were implemented but I couldn't create the pull request: %s", err.Error())}, nil
	}
	o.hub.Emit(jobID, EventToolCompleted, map[string]any{
		"tool_name": "create_pull_request", "is_error": false,
		"result_preview": prURL, "duration_ms": prDurationMs,
	})

	o.closeJob(ctx, jobID, EventJobCompleted, map[string]any{
		"final_response":    sr.ResultText,
		"pr_url":            prURL,
		"total_duration_ms": time.Since(startTime).Milliseconds(),
	})

	o.hub.SetPhase(jobID, PhaseDone)
	return OrchestratorResult{IsJob: true, JobID: jobID, PRURL: prURL}, nil
}

// processSessionResult inspects a planning session result and returns the appropriate
// orchestrator result, updating job state as needed.
func (o *Orchestrator) processSessionResult(ctx context.Context, jobID string, sr *SessionResult, repoDir string) (OrchestratorResult, error) {
	state, _ := o.hub.GetJobState(jobID)

	// Update session ID.
	if sr.SessionID != "" {
		state.SessionID = sr.SessionID
	}

	if sr.IsError {
		o.closeJob(ctx, jobID, EventJobError, map[string]any{"error": sr.ResultText})
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: fmt.Sprintf("Claude Code reported an error: %s", sr.ResultText)}, nil
	}

	// Clarification needed — detected via AskUserQuestion tool_use.
	if sr.Question != "" {
		o.hub.SetPhase(jobID, PhaseAwaitingQuestion)
		return OrchestratorResult{IsJob: true, JobID: jobID, Text: sr.Question, QuestionBlocks: formatQuestionBlocks(sr.Question)}, nil
	}

	// Plan completed (ExitPlanMode called).
	if sr.PlanExited {
		planContent, err := readPlanFile(sr.PlanFilePath, repoDir)
		if err != nil {
			log.Printf("orchestrator: failed to read plan file: %v, falling back to result text", err)
			planContent = sr.ResultText
		}
		if planContent == "" {
			planContent = sr.ResultText
		}

		o.hub.SetPhase(jobID, PhaseAwaitingApproval)
		state.PlanFilePath = sr.PlanFilePath
		state.PlanContent = planContent

		o.hub.Emit(jobID, EventPlanGenerated, map[string]any{"plan": planContent})

		planText := formatPlanMessage(planContent)
		blocks := formatPlanBlocks(planContent, jobID)
		return OrchestratorResult{
			IsJob:      true,
			JobID:      jobID,
			Text:       planText,
			PlanBlocks: blocks,
			PlanText:   planText,
		}, nil
	}

	// Fallback: no explicit signal — use ResultText as plan.
	if sr.ResultText != "" {
		o.hub.SetPhase(jobID, PhaseAwaitingApproval)
		state.PlanContent = sr.ResultText

		o.hub.Emit(jobID, EventPlanGenerated, map[string]any{"plan": sr.ResultText})

		planText := formatPlanMessage(sr.ResultText)
		blocks := formatPlanBlocks(sr.ResultText, jobID)
		return OrchestratorResult{
			IsJob:      true,
			JobID:      jobID,
			Text:       planText,
			PlanBlocks: blocks,
			PlanText:   planText,
		}, nil
	}

	// No useful output at all.
	o.closeJob(ctx, jobID, EventJobError, map[string]any{"error": "no output from planning session"})
	return OrchestratorResult{IsJob: true, JobID: jobID, Text: "Claude Code produced no output during planning."}, nil
}

// readPlanFile reads the plan content from a file written during planning.
func readPlanFile(planFilePath, repoDir string) (string, error) {
	if planFilePath == "" {
		return "", fmt.Errorf("no plan file path")
	}
	// The path might be absolute (within the container) or relative.
	path := planFilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read plan file %s: %w", path, err)
	}
	return string(data), nil
}

// createJob creates a new job and registers it with the hub.
func (o *Orchestrator) createJob(intent IntentResult, channel, threadTS string) string {
	jobID := generateJobID()
	slackThreadURL := ""
	if channel != "" && threadTS != "" {
		slackThreadURL = fmt.Sprintf("https://slack.com/archives/%s/p%s",
			channel, strings.ReplaceAll(threadTS, ".", ""))
	}

	o.hub.Emit(jobID, EventJobStarted, map[string]any{
		"task":             intent.Task,
		"phase":            string(PhasePlanning),
		"slack_thread_url": slackThreadURL,
		"channel":          channel,
		"thread_ts":        threadTS,
	})
	o.hub.RegisterThreadJob(channel, threadTS, jobID)

	o.hub.SetJobState(jobID, &JobState{
		Repo:     intent.Repo,
		Task:     intent.Task,
		Phase:    PhasePlanning,
		Channel:  channel,
		ThreadTS: threadTS,
	})

	return jobID
}

// closeJob emits a terminal event and unregisters the thread→job mapping.
func (o *Orchestrator) closeJob(ctx context.Context, jobID string, evtType EventType, data map[string]any) {
	o.hub.Emit(jobID, evtType, data)
	channel, _ := ctx.Value(ctxKeyChannel).(string)
	threadTS, _ := ctx.Value(ctxKeyThreadTS).(string)
	o.hub.UnregisterThreadJob(channel, threadTS)
	o.hub.SetPhase(jobID, PhaseDone)
}

// formatPlanMessage wraps a plan in the standard format for Slack.
func formatPlanMessage(plan string) string {
	return fmt.Sprintf("%s\n\n%s\n\n_Reply with your feedback, or say \"go\" to approve and start implementation._", planMarker, markdownToMrkdwn(plan))
}

// formatPlanBlocks returns Block Kit blocks for a plan message with an Approve button.
func formatPlanBlocks(plan, jobID string) []slack.Block {
	// Slack section blocks have a 3000 char limit for text.
	displayPlan := plan
	if len(displayPlan) > 2800 {
		displayPlan = displayPlan[:2800] + "\n..."
	}

	planSection := slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("%s\n\n%s", planMarker, markdownToMrkdwn(displayPlan)), false, false),
		nil, nil,
	)

	divider := slack.NewDividerBlock()

	ctxBlock := slack.NewContextBlock("",
		slack.NewTextBlockObject(slack.MarkdownType, "Reply with your feedback, or click *Approve* to start implementation.", false, false),
	)

	approveBtn := slack.NewButtonBlockElement("approve_plan", jobID,
		slack.NewTextBlockObject(slack.PlainTextType, "Approve", false, false),
	)
	approveBtn.Style = slack.StylePrimary

	actionsBlock := slack.NewActionBlock("plan_actions", approveBtn)

	return []slack.Block{planSection, divider, ctxBlock, actionsBlock}
}

// formatQuestionBlocks returns Block Kit blocks for a clarification question.
func formatQuestionBlocks(question string) []slack.Block {
	displayQuestion := question
	if len(displayQuestion) > 2800 {
		displayQuestion = displayQuestion[:2800] + "\n..."
	}

	section := slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("\u2753 *Clarification needed*\n\n%s", markdownToMrkdwn(displayQuestion)), false, false),
		nil, nil,
	)

	divider := slack.NewDividerBlock()

	ctxBlock := slack.NewContextBlock("",
		slack.NewTextBlockObject(slack.MarkdownType, "Reply in this thread to answer.", false, false),
	)

	return []slack.Block{section, divider, ctxBlock}
}

// formatApprovedPlanBlocks returns Block Kit blocks for an already-approved plan (no button).
func formatApprovedPlanBlocks(plan, approvedBy string) []slack.Block {
	displayPlan := plan
	if len(displayPlan) > 2800 {
		displayPlan = displayPlan[:2800] + "\n..."
	}

	planSection := slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("%s\n\n%s", planMarker, markdownToMrkdwn(displayPlan)), false, false),
		nil, nil,
	)

	divider := slack.NewDividerBlock()

	ctxBlock := slack.NewContextBlock("",
		slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("Approved by %s", approvedBy), false, false),
	)

	return []slack.Block{planSection, divider, ctxBlock}
}

// formatSupersededPlanBlocks returns Block Kit blocks for a plan that was superseded by feedback (no button).
func formatSupersededPlanBlocks(plan, label string) []slack.Block {
	displayPlan := plan
	if len(displayPlan) > 2800 {
		displayPlan = displayPlan[:2800] + "\n..."
	}

	planSection := slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("%s\n\n%s", planMarker, markdownToMrkdwn(displayPlan)), false, false),
		nil, nil,
	)

	divider := slack.NewDividerBlock()

	ctxBlock := slack.NewContextBlock("",
		slack.NewTextBlockObject(slack.MarkdownType, label, false, false),
	)

	return []slack.Block{planSection, divider, ctxBlock}
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

	var suffix [4]byte
	rand.Read(suffix[:])
	return "bob/" + s + "-" + hex.EncodeToString(suffix[:])
}
