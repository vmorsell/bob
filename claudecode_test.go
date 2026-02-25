package main

import (
	"encoding/json"
	"sync/atomic"
	"testing"
)

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeLines(sp *claudeStreamParser, lines ...string) {
	for _, line := range lines {
		sp.Write([]byte(line + "\n"))
	}
}

func TestStreamParser_SystemInit(t *testing.T) {
	t.Run("valid init captures sessionID", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":       "system",
			"subtype":    "init",
			"session_id": "sess-123",
		}))
		r := sp.result()
		if r.SessionID != "sess-123" {
			t.Errorf("sessionID = %q, want %q", r.SessionID, "sess-123")
		}
	})

	t.Run("wrong subtype ignored", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":       "system",
			"subtype":    "heartbeat",
			"session_id": "sess-456",
		}))
		r := sp.result()
		if r.SessionID != "" {
			t.Errorf("sessionID = %q, want empty", r.SessionID)
		}
	})

	t.Run("empty session_id ignored", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":       "system",
			"subtype":    "init",
			"session_id": "",
		}))
		r := sp.result()
		if r.SessionID != "" {
			t.Errorf("sessionID = %q, want empty", r.SessionID)
		}
	})
}

func TestStreamParser_AskUserQuestion(t *testing.T) {
	t.Run("main agent captures question", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "tool_use",
						"name": "AskUserQuestion",
						"id":   "tu-1",
						"input": map[string]any{
							"questions": []map[string]any{
								{"question": "Which DB?"},
							},
						},
					},
				},
			},
		}))
		r := sp.result()
		if r.Question != "Which DB?" {
			t.Errorf("question = %q, want %q", r.Question, "Which DB?")
		}
		if !sp.suppressResultIDs["tu-1"] {
			t.Error("expected tu-1 in suppressResultIDs")
		}
	})

	t.Run("sub-agent ignored", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "parent-1",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "tool_use",
						"name": "AskUserQuestion",
						"id":   "tu-2",
						"input": map[string]any{
							"questions": []map[string]any{
								{"question": "ignored"},
							},
						},
					},
				},
			},
		}))
		r := sp.result()
		if r.Question != "" {
			t.Errorf("question = %q, want empty (sub-agent)", r.Question)
		}
	})

	t.Run("cancelOnQuestion invoked", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		var called atomic.Bool
		sp.cancelOnQuestion = func() { called.Store(true) }
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "tool_use",
						"name": "AskUserQuestion",
						"id":   "tu-3",
						"input": map[string]any{
							"questions": []map[string]any{
								{"question": "q?"},
							},
						},
					},
				},
			},
		}))
		if !called.Load() {
			t.Error("cancelOnQuestion was not called")
		}
	})

	t.Run("malformed input no crash", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type":  "tool_use",
						"name":  "AskUserQuestion",
						"id":    "tu-4",
						"input": "not-an-object",
					},
				},
			},
		}))
		r := sp.result()
		if r.Question != "" {
			t.Errorf("question = %q, want empty", r.Question)
		}
	})
}

func TestStreamParser_ExitPlanMode(t *testing.T) {
	t.Run("main agent sets planExited", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type":  "tool_use",
						"name":  "ExitPlanMode",
						"id":    "tu-5",
						"input": map[string]any{},
					},
				},
			},
		}))
		r := sp.result()
		if !r.PlanExited {
			t.Error("planExited should be true")
		}
		if !sp.suppressResultIDs["tu-5"] {
			t.Error("expected tu-5 in suppressResultIDs")
		}
	})

	t.Run("sub-agent does not set planExited", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "parent-1",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type":  "tool_use",
						"name":  "ExitPlanMode",
						"id":    "tu-6",
						"input": map[string]any{},
					},
				},
			},
		}))
		r := sp.result()
		if r.PlanExited {
			t.Error("planExited should be false for sub-agent")
		}
	})
}

func TestStreamParser_WritePlanFile(t *testing.T) {
	t.Run("path with .claude/plans/ captured", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "tool_use",
						"name": "Write",
						"id":   "tu-w1",
						"input": map[string]any{
							"file_path": "/workspace/repo/.claude/plans/plan.md",
						},
					},
				},
			},
		}))
		r := sp.result()
		if r.PlanFilePath != "/workspace/repo/.claude/plans/plan.md" {
			t.Errorf("planFilePath = %q, want the .claude/plans/ path", r.PlanFilePath)
		}
	})

	t.Run("path without .claude/plans/ not captured", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "tool_use",
						"name": "Write",
						"id":   "tu-w2",
						"input": map[string]any{
							"file_path": "/workspace/repo/src/main.go",
						},
					},
				},
			},
		}))
		r := sp.result()
		if r.PlanFilePath != "" {
			t.Errorf("planFilePath = %q, want empty", r.PlanFilePath)
		}
	})

	t.Run("malformed Write input no crash", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type":  "tool_use",
						"name":  "Write",
						"id":    "tu-w3",
						"input": 42,
					},
				},
			},
		}))
		r := sp.result()
		if r.PlanFilePath != "" {
			t.Errorf("planFilePath = %q, want empty", r.PlanFilePath)
		}
	})
}

func TestStreamParser_TaskTracking(t *testing.T) {
	t.Run("Task tool_use tracked and result removes it", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		// Task tool_use (always tracked regardless of parent)
		writeLines(sp, mustJSON(map[string]any{
			"type":               "assistant",
			"parent_tool_use_id": "",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "tool_use",
						"name": "Task",
						"id":   "task-1",
						"input": map[string]any{
							"description": "explore codebase",
						},
					},
				},
			},
		}))
		if _, ok := sp.pendingTaskDescs["task-1"]; !ok {
			t.Fatal("expected task-1 in pendingTaskDescs")
		}

		// Matching tool_result removes it.
		writeLines(sp, mustJSON(map[string]any{
			"type": "user",
			"message": map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": "task-1",
						"content":     "done",
					},
				},
			},
		}))
		if _, ok := sp.pendingTaskDescs["task-1"]; ok {
			t.Error("expected task-1 to be removed from pendingTaskDescs")
		}
	})
}

func TestStreamParser_SuppressedResults(t *testing.T) {
	t.Run("suppressed ID error skipped", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		sp.suppressResultIDs["tu-x"] = true

		writeLines(sp, mustJSON(map[string]any{
			"type": "user",
			"message": map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": "tu-x",
						"content":     "error: user denied",
						"is_error":    true,
					},
				},
			},
		}))
		// After processing, the suppressed ID should be consumed.
		if sp.suppressResultIDs["tu-x"] {
			t.Error("expected tu-x to be consumed from suppressResultIDs")
		}
	})

	t.Run("unknown ID not suppressed", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		// No IDs in suppressResultIDs — processing a user error result should not crash.
		writeLines(sp, mustJSON(map[string]any{
			"type": "user",
			"message": map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": "tu-unknown",
						"content":     "real error",
						"is_error":    true,
					},
				},
			},
		}))
		// Just verifying no panic.
	})
}

func TestStreamParser_Result(t *testing.T) {
	t.Run("result text captured", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":   "result",
			"result": "All changes applied.",
		}))
		r := sp.result()
		if r.ResultText != "All changes applied." {
			t.Errorf("resultText = %q, want %q", r.ResultText, "All changes applied.")
		}
		if r.IsError {
			t.Error("isError should be false")
		}
	})

	t.Run("error result", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":    "result",
			"subtype": "error",
			"error":   "context deadline exceeded",
		}))
		r := sp.result()
		if r.ResultText != "context deadline exceeded" {
			t.Errorf("resultText = %q, want error message", r.ResultText)
		}
		if !r.IsError {
			t.Error("isError should be true")
		}
	})

	t.Run("non-error result", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type":    "result",
			"subtype": "success",
			"result":  "done",
		}))
		r := sp.result()
		if r.ResultText != "done" {
			t.Errorf("resultText = %q, want %q", r.ResultText, "done")
		}
		if r.IsError {
			t.Error("isError should be false")
		}
	})
}

func TestStreamParser_EdgeCases(t *testing.T) {
	t.Run("empty line skipped", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, "", "  ", "\t")
		r := sp.result()
		if r.SessionID != "" || r.Question != "" || r.PlanExited || r.ResultText != "" {
			t.Error("empty lines should produce no result")
		}
	})

	t.Run("non-JSON no crash", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, "this is stderr noise", "Warning: something happened")
		r := sp.result()
		if r.SessionID != "" {
			t.Errorf("sessionID = %q, want empty", r.SessionID)
		}
	})

	t.Run("rate_limit_event no-op", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp, mustJSON(map[string]any{
			"type": "rate_limit_event",
		}))
		r := sp.result()
		if r.SessionID != "" || r.IsError {
			t.Error("rate_limit_event should be a no-op")
		}
	})

	t.Run("partial line buffering", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		line := mustJSON(map[string]any{
			"type":       "system",
			"subtype":    "init",
			"session_id": "sess-buf",
		})
		// Write in two chunks.
		half := len(line) / 2
		sp.Write([]byte(line[:half]))
		sp.Write([]byte(line[half:] + "\n"))

		r := sp.result()
		if r.SessionID != "sess-buf" {
			t.Errorf("sessionID = %q, want %q", r.SessionID, "sess-buf")
		}
	})
}

func TestStreamParser_MultipleEvents(t *testing.T) {
	t.Run("init + ExitPlanMode + result sequence", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp,
			mustJSON(map[string]any{
				"type":       "system",
				"subtype":    "init",
				"session_id": "sess-multi",
			}),
			mustJSON(map[string]any{
				"type":               "assistant",
				"parent_tool_use_id": "",
				"message": map[string]any{
					"role": "assistant",
					"content": []map[string]any{
						{
							"type":  "tool_use",
							"name":  "ExitPlanMode",
							"id":    "tu-ep",
							"input": map[string]any{},
						},
					},
				},
			}),
			mustJSON(map[string]any{
				"type":   "result",
				"result": "Plan complete.",
			}),
		)

		r := sp.result()
		if r.SessionID != "sess-multi" {
			t.Errorf("sessionID = %q, want %q", r.SessionID, "sess-multi")
		}
		if !r.PlanExited {
			t.Error("planExited should be true")
		}
		if r.ResultText != "Plan complete." {
			t.Errorf("resultText = %q, want %q", r.ResultText, "Plan complete.")
		}
	})

	t.Run("multiple Write calls last one wins", func(t *testing.T) {
		sp := newClaudeStreamParser(nil, "")
		writeLines(sp,
			mustJSON(map[string]any{
				"type":               "assistant",
				"parent_tool_use_id": "",
				"message": map[string]any{
					"role": "assistant",
					"content": []map[string]any{
						{
							"type": "tool_use",
							"name": "Write",
							"id":   "w1",
							"input": map[string]any{
								"file_path": "/workspace/repo/.claude/plans/draft.md",
							},
						},
					},
				},
			}),
			mustJSON(map[string]any{
				"type":               "assistant",
				"parent_tool_use_id": "",
				"message": map[string]any{
					"role": "assistant",
					"content": []map[string]any{
						{
							"type": "tool_use",
							"name": "Write",
							"id":   "w2",
							"input": map[string]any{
								"file_path": "/workspace/repo/.claude/plans/final.md",
							},
						},
					},
				},
			}),
		)
		r := sp.result()
		if r.PlanFilePath != "/workspace/repo/.claude/plans/final.md" {
			t.Errorf("planFilePath = %q, want final.md path", r.PlanFilePath)
		}
	})
}
