package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const planSystemPrompt = `You are a senior software engineer planning changes to a codebase.

Your job is to explore the codebase thoroughly and produce a detailed, self-contained implementation plan. The plan must include enough context that a different engineer (or a fresh session of yourself) can implement it without re-exploring the codebase.

Requirements for the plan:
- List every file to be created or modified, with the specific changes needed
- Include relevant code snippets, function signatures, and type definitions from the existing codebase that the implementer will need to reference
- Specify the order of operations
- Note any existing patterns or conventions the implementation should follow
- If you need to ask the user a clarifying question, do so via AskUserQuestion

Do NOT modify any files. Use only read-only tools (Read, Glob, Grep, Task with Explore agents).

When your plan is complete, write it to the plan file and call ExitPlanMode.`

const executeSystemPrompt = `You are a senior software engineer implementing changes to a codebase.

You have been given an approved implementation plan. Follow it precisely.

Rules:
- Implement exactly what the plan specifies — no more, no less
- Follow existing codebase conventions
- Do not refactor, optimize, or "improve" code outside the plan scope
- If the plan is ambiguous on a specific point, make the simplest choice consistent with the surrounding code
- Do not run tests or start servers — just make the file changes

When done, output a brief summary of what was changed.`

// SessionOpts configures a RunSession call.
type SessionOpts struct {
	RepoDir        string // /workspace/<repo>
	Prompt         string // the -p argument
	SystemPrompt   string // prepended to prompt (planning or execution instructions)
	SessionID      string // --resume <id>; empty = new session
	PermissionMode string // "plan" or "acceptEdits"
}

// SessionResult captures the structured outcome of a Claude Code session.
type SessionResult struct {
	SessionID    string // from system/init event
	PlanFilePath string // Write to .claude/plans/ detected
	Question     string // from AskUserQuestion tool_use input
	PlanExited   bool   // ExitPlanMode tool_use detected
	ResultText   string // from result event
	IsError      bool
}

// resetRepo resets a cloned repo to clean main state.
func resetRepo(ctx context.Context, repoDir string) error {
	chownRoot := exec.CommandContext(ctx, "chown", "-R", "0:0", repoDir)
	if out, err := chownRoot.CombinedOutput(); err != nil {
		return fmt.Errorf("chown to root failed: %s: %w", out, err)
	}
	resetCmd := exec.CommandContext(ctx, "sh", "-c", "git checkout . && git clean -fd && git checkout main && git pull")
	resetCmd.Dir = repoDir
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset failed: %s: %w", out, err)
	}
	return nil
}

// RunSession executes a Claude Code CLI session.
func RunSession(ctx context.Context, claudeCodeToken string, hub *Hub, jobID string, opts SessionOpts) (*SessionResult, error) {
	if _, err := os.Stat(opts.RepoDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("repository not found at %s", opts.RepoDir)
	}

	// Build the prompt: system prompt + user prompt.
	prompt := opts.Prompt
	if opts.SystemPrompt != "" {
		prompt = opts.SystemPrompt + "\n\n---\n\n" + prompt
	}

	// Run Claude Code CLI with a 15-minute timeout.
	cliCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	// Ensure worker owns the repo for Claude CLI.
	chown := exec.CommandContext(cliCtx, "chown", "-R", "1000:1000", opts.RepoDir)
	if out, err := chown.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("chown failed: %s: %w", out, err)
	}

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
	}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	if opts.SessionID != "" {
		args = append(args, "--resume", opts.SessionID)
	}

	cmd := exec.CommandContext(cliCtx, "claude", args...)
	cmd.Dir = opts.RepoDir
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN="+claudeCodeToken, "HOME=/home/worker")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
	}

	sp := newClaudeStreamParser(hub, jobID)
	cmd.Stdout = sp
	cmd.Stderr = sp
	runErr := cmd.Run()

	// Chown back to root so subsequent git commands work.
	chownBack := exec.CommandContext(ctx, "chown", "-R", "0:0", opts.RepoDir)
	if out, chownErr := chownBack.CombinedOutput(); chownErr != nil {
		return nil, fmt.Errorf("chown back failed: %s: %w", out, chownErr)
	}

	if runErr != nil {
		return nil, fmt.Errorf("claude code failed: %s: %w", truncate(sp.raw.String(), 500), runErr)
	}

	return sp.result(), nil
}

// claudeStreamParser parses the --output-format stream-json output from the
// Claude Code CLI, emitting real-time hub events for each reasoning step and
// tool call, while collecting structured results.
type claudeStreamParser struct {
	hub   *Hub
	jobID string

	lineBuf []byte
	raw     bytes.Buffer // full raw bytes, for error messages

	// Structured results captured from the stream.
	sessionID    string
	planFilePath string
	question     string
	planExited   bool
	resultText   string
	isError      bool

	pendingTaskDescs  map[string]string // tool_use_id → Task description
	thinkingStartedAt time.Time
}

func newClaudeStreamParser(hub *Hub, jobID string) *claudeStreamParser {
	return &claudeStreamParser{
		hub:              hub,
		jobID:            jobID,
		pendingTaskDescs: make(map[string]string),
	}
}

func (p *claudeStreamParser) Write(data []byte) (int, error) {
	p.raw.Write(data)
	for _, b := range data {
		if b == '\n' {
			p.processLine(string(p.lineBuf))
			p.lineBuf = p.lineBuf[:0]
		} else {
			p.lineBuf = append(p.lineBuf, b)
		}
	}
	return len(data), nil
}

func (p *claudeStreamParser) result() *SessionResult {
	return &SessionResult{
		SessionID:    p.sessionID,
		PlanFilePath: p.planFilePath,
		Question:     p.question,
		PlanExited:   p.planExited,
		ResultText:   p.resultText,
		IsError:      p.isError,
	}
}

// claudeStreamEvent covers the shapes we care about from --output-format stream-json.
type claudeStreamEvent struct {
	Type            string `json:"type"`
	Subtype         string `json:"subtype"`
	SessionID       string `json:"session_id"` // populated on type=system, subtype=init
	ParentToolUseID string `json:"parent_tool_use_id"`
	Message         struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	} `json:"message"`
	Result string `json:"result"` // populated on type=result
	Error  string `json:"error"`  // populated on type=result,subtype=error
}

type claudeContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"` // populated for type=thinking
	Name     string          `json:"name"`
	ID       string          `json:"id"` // populated for type=tool_use
	Input    json.RawMessage `json:"input"`
}

// claudeToolResultBlock represents a tool_result content block in a "user" event.
type claudeToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

func (p *claudeStreamParser) processLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	var evt claudeStreamEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		// Not JSON (e.g. stderr noise) — emit verbatim.
		p.emit(line)
		return
	}

	switch evt.Type {
	case "system":
		if evt.Subtype == "init" && evt.SessionID != "" {
			p.sessionID = evt.SessionID
		}
	case "assistant":
		for _, raw := range evt.Message.Content {
			var block claudeContentBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				continue
			}
			switch block.Type {
			case "text":
				for _, textLine := range strings.Split(block.Text, "\n") {
					if strings.TrimSpace(textLine) != "" {
						p.emit(textLine)
					}
				}
			case "thinking":
				p.thinkingStartedAt = time.Now()
				if p.hub != nil && p.jobID != "" {
					p.hub.Emit(p.jobID, EventClaudeCodeLine, map[string]any{
						"thinking":    block.Thinking,
						"thinking_ts": time.Now().UnixMilli(),
					})
				}
			case "tool_use":
				p.processToolUse(block, evt.ParentToolUseID)
			}
		}
	case "user":
		var completed []map[string]any
		for _, raw := range evt.Message.Content {
			var block claudeToolResultBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				continue
			}
			if block.Type != "tool_result" {
				continue
			}
			if block.IsError {
				if p.hub != nil && p.jobID != "" {
					p.hub.Emit(p.jobID, EventClaudeCodeLine, map[string]any{
						"tool_error": truncate(block.Content, 300),
					})
				}
				continue
			}
			if desc, ok := p.pendingTaskDescs[block.ToolUseID]; ok {
				completed = append(completed, map[string]any{"description": desc})
				delete(p.pendingTaskDescs, block.ToolUseID)
			}
		}
		if len(completed) > 0 && p.hub != nil && p.jobID != "" {
			p.hub.Emit(p.jobID, EventClaudeCodeLine, map[string]any{
				"agents_finished": len(completed),
				"agents":          completed,
			})
		}
	case "result":
		if evt.Subtype == "error" && evt.Error != "" {
			p.resultText = evt.Error
			p.isError = true
		} else {
			p.resultText = evt.Result
		}
		for _, textLine := range strings.Split(p.resultText, "\n") {
			if strings.TrimSpace(textLine) != "" {
				p.emit(textLine)
			}
		}
	case "rate_limit_event":
		// no-op
	}
}

// processToolUse handles tool_use blocks, extracting signals and emitting hub events.
func (p *claudeStreamParser) processToolUse(block claudeContentBlock, parentToolUseID string) {
	// Only detect signals from the main agent (not sub-agents).
	if parentToolUseID == "" {
		switch block.Name {
		case "AskUserQuestion":
			var input struct {
				Questions []struct {
					Question string `json:"question"`
				} `json:"questions"`
			}
			if err := json.Unmarshal(block.Input, &input); err == nil && len(input.Questions) > 0 {
				p.question = input.Questions[0].Question
			}
		case "ExitPlanMode":
			p.planExited = true
		case "Write":
			var input struct {
				FilePath string `json:"file_path"`
			}
			if err := json.Unmarshal(block.Input, &input); err == nil {
				if strings.Contains(input.FilePath, ".claude/plans/") {
					p.planFilePath = input.FilePath
				}
			}
		}
	}

	// Track Task sub-agent IDs for later aggregation.
	if block.Name == "Task" && block.ID != "" {
		var input struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(block.Input, &input); err == nil && input.Description != "" {
			p.pendingTaskDescs[block.ID] = input.Description
		}
	}

	p.emitTool(block.Name, block.Input)
}

func (p *claudeStreamParser) emit(text string) {
	if p.hub == nil || p.jobID == "" {
		return
	}
	p.hub.Emit(p.jobID, EventClaudeCodeLine, map[string]any{"text": text})
}

// emitTool emits a claude_code_line event carrying the full tool input so the
// UI can render rich diffs (Edit/Write) and checklists (TodoWrite).
func (p *claudeStreamParser) emitTool(name string, input json.RawMessage) {
	if p.hub == nil || p.jobID == "" {
		return
	}
	inputStr := ""
	if len(input) > 0 {
		inputStr = string(input)
	}
	p.hub.Emit(p.jobID, EventClaudeCodeLine, map[string]any{
		"tool_name":  name,
		"tool_input": inputStr,
	})
}
