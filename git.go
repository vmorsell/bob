package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"
)

type repo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CloneURL    string `json:"clone_url"`
	Private     bool   `json:"private"`
}

func ListReposTool(owner, token string) Tool {
	return Tool{
		Name:        "list_repos",
		Description: "Search repositories owned by the configured GitHub user or organization. Returns matching repos with name, description, clone URL, and visibility.",
		Schema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Optional search query to filter repositories by name. If empty, lists all repos.",
				},
			},
		},
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("parse input: %w", err)
			}

			var repos []repo
			var fetchErr error

			if params.Query != "" {
				// Search API: user: qualifier works for both orgs and personal accounts.
				repos, fetchErr = fetchRepos(ctx, token,
					fmt.Sprintf("https://api.github.com/search/repositories?q=%s+user:%s&per_page=10", params.Query, owner),
					true)
			} else {
				// Try org endpoint first, fall back to user endpoint.
				repos, fetchErr = fetchRepos(ctx, token,
					fmt.Sprintf("https://api.github.com/orgs/%s/repos?per_page=30", owner),
					false)
				if fetchErr != nil {
					repos, fetchErr = fetchRepos(ctx, token,
						fmt.Sprintf("https://api.github.com/users/%s/repos?per_page=30", owner),
						false)
				}
			}
			if fetchErr != nil {
				return "", fetchErr
			}

			// Return a slim JSON array.
			type slimRepo struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				CloneURL    string `json:"clone_url"`
				Private     bool   `json:"private"`
			}
			slim := make([]slimRepo, len(repos))
			for i, r := range repos {
				slim[i] = slimRepo{
					Name:        r.Name,
					Description: r.Description,
					CloneURL:    r.CloneURL,
					Private:     r.Private,
				}
			}
			out, err := json.Marshal(slim)
			if err != nil {
				return "", fmt.Errorf("marshal result: %w", err)
			}
			return string(out), nil
		},
	}
}

func fetchRepos(ctx context.Context, token, url string, isSearch bool) ([]repo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api status %d: %s", resp.StatusCode, body)
	}

	if isSearch {
		var searchResult struct {
			Items []repo `json:"items"`
		}
		if err := json.Unmarshal(body, &searchResult); err != nil {
			return nil, fmt.Errorf("parse search response: %w", err)
		}
		return searchResult.Items, nil
	}

	var repos []repo
	if err := json.Unmarshal(body, &repos); err != nil {
		return nil, fmt.Errorf("parse repos response: %w", err)
	}
	return repos, nil
}

func CloneRepoTool(owner, token string) Tool {
	return Tool{
		Name:        "clone_repo",
		Description: "Clone a GitHub repository owned by the configured GitHub user or organization into the workspace. Uses a shallow clone for speed.",
		Schema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name (e.g. 'payment-service').",
				},
			},
			Required: []string{"repo"},
		},
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Repo string `json:"repo"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("parse input: %w", err)
			}

			// Sanitize repo name to prevent path traversal.
			repoName := filepath.Base(params.Repo)
			dest := filepath.Join("/workspace", repoName)

			if _, err := os.Stat(dest); err == nil {
				return fmt.Sprintf("Repository %q is already cloned at %s.", repoName, dest), nil
			}

			cloneURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repoName)
			cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", cloneURL, dest)
			output, err := cmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("git clone failed: %s: %w", output, err)
			}

			return fmt.Sprintf("Successfully cloned %s/%s to %s.", owner, repoName, dest), nil
		},
	}
}
