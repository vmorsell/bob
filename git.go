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
	"sort"
	"strings"

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
		Description: "Search repositories owned by the configured GitHub user or organization. Returns matching repos with name, description, clone URL, and visibility. When a query is provided, returns exact matches plus fuzzy matches for misspellings.",
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

			// Always fetch all repos so we can do fuzzy matching.
			repos, fetchErr := fetchRepos(ctx, token,
				fmt.Sprintf("https://api.github.com/orgs/%s/repos?per_page=100", owner),
				false)
			if fetchErr != nil {
				repos, fetchErr = fetchRepos(ctx, token,
					fmt.Sprintf("https://api.github.com/users/%s/repos?per_page=100", owner),
					false)
			}
			if fetchErr != nil {
				return "", fetchErr
			}

			type slimRepo struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				CloneURL    string `json:"clone_url"`
				Private     bool   `json:"private"`
			}

			toSlim := func(r repo) slimRepo {
				return slimRepo{Name: r.Name, Description: r.Description, CloneURL: r.CloneURL, Private: r.Private}
			}

			if params.Query == "" {
				slim := make([]slimRepo, len(repos))
				for i, r := range repos {
					slim[i] = toSlim(r)
				}
				out, err := json.Marshal(slim)
				if err != nil {
					return "", fmt.Errorf("marshal result: %w", err)
				}
				return string(out), nil
			}

			// Fuzzy match: exact first, then by Levenshtein distance.
			query := strings.ToLower(params.Query)
			type scored struct {
				r    repo
				dist int
			}
			var matches []scored
			for _, r := range repos {
				name := strings.ToLower(r.Name)
				if name == query {
					// Exact match — return immediately.
					out, err := json.Marshal([]slimRepo{toSlim(r)})
					if err != nil {
						return "", fmt.Errorf("marshal result: %w", err)
					}
					return string(out), nil
				}
				dist := levenshtein(query, name)
				// Include repos within edit distance 3 or where query is a substring.
				if dist <= 3 || strings.Contains(name, query) || strings.Contains(query, name) {
					matches = append(matches, scored{r, dist})
				}
			}

			if len(matches) == 0 {
				// No close matches — return all repos so the LLM can reason about the best fit.
				type result struct {
					Message string     `json:"message"`
					AllRepos []slimRepo `json:"all_repos"`
				}
				slim := make([]slimRepo, len(repos))
				for i, r := range repos {
					slim[i] = toSlim(r)
				}
				out, err := json.Marshal(result{
					Message:  fmt.Sprintf("No repository closely matching %q was found. Here are all available repositories so you can identify the best match:", params.Query),
					AllRepos: slim,
				})
				if err != nil {
					return "", fmt.Errorf("marshal result: %w", err)
				}
				return string(out), nil
			}

			// Sort by distance ascending.
			sort.Slice(matches, func(i, j int) bool { return matches[i].dist < matches[j].dist })

			type fuzzyResult struct {
				Message string     `json:"message"`
				Matches []slimRepo `json:"matches"`
			}
			slim := make([]slimRepo, len(matches))
			for i, m := range matches {
				slim[i] = toSlim(m.r)
			}
			out, err := json.Marshal(fuzzyResult{
				Message: fmt.Sprintf("No exact match for %q. Closest repositories by name similarity:", params.Query),
				Matches: slim,
			})
			if err != nil {
				return "", fmt.Errorf("marshal result: %w", err)
			}
			return string(out), nil
		},
	}
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	row := make([]int, lb+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= la; i++ {
		prev := row[0]
		row[0] = i
		for j := 1; j <= lb; j++ {
			tmp := row[j]
			if ra[i-1] == rb[j-1] {
				row[j] = prev
			} else {
				row[j] = 1 + min(prev, min(row[j], row[j-1]))
			}
			prev = tmp
		}
	}
	return row[lb]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
		Name: "clone_repo",
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
