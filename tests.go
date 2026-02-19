package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func RunTestsTool(owner string) Tool {
	return Tool{
		Name: "run_tests",
		Description: "Run a test or build command in a cloned repository to verify that changes work. The repo must already be cloned to /workspace via clone_repo. Returns the command output and exit code.",
		Schema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name (must already be cloned to /workspace).",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "The test command to run (e.g. 'go test ./...', 'npm test').",
				},
			},
			Required: []string{"repo", "command"},
		},
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Repo    string `json:"repo"`
				Command string `json:"command"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("parse input: %w", err)
			}

			repoName := filepath.Base(params.Repo)
			repoDir := filepath.Join("/workspace", repoName)

			if _, err := os.Stat(repoDir); os.IsNotExist(err) {
				return "", fmt.Errorf("repository %q not found at %s — clone it first using clone_repo", repoName, repoDir)
			}

			cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()

			cmd := exec.CommandContext(cmdCtx, "sh", "-c", params.Command)
			cmd.Dir = repoDir

			sw := newStreamingWriter(HubFromCtx(ctx), JobIDFromCtx(ctx))
			cmd.Stdout = sw
			cmd.Stderr = sw
			runErr := cmd.Run()

			output := sw.buf.String()
			if len(output) > 50*1024 {
				output = output[:50*1024] + "\n... (output truncated)"
			}

			if runErr != nil {
				if exitErr, ok := runErr.(*exec.ExitError); ok {
					return fmt.Sprintf("Command exited with code %d:\n%s", exitErr.ExitCode(), output), nil
				}
				return "", fmt.Errorf("run command: %w", runErr)
			}

			return output, nil
		},
	}
}
