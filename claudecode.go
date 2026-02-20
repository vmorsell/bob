package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func ImplementChangesTool(owner, claudeCodeToken string) Tool {
	// Track which repo+job combos have already had a Claude Code session.
	// Key: "jobID:repoName", Value: true
	var sessions sync.Map

	return Tool{
		Name: "implement_changes",
		Description: "Use Claude Code CLI to implement code changes in a cloned repository. The repo must already be cloned to /workspace via clone_repo. Returns the Claude Code output describing what was changed.",
		Schema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name (must already be cloned to /workspace).",
				},
				"task": map[string]any{
					"type":        "string",
					"description": "Description of the code changes to implement.",
				},
			},
			Required: []string{"repo", "task"},
		},
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Repo string `json:"repo"`
				Task string `json:"task"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("parse input: %w", err)
			}

			repoName := filepath.Base(params.Repo)
			repoDir := filepath.Join("/workspace", repoName)

			if _, err := os.Stat(repoDir); os.IsNotExist(err) {
				return "", fmt.Errorf("repository %q not found at %s — clone it first using clone_repo", repoName, repoDir)
			}

			jobID := JobIDFromCtx(ctx)
			sessionKey := jobID + ":" + repoName
			_, isRetry := sessions.Load(sessionKey)

			if !isRetry {
				// First run: reset to clean state.
				chownRoot := exec.CommandContext(ctx, "chown", "-R", "0:0", repoDir)
				if out, err := chownRoot.CombinedOutput(); err != nil {
					return "", fmt.Errorf("chown to root failed: %s: %w", out, err)
				}
				resetCmd := exec.CommandContext(ctx, "sh", "-c", "git checkout . && git clean -fd && git checkout main && git pull")
				resetCmd.Dir = repoDir
				if out, err := resetCmd.CombinedOutput(); err != nil {
					return "", fmt.Errorf("git reset failed: %s: %w", out, err)
				}
			}

			// Run Claude Code CLI with a 15 minute timeout.
			cliCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			defer cancel()

			// Ensure worker owns the repo for Claude CLI.
			chown := exec.CommandContext(cliCtx, "chown", "-R", "1000:1000", repoDir)
			if out, err := chown.CombinedOutput(); err != nil {
				return "", fmt.Errorf("chown failed: %s: %w", out, err)
			}

			// Append summary instruction to the task.
			task := params.Task + "\n\nWhen you have finished all changes, write a brief summary starting with \"BOB_SUMMARY:\" on its own line, followed by 2-4 sentences describing what was implemented and what files were changed."

			// Build CLI args.
			args := []string{"-p", task,
				"--output-format", "stream-json",
				"--verbose",
				"--dangerously-skip-permissions"}
			if isRetry {
				args = append([]string{"--continue"}, args...)
			}
			cmd := exec.CommandContext(cliCtx, "claude", args...)
			cmd.Dir = repoDir
			cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN="+claudeCodeToken, "HOME=/home/worker")
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
			}

			sp := newClaudeStreamParser(HubFromCtx(ctx), JobIDFromCtx(ctx))
			cmd.Stdout = sp
			cmd.Stderr = sp
			runErr := cmd.Run()

			// Mark this repo+job as having a session (even if it failed/timed out).
			sessions.Store(sessionKey, true)

			// Chown back to root so subsequent git commands work.
			// Use parent ctx, not cliCtx — the CLI timeout may already be exceeded.
			chownBack := exec.CommandContext(ctx, "chown", "-R", "0:0", repoDir)
			if out, chownErr := chownBack.CombinedOutput(); chownErr != nil {
				return "", fmt.Errorf("chown back failed: %s: %w", out, chownErr)
			}

			if runErr != nil {
				return "", fmt.Errorf("claude code failed: %s: %w", truncate(sp.raw.String(), 500), runErr)
			}

			// Check if any changes were made.
			statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
			statusCmd.Dir = repoDir
			statusOut, err := statusCmd.Output()
			if err != nil {
				return "", fmt.Errorf("git status failed: %w", err)
			}
			if len(bytes.TrimSpace(statusOut)) == 0 {
				return "No changes were made.", nil
			}

			// Return the summary, or fall back to truncated raw output.
			result := extractSummary(sp.output())
			if result == "" {
				result = sp.output()
				if len(result) > 2000 {
					result = result[:2000] + "\n... (truncated)"
				}
			}
			return result, nil
		},
	}
}

// extractSummary extracts the text after "BOB_SUMMARY:" from Claude Code output.
func extractSummary(s string) string {
	const marker = "BOB_SUMMARY:"
	if i := strings.Index(s, marker); i >= 0 {
		return strings.TrimSpace(s[i+len(marker):])
	}
	return ""
}

// claudeStreamParser parses the --output-format stream-json output from the
// Claude Code CLI, emitting real-time hub events for each reasoning step and
// tool call, while also collecting the final result text.
type claudeStreamParser struct {
	hub     *Hub
	jobID   string
	lineBuf []byte
	raw     bytes.Buffer // full raw bytes, for error messages
	result  string       // text from the final "result" event
}

func newClaudeStreamParser(hub *Hub, jobID string) *claudeStreamParser {
	return &claudeStreamParser{hub: hub, jobID: jobID}
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
				for _, textLine := range strings.Split(block.Text, "\n") {
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
		// Emit the final summary as the last lines.
		for _, textLine := range strings.Split(p.result, "\n") {
			if strings.TrimSpace(textLine) != "" {
				p.emit(textLine)
			}
		}
	}
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

// claudeToolLabel builds a short human-readable label for a tool call.
func claudeToolLabel(name string, input json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return "  > " + name
	}
	// Pick the most meaningful string field to show.
	for _, key := range []string{"file_path", "command", "path", "pattern", "glob", "query", "description", "new_string"} {
		v, ok := m[key].(string)
		if !ok || v == "" {
			continue
		}
		if len(v) > 60 {
			v = v[:60] + "…"
		}
		return fmt.Sprintf("  > %s  %s=%s", name, key, v)
	}
	return "  > " + name
}

func CreatePullRequestTool(owner, token string) Tool {
	return Tool{
		Name: "create_pull_request",
		Description: "Create a GitHub pull request from uncommitted changes in a cloned repository. Commits all changes, pushes a new branch, and opens a PR. Returns the PR URL.",
		Schema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name (must already be cloned to /workspace with changes).",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Pull request title.",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch name to create (e.g. 'bob/fix-login-bug').",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Optional pull request description.",
				},
			},
			Required: []string{"repo", "title", "branch"},
		},
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Repo   string `json:"repo"`
				Title  string `json:"title"`
				Branch string `json:"branch"`
				Body   string `json:"body"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("parse input: %w", err)
			}

			repoName := filepath.Base(params.Repo)
			repoDir := filepath.Join("/workspace", repoName)

			// Configure git user.
			for _, args := range [][]string{
				{"config", "user.name", "Bob"},
				{"config", "user.email", "bob@noreply"},
			} {
				cmd := exec.CommandContext(ctx, "git", args...)
				cmd.Dir = repoDir
				if out, err := cmd.CombinedOutput(); err != nil {
					return "", fmt.Errorf("git config failed: %s: %w", out, err)
				}
			}

			// Create and checkout branch, stage, commit.
			gitSteps := []struct {
				args []string
				desc string
			}{
				{[]string{"checkout", "-b", params.Branch}, "create branch"},
				{[]string{"add", "-A"}, "stage changes"},
				{[]string{"commit", "-m", params.Title}, "commit"},
			}
			for _, step := range gitSteps {
				cmd := exec.CommandContext(ctx, "git", step.args...)
				cmd.Dir = repoDir
				if out, err := cmd.CombinedOutput(); err != nil {
					return "", fmt.Errorf("%s failed: %s: %w", step.desc, out, err)
				}
			}

			// Unshallow if needed (ignore error if already full).
			unshallow := exec.CommandContext(ctx, "git", "fetch", "--unshallow")
			unshallow.Dir = repoDir
			unshallow.CombinedOutput() // best-effort

			// Push branch.
			pushURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repoName)
			pushCmd := exec.CommandContext(ctx, "git", "push", pushURL, params.Branch)
			pushCmd.Dir = repoDir
			if out, err := pushCmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("push failed: %s: %w", out, err)
			}

			// Create PR via GitHub API.
			prBody := struct {
				Title string `json:"title"`
				Head  string `json:"head"`
				Base  string `json:"base"`
				Body  string `json:"body,omitempty"`
			}{
				Title: params.Title,
				Head:  params.Branch,
				Base:  "main",
				Body:  params.Body,
			}
			prJSON, err := json.Marshal(prBody)
			if err != nil {
				return "", fmt.Errorf("marshal PR body: %w", err)
			}

			apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repoName)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(prJSON))
			if err != nil {
				return "", fmt.Errorf("create request: %w", err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return "", fmt.Errorf("github api: %w", err)
			}
			defer resp.Body.Close()

			respBody, err := io.ReadAll(resp.Body)
			if err != nil {
				return "", fmt.Errorf("read response: %w", err)
			}

			if resp.StatusCode != http.StatusCreated {
				return "", fmt.Errorf("github api status %d: %s", resp.StatusCode, respBody)
			}

			var prResult struct {
				HTMLURL string `json:"html_url"`
			}
			if err := json.Unmarshal(respBody, &prResult); err != nil {
				return "", fmt.Errorf("parse PR response: %w", err)
			}

			return fmt.Sprintf("Pull request created: %s", prResult.HTMLURL), nil
		},
	}
}
