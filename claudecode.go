package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const terminalStatePromptSuffix = `

At the very end of your work, output a single JSON line (no code block):
{"status":"completed","message":"Brief summary of what was done"}
or
{"status":"needs_information","message":"Specific question for the user"}
or
{"status":"error","message":"What went wrong"}`

// TerminalState is the structured outcome reported by Claude Code at the end of its run.
type TerminalState struct {
	Status  string `json:"status"`  // "completed", "needs_information", or "error"
	Message string `json:"message"` // summary, question, or error description
}

// ImplementChanges runs Claude Code CLI in the given repo to implement the task.
// It parses the terminal state JSON from Claude Code's output and returns it.
func ImplementChanges(ctx context.Context, claudeCodeToken string, notifier *SlackNotifier, repoName, task string) (TerminalState, error) {
	repoName = filepath.Base(repoName)
	repoDir := filepath.Join("/workspace", repoName)

	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return TerminalState{}, fmt.Errorf("repository %q not found at %s", repoName, repoDir)
	}

	// Reset to clean state.
	chownRoot := exec.CommandContext(ctx, "chown", "-R", "0:0", repoDir)
	if out, err := chownRoot.CombinedOutput(); err != nil {
		return TerminalState{}, fmt.Errorf("chown to root failed: %s: %w", out, err)
	}
	resetCmd := exec.CommandContext(ctx, "sh", "-c", "git checkout . && git clean -fd && git checkout main && git pull")
	resetCmd.Dir = repoDir
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return TerminalState{}, fmt.Errorf("git reset failed: %s: %w", out, err)
	}

	// Run Claude Code CLI with a 15-minute timeout.
	cliCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	// Ensure worker owns the repo for Claude CLI.
	chown := exec.CommandContext(cliCtx, "chown", "-R", "1000:1000", repoDir)
	if out, err := chown.CombinedOutput(); err != nil {
		return TerminalState{}, fmt.Errorf("chown failed: %s: %w", out, err)
	}

	fullTask := task + terminalStatePromptSuffix
	args := []string{
		"-p", fullTask,
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
	}
	cmd := exec.CommandContext(cliCtx, "claude", args...)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN="+claudeCodeToken, "HOME=/home/worker")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
	}

	sp := newClaudeStreamParser(HubFromCtx(ctx), JobIDFromCtx(ctx), notifier, ctx)
	cmd.Stdout = sp
	cmd.Stderr = sp
	runErr := cmd.Run()

	// Chown back to root so subsequent git commands work.
	// Use parent ctx, not cliCtx — the CLI timeout may already be exceeded.
	chownBack := exec.CommandContext(ctx, "chown", "-R", "0:0", repoDir)
	if out, chownErr := chownBack.CombinedOutput(); chownErr != nil {
		return TerminalState{}, fmt.Errorf("chown back failed: %s: %w", out, chownErr)
	}

	if runErr != nil {
		return TerminalState{}, fmt.Errorf("claude code failed: %s: %w", truncate(sp.raw.String(), 500), runErr)
	}

	// Use parsed terminal state if Claude Code emitted one.
	if sp.terminalState.Status != "" {
		return sp.terminalState, nil
	}

	// Fall back: check if changes were made.
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	statusCmd.Dir = repoDir
	statusOut, err := statusCmd.Output()
	if err != nil {
		return TerminalState{Status: "error", Message: fmt.Sprintf("git status failed: %v", err)}, nil
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		return TerminalState{Status: "completed", Message: "No changes were made."}, nil
	}
	return TerminalState{Status: "completed", Message: sp.output()}, nil
}

// claudeStreamParser parses the --output-format stream-json output from the
// Claude Code CLI, emitting real-time hub events for each reasoning step and
// tool call, while also collecting the final result text and terminal state.
type claudeStreamParser struct {
	hub           *Hub
	jobID         string
	notifier      *SlackNotifier
	ctx           context.Context
	lineBuf       []byte
	raw           bytes.Buffer // full raw bytes, for error messages
	result        string       // text from the final "result" event
	terminalState TerminalState
}

func newClaudeStreamParser(hub *Hub, jobID string, notifier *SlackNotifier, ctx context.Context) *claudeStreamParser {
	return &claudeStreamParser{hub: hub, jobID: jobID, notifier: notifier, ctx: ctx}
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

func (p *claudeStreamParser) output() string {
	if p.result != "" {
		return p.result
	}
	return p.raw.String()
}

// claudeStreamEvent covers the shapes we care about from --output-format stream-json.
type claudeStreamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	} `json:"message"`
	Result string `json:"result"` // populated on type=result
	Error  string `json:"error"`  // populated on type=result,subtype=error
}

type claudeContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
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
		// init metadata — skip
	case "assistant":
		for _, raw := range evt.Message.Content {
			var block claudeContentBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				continue
			}
			switch block.Type {
			case "text":
				// Scan each line for terminal state JSON; filter it out of output.
				var filteredLines []string
				for _, textLine := range strings.Split(block.Text, "\n") {
					if ts, ok := tryParseTerminalState(textLine); ok {
						p.terminalState = ts
						continue // don't emit or notify the terminal state JSON
					}
					filteredLines = append(filteredLines, textLine)
				}
				filteredText := strings.Join(filteredLines, "\n")
				if p.notifier != nil && strings.TrimSpace(filteredText) != "" {
					p.notifier.Notify(p.ctx, filteredText)
				}
				for _, textLine := range filteredLines {
					if strings.TrimSpace(textLine) != "" {
						p.emit(textLine)
					}
				}
			case "tool_use":
				p.emitTool(block.Name, block.Input)
			}
		}
	case "user":
		// tool results — skip to keep output clean
	case "result":
		if evt.Subtype == "error" && evt.Error != "" {
			p.result = evt.Error
		} else {
			p.result = evt.Result
		}
		// Try to find terminal state in result if not already captured.
		if p.terminalState.Status == "" {
			for _, textLine := range strings.Split(p.result, "\n") {
				if ts, ok := tryParseTerminalState(textLine); ok {
					p.terminalState = ts
					break
				}
			}
		}
		// Emit the final summary as the last lines.
		for _, textLine := range strings.Split(p.result, "\n") {
			if strings.TrimSpace(textLine) != "" {
				p.emit(textLine)
			}
		}
	}
}

// tryParseTerminalState attempts to parse a line as terminal state JSON.
func tryParseTerminalState(line string) (TerminalState, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, `{"status":`) {
		return TerminalState{}, false
	}
	var ts TerminalState
	if err := json.Unmarshal([]byte(line), &ts); err != nil {
		return TerminalState{}, false
	}
	if ts.Status == "" {
		return TerminalState{}, false
	}
	return ts, true
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
