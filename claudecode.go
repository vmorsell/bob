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
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func ImplementChangesTool(claudeCodeToken string, notifier *SlackNotifier) Tool {
	return Tool{
		Name:        "implement_changes",
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

			// Ensure root owns the repo (may be left as worker from a previous run).
			chownRoot := exec.CommandContext(ctx, "chown", "-R", "0:0", repoDir)
			if out, err := chownRoot.CombinedOutput(); err != nil {
				return "", fmt.Errorf("chown to root failed: %s: %w", out, err)
			}

			// Reset to clean state.
			resetCmd := exec.CommandContext(ctx, "sh", "-c", "git checkout . && git clean -fd && git checkout main && git pull")
			resetCmd.Dir = repoDir
			if out, err := resetCmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("git reset failed: %s: %w", out, err)
			}

			// Ack to Slack.
			notifier.Notify(ctx, fmt.Sprintf("Working on implementing changes in `%s`...", repoName))

			// Run Claude Code CLI with a 5 minute timeout.
			cliCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()

			// Chown repo to worker user so claude CLI can read/write.
			chown := exec.CommandContext(cliCtx, "chown", "-R", "1000:1000", repoDir)
			if out, err := chown.CombinedOutput(); err != nil {
				return "", fmt.Errorf("chown failed: %s: %w", out, err)
			}

			cmd := exec.CommandContext(cliCtx, "claude", "-p", params.Task, "--dangerously-skip-permissions")
			cmd.Dir = repoDir
			cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN="+claudeCodeToken, "HOME=/home/worker")
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
			}

			output, err := cmd.CombinedOutput()

			// Chown back to root so subsequent git commands work.
			chownBack := exec.CommandContext(cliCtx, "chown", "-R", "0:0", repoDir)
			if out, chownErr := chownBack.CombinedOutput(); chownErr != nil {
				return "", fmt.Errorf("chown back failed: %s: %w", out, chownErr)
			}

			if err != nil {
				return "", fmt.Errorf("claude code failed: %s: %w", truncate(string(output), 500), err)
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

			// Truncate output to 50KB.
			result := string(output)
			if len(result) > 50*1024 {
				result = result[:50*1024] + "\n... (output truncated)"
			}
			return result, nil
		},
	}
}

func CreatePullRequestTool(owner, token string) Tool {
	return Tool{
		Name:        "create_pull_request",
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
