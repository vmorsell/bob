package main

import "testing"

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
