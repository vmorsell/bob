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

// CloneRepo shallow-clones a GitHub repository to /workspace/{repoName}.
// No-ops if the directory already exists.
func CloneRepo(ctx context.Context, owner, token, repoName string) error {
	repoName = filepath.Base(repoName)
	dest := filepath.Join("/workspace", repoName)

	if _, err := os.Stat(dest); err == nil {
		return nil // already cloned
	}

	cloneURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repoName)
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", cloneURL, dest)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %s: %w", sanitizeGitOutput(output, token), err)
	}
	return nil
}

// CreatePullRequest commits all changes, pushes a new branch, and opens a PR.
// Returns the PR HTML URL.
func CreatePullRequest(ctx context.Context, owner, token, repoName, title, branch, body string) (string, error) {
	repoName = filepath.Base(repoName)
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

	// Unshallow if needed (ignore error if already full).
	unshallow := exec.CommandContext(ctx, "git", "fetch", "--unshallow")
	unshallow.Dir = repoDir
	unshallow.CombinedOutput() // best-effort

	// Push branch.
	pushURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repoName)
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
