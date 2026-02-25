package main

import (
	"testing"
)

func TestSanitizeGitOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		token  string
		want   string
	}{
		{
			name:   "replaces token in clone error",
			output: "fatal: could not read from remote repository 'https://x-access-token:ghp_secret123@github.com/org/repo.git'",
			token:  "ghp_secret123",
			want:   "fatal: could not read from remote repository 'https://x-access-token:[REDACTED]@github.com/org/repo.git'",
		},
		{
			name:   "replaces multiple occurrences",
			output: "token=ghp_abc line1\ntoken=ghp_abc line2",
			token:  "ghp_abc",
			want:   "token=[REDACTED] line1\ntoken=[REDACTED] line2",
		},
		{
			name:   "empty token is a no-op",
			output: "some output",
			token:  "",
			want:   "some output",
		},
		{
			name:   "no token present in output",
			output: "fatal: not a git repository",
			token:  "ghp_secret",
			want:   "fatal: not a git repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeGitOutput([]byte(tt.output), tt.token)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsSecretFile(t *testing.T) {
	blocked := []string{
		".env",
		".env.local",
		".env.production",
		"config/.env",
		"server.pem",
		"tls.key",
		"credentials.json",
		"service-account.json",
		"service-account-prod.json",
		"api.secret",
		"nested/dir/.env",
	}
	for _, f := range blocked {
		if !isSecretFile(f) {
			t.Errorf("expected %q to be blocked", f)
		}
	}

	allowed := []string{
		"main.go",
		"README.md",
		"src/handler.ts",
		"Dockerfile",
		"go.sum",
		"environment.go",
	}
	for _, f := range allowed {
		if isSecretFile(f) {
			t.Errorf("expected %q to be allowed", f)
		}
	}
}
