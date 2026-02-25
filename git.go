package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sanitizeGitOutput removes embedded credentials from git command output.
func sanitizeGitOutput(output []byte, token string) string {
	s := string(output)
	if token != "" {
		s = strings.ReplaceAll(s, token, "[REDACTED]")
	}
	return s
}

// isSecretFile returns true if a filename looks like it contains secrets.
func isSecretFile(name string) bool {
	lower := strings.ToLower(filepath.Base(name))
	for _, pat := range []string{".env", ".pem", ".key", ".secret", "credentials", "service-account"} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// changedFiles returns the list of modified and untracked files in repoDir,
// excluding files that look like they contain secrets.
func changedFiles(ctx context.Context, repoDir string) ([]string, error) {
	// Modified/deleted files.
	diffCmd := exec.CommandContext(ctx, "git", "diff", "--name-only")
	diffCmd.Dir = repoDir
	diffOut, err := diffCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %s: %w", diffOut, err)
	}

	// New untracked files (respects .gitignore).
	lsCmd := exec.CommandContext(ctx, "git", "ls-files", "--others", "--exclude-standard")
	lsCmd.Dir = repoDir
	lsOut, err := lsCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git ls-files failed: %s: %w", lsOut, err)
	}

	seen := make(map[string]bool)
	var files []string
	for _, line := range strings.Split(string(diffOut)+"\n"+string(lsOut), "\n") {
		f := strings.TrimSpace(line)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		if isSecretFile(f) {
			log.Printf("WARNING: skipping potentially sensitive file: %s", f)
			continue
		}
		files = append(files, f)
	}
	return files, nil
}

type repo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CloneURL    string `json:"clone_url"`
	Private     bool   `json:"private"`
}

// FindRepo checks whether a repository exists in the GitHub owner's org/account.
func FindRepo(ctx context.Context, token, owner, name string) (repo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return repo{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return repo{}, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return repo{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return repo{}, fmt.Errorf("repository %q not found", name)
	}
	if resp.StatusCode != http.StatusOK {
		return repo{}, fmt.Errorf("github api status %d: %s", resp.StatusCode, body)
	}

	var r repo
	if err := json.Unmarshal(body, &r); err != nil {
		return repo{}, fmt.Errorf("parse response: %w", err)
	}
	return r, nil
}

// EnsureBaseClone ensures a shallow base clone exists at /workspace/<repoName>
// and fetches the latest main. The base clone is never used directly by jobs;
// worktrees are created from it instead.
func EnsureBaseClone(ctx context.Context, owner, token, repoName string) (baseDir string, err error) {
	repoName = filepath.Base(repoName)
	baseDir = filepath.Join("/workspace", repoName)
	fetchURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repoName)

	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", fetchURL, baseDir)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git clone failed: %s: %w", sanitizeGitOutput(output, token), err)
		}

		// Remove token from stored remote URL so Claude Code can't read it from .git/config.
		cleanURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repoName)
		setURL := exec.CommandContext(ctx, "git", "remote", "set-url", "origin", cleanURL)
		setURL.Dir = baseDir
		if out, err := setURL.CombinedOutput(); err != nil {
			return "", fmt.Errorf("set-url failed: %s: %w", out, err)
		}
	}

	// Fetch latest main so FETCH_HEAD is current.
	fetch := exec.CommandContext(ctx, "git", "fetch", fetchURL, "main")
	fetch.Dir = baseDir
	if out, err := fetch.CombinedOutput(); err != nil {
		return "", fmt.Errorf("fetch main failed: %s: %w", sanitizeGitOutput(out, token), err)
	}
	return baseDir, nil
}

// CreateWorktree creates a git worktree for a job from FETCH_HEAD.
// Returns the worktree path.
func CreateWorktree(ctx context.Context, baseDir, jobID string) (string, error) {
	wtPath := filepath.Join(baseDir, "worktrees", jobID)
	branch := "job/" + jobID

	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branch, wtPath, "FETCH_HEAD")
	cmd.Dir = baseDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("worktree add failed: %s: %w", out, err)
	}
	return wtPath, nil
}

// RemoveWorktree removes a job's worktree and its branch.
func RemoveWorktree(ctx context.Context, baseDir, wtPath, jobID string) {
	rm := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wtPath)
	rm.Dir = baseDir
	if out, err := rm.CombinedOutput(); err != nil {
		log.Printf("worktree remove failed (non-fatal): %s: %v", out, err)
	}

	prune := exec.CommandContext(ctx, "git", "worktree", "prune")
	prune.Dir = baseDir
	prune.CombinedOutput() // best-effort

	branch := "job/" + jobID
	del := exec.CommandContext(ctx, "git", "branch", "-D", branch)
	del.Dir = baseDir
	if out, err := del.CombinedOutput(); err != nil {
		log.Printf("branch delete failed (non-fatal): %s: %v", out, err)
	}
}

// ResetWorktree fetches latest main and hard-resets the worktree, giving a
// clean starting point for implementation. Fetch runs on the base clone,
// FETCH_HEAD is resolved to a SHA there, and the SHA is used for the reset
// in the worktree (avoids per-worktree FETCH_HEAD portability issues).
func ResetWorktree(ctx context.Context, baseDir, wtPath, token, owner, repoName string) error {
	fetchURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repoName)
	fetch := exec.CommandContext(ctx, "git", "fetch", fetchURL, "main")
	fetch.Dir = baseDir
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch main failed: %s: %w", sanitizeGitOutput(out, token), err)
	}

	// Resolve FETCH_HEAD to a commit hash on the base clone where it's reliable.
	revParse := exec.CommandContext(ctx, "git", "rev-parse", "FETCH_HEAD")
	revParse.Dir = baseDir
	hashOut, err := revParse.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rev-parse FETCH_HEAD failed: %s: %w", hashOut, err)
	}
	commit := strings.TrimSpace(string(hashOut))

	reset := exec.CommandContext(ctx, "git", "reset", "--hard", commit)
	reset.Dir = wtPath
	if out, err := reset.CombinedOutput(); err != nil {
		return fmt.Errorf("reset worktree failed: %s: %w", out, err)
	}

	clean := exec.CommandContext(ctx, "git", "clean", "-fd")
	clean.Dir = wtPath
	if out, err := clean.CombinedOutput(); err != nil {
		return fmt.Errorf("clean worktree failed: %s: %w", out, err)
	}
	return nil
}

// CreatePullRequest commits all changes, pushes a new branch, and opens a PR.
// repoDir is the working directory (typically a worktree path).
// Returns the PR HTML URL.
func CreatePullRequest(ctx context.Context, owner, token, repoName, repoDir, title, branch, body string) (string, error) {
	repoName = filepath.Base(repoName)

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

	// Create branch.
	checkoutCmd := exec.CommandContext(ctx, "git", "checkout", "-b", branch)
	checkoutCmd.Dir = repoDir
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create branch failed: %s: %w", out, err)
	}

	// Collect changed and untracked files, filtering out secrets.
	filesToAdd, err := changedFiles(ctx, repoDir)
	if err != nil {
		return "", err
	}
	if len(filesToAdd) == 0 {
		return "", fmt.Errorf("no files to commit")
	}

	// Stage only the approved files.
	addArgs := append([]string{"add", "--"}, filesToAdd...)
	addCmd := exec.CommandContext(ctx, "git", addArgs...)
	addCmd.Dir = repoDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("stage changes failed: %s: %w", out, err)
	}

	// Commit.
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", title)
	commitCmd.Dir = repoDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("commit failed: %s: %w", out, err)
	}

	// Token URL for authenticated fetch/push operations.
	pushURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repoName)

	// Unshallow if needed (ignore error if already full).
	// Uses explicit token URL since the stored origin no longer has credentials.
	unshallow := exec.CommandContext(ctx, "git", "fetch", "--unshallow", pushURL)
	unshallow.Dir = repoDir
	unshallow.CombinedOutput() // best-effort
	pushCmd := exec.CommandContext(ctx, "git", "push", pushURL, branch)
	pushCmd.Dir = repoDir
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("push failed: %s: %w", sanitizeGitOutput(out, token), err)
	}

	// Create PR via GitHub API.
	prPayload := struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body,omitempty"`
	}{
		Title: title,
		Head:  branch,
		Base:  "main",
		Body:  body,
	}
	prJSON, err := json.Marshal(prPayload)
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
	return prResult.HTMLURL, nil
}
