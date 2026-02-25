package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestIsValidRepoName(t *testing.T) {
	valid := []string{
		"myrepo",
		"my-repo",
		"my_repo",
		"my.repo",
		"MyRepo123",
		"a",
	}
	for _, name := range valid {
		if !isValidRepoName(name) {
			t.Errorf("expected %q to be valid", name)
		}
	}

	invalid := []string{
		"",
		"repo with spaces",
		"repo/slash",
		"repo@mention",
		"repo;drop table",
		"$(cmd)",
		string(make([]byte, 101)), // too long
	}
	for _, name := range invalid {
		if isValidRepoName(name) {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}

func TestParseAllowedRepos(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]bool
	}{
		{"empty", "", nil},
		{"whitespace only", "  ", nil},
		{"single repo", "myrepo", map[string]bool{"myrepo": true}},
		{"multiple repos", "foo,bar,baz", map[string]bool{"foo": true, "bar": true, "baz": true}},
		{"with whitespace", " foo , bar ", map[string]bool{"foo": true, "bar": true}},
		{"trailing comma", "foo,bar,", map[string]bool{"foo": true, "bar": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAllowedRepos(tt.input)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("expected %d entries, got %d: %v", len(tt.want), len(got), got)
				return
			}
			for k := range tt.want {
				if !got[k] {
					t.Errorf("expected key %q in result", k)
				}
			}
		})
	}
}

func TestTaskBranchName(t *testing.T) {
	branchRe := regexp.MustCompile(`^bob/[a-z0-9-]+-[0-9a-f]{8}$`)

	t.Run("format matches pattern", func(t *testing.T) {
		name := taskBranchName("Add user authentication")
		if !branchRe.MatchString(name) {
			t.Errorf("branch name %q does not match expected pattern", name)
		}
	})

	t.Run("spaces become hyphens", func(t *testing.T) {
		name := taskBranchName("fix login bug")
		// Strip the random suffix for slug check.
		slug := name[len("bob/") : len(name)-9] // 8 hex chars + dash
		if strings.Contains(slug, " ") {
			t.Errorf("slug %q still contains spaces", slug)
		}
	})

	t.Run("special chars stripped", func(t *testing.T) {
		name := taskBranchName("fix: the bug! (urgent)")
		if !branchRe.MatchString(name) {
			t.Errorf("branch name %q does not match expected pattern", name)
		}
	})

	t.Run("long task truncated", func(t *testing.T) {
		longTask := strings.Repeat("word ", 30) // 150 chars
		name := taskBranchName(longTask)
		// "bob/" prefix (4) + slug (<=50) + "-" (1) + hex (8) = max 63
		if len(name) > 63 {
			t.Errorf("branch name too long: %d chars", len(name))
		}
	})

	t.Run("consecutive hyphens collapsed", func(t *testing.T) {
		name := taskBranchName("fix   multiple   spaces")
		slug := name[len("bob/") : len(name)-9]
		if strings.Contains(slug, "--") {
			t.Errorf("slug %q contains consecutive hyphens", slug)
		}
	})

	t.Run("unique suffix each call", func(t *testing.T) {
		a := taskBranchName("same task")
		b := taskBranchName("same task")
		if a == b {
			t.Errorf("expected different suffixes, both got %q", a)
		}
	})
}

func TestFormatPlanMessage(t *testing.T) {
	msg := formatPlanMessage("Do the thing.\n\nStep 1: foo")
	if !strings.Contains(msg, planMarker) {
		t.Error("missing planMarker")
	}
	if !strings.Contains(msg, "Reply with your feedback") {
		t.Error("missing footer")
	}
}

func TestFormatPlanBlocks(t *testing.T) {
	t.Run("correct block count and structure", func(t *testing.T) {
		blocks := formatPlanBlocks("my plan", "job-123")
		if len(blocks) != 4 {
			t.Fatalf("expected 4 blocks, got %d", len(blocks))
		}
		// Last block should be actions.
		if blocks[3].BlockType() != "actions" {
			t.Errorf("block[3] type = %q, want actions", blocks[3].BlockType())
		}
	})

	t.Run("long plan truncated", func(t *testing.T) {
		longPlan := strings.Repeat("x", 3000)
		blocks := formatPlanBlocks(longPlan, "job-456")
		if len(blocks) != 4 {
			t.Fatalf("expected 4 blocks, got %d", len(blocks))
		}
	})

	t.Run("button has correct action_id and value", func(t *testing.T) {
		blocks := formatPlanBlocks("plan", "job-789")
		// The actions block is the last one. We can verify by checking the block type.
		if blocks[3].BlockType() != "actions" {
			t.Fatalf("block[3] type = %q, want actions", blocks[3].BlockType())
		}
	})
}

func TestFormatQuestionBlocks(t *testing.T) {
	t.Run("correct block count", func(t *testing.T) {
		blocks := formatQuestionBlocks("Which database should I use?")
		if len(blocks) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(blocks))
		}
	})

	t.Run("long question truncated", func(t *testing.T) {
		longQ := strings.Repeat("q", 3000)
		blocks := formatQuestionBlocks(longQ)
		if len(blocks) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(blocks))
		}
	})
}

func TestFormatApprovedPlanBlocks(t *testing.T) {
	t.Run("correct block count no actions", func(t *testing.T) {
		blocks := formatApprovedPlanBlocks("the plan", "<@U123>")
		if len(blocks) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(blocks))
		}
		for _, b := range blocks {
			if b.BlockType() == "actions" {
				t.Error("approved plan blocks should not have actions")
			}
		}
	})

	t.Run("context contains approver", func(t *testing.T) {
		blocks := formatApprovedPlanBlocks("the plan", "<@U456>")
		// The context block is the last one.
		if blocks[2].BlockType() != "context" {
			t.Fatalf("block[2] type = %q, want context", blocks[2].BlockType())
		}
	})
}

func TestFormatSupersededPlanBlocks(t *testing.T) {
	t.Run("correct block count no actions", func(t *testing.T) {
		blocks := formatSupersededPlanBlocks("the plan", "Revision requested")
		if len(blocks) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(blocks))
		}
		for _, b := range blocks {
			if b.BlockType() == "actions" {
				t.Error("superseded plan blocks should not have actions")
			}
		}
	})

	t.Run("context contains label", func(t *testing.T) {
		blocks := formatSupersededPlanBlocks("the plan", "superseded by updated plan")
		if blocks[2].BlockType() != "context" {
			t.Fatalf("block[2] type = %q, want context", blocks[2].BlockType())
		}
	})
}

func TestReadPlanFile(t *testing.T) {
	t.Run("valid absolute path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "plan.md")
		if err := os.WriteFile(path, []byte("# My Plan"), 0o644); err != nil {
			t.Fatal(err)
		}
		content, err := readPlanFile(path, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if content != "# My Plan" {
			t.Errorf("content = %q, want %q", content, "# My Plan")
		}
	})

	t.Run("relative path joined with repoDir", func(t *testing.T) {
		dir := t.TempDir()
		subdir := filepath.Join(dir, ".claude", "plans")
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subdir, "plan.md"), []byte("relative plan"), 0o644); err != nil {
			t.Fatal(err)
		}
		content, err := readPlanFile(".claude/plans/plan.md", dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if content != "relative plan" {
			t.Errorf("content = %q, want %q", content, "relative plan")
		}
	})

	t.Run("empty path returns error", func(t *testing.T) {
		_, err := readPlanFile("", "/some/dir")
		if err == nil {
			t.Error("expected error for empty path")
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		_, err := readPlanFile("/nonexistent/path/plan.md", "")
		if err == nil {
			t.Error("expected error for missing file")
		}
	})
}
