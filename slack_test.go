package main

import (
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestIsApprovalText(t *testing.T) {
	valid := []string{
		"go", "Go", "GO", " go ", "lgtm", "LGTM", "approved", "ship it", "looks good", "yes", "approve",
	}
	for _, s := range valid {
		if !isApprovalText(s) {
			t.Errorf("expected %q to be approval text", s)
		}
	}

	invalid := []string{
		"", "go ahead", "nope", "approve this", "lgtm!", "no", "maybe",
	}
	for _, s := range invalid {
		if isApprovalText(s) {
			t.Errorf("expected %q to NOT be approval text", s)
		}
	}
}

func TestStripMention(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"single mention with text", "<@U12345> hello", "hello"},
		{"multiple mentions", "<@U12345> <@U67890> hello", "hello"},
		{"no mention", "hello world", "hello world"},
		{"empty string", "", ""},
		{"mention only", "<@U12345>", ""},
		{"mention in middle", "hello <@U12345> world", "hello world"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMention(tt.in)
			if got != tt.want {
				t.Errorf("stripMention(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestThreadToMessages(t *testing.T) {
	botUserID := "UBOT"

	t.Run("user and bot messages", func(t *testing.T) {
		replies := []slack.Message{
			{Msg: slack.Msg{Text: "<@UBOT> do something", User: "UHUMAN"}},
			{Msg: slack.Msg{Text: "Sure, working on it", User: "UBOT"}},
			{Msg: slack.Msg{Text: "thanks", User: "UHUMAN"}},
		}
		msgs := threadToMessages(replies, botUserID)
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
		if msgs[0].Role != RoleUser {
			t.Errorf("msg[0] role = %q, want %q", msgs[0].Role, RoleUser)
		}
		if msgs[1].Role != RoleAssistant {
			t.Errorf("msg[1] role = %q, want %q", msgs[1].Role, RoleAssistant)
		}
		if msgs[2].Role != RoleUser {
			t.Errorf("msg[2] role = %q, want %q", msgs[2].Role, RoleUser)
		}
	})

	t.Run("empty text after strip skipped", func(t *testing.T) {
		replies := []slack.Message{
			{Msg: slack.Msg{Text: "<@UBOT>", User: "UHUMAN"}},
			{Msg: slack.Msg{Text: "real message", User: "UHUMAN"}},
		}
		msgs := threadToMessages(replies, botUserID)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if msgs[0].Content != "real message" {
			t.Errorf("content = %q, want %q", msgs[0].Content, "real message")
		}
	})

	t.Run("nil input", func(t *testing.T) {
		msgs := threadToMessages(nil, botUserID)
		if msgs != nil {
			t.Errorf("expected nil, got %v", msgs)
		}
	})
}

func TestEventDedupIsDuplicate(t *testing.T) {
	d := &eventDedup{seen: make(map[string]time.Time)}

	t.Run("first call not duplicate", func(t *testing.T) {
		if d.isDuplicate("ts1") {
			t.Error("first call should not be duplicate")
		}
	})

	t.Run("same key is duplicate", func(t *testing.T) {
		if !d.isDuplicate("ts1") {
			t.Error("second call with same key should be duplicate")
		}
	})

	t.Run("different key not duplicate", func(t *testing.T) {
		if d.isDuplicate("ts2") {
			t.Error("different key should not be duplicate")
		}
	})
}
