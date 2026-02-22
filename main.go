package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
)

func main() {
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	signingSecret := os.Getenv("SLACK_SIGNING_SECRET")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	githubToken := os.Getenv("GITHUB_TOKEN")
	githubOwner := os.Getenv("GITHUB_OWNER")
	claudeCodeToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	bobURL := os.Getenv("BOB_URL") // e.g. https://bob.example.com
	if githubOwner == "" {
		githubOwner = os.Getenv("GITHUB_ORG") // backwards compat
	}

	if botToken == "" || signingSecret == "" || anthropicKey == "" {
		log.Fatal("SLACK_BOT_TOKEN, SLACK_SIGNING_SECRET, and ANTHROPIC_API_KEY must be set")
	}
	if githubToken == "" || githubOwner == "" {
		log.Fatal("GITHUB_TOKEN and GITHUB_OWNER must be set")
	}
	if claudeCodeToken == "" {
		log.Fatal("CLAUDE_CODE_OAUTH_TOKEN must be set")
	}

	slackClient := slack.New(botToken)
	notifier := NewSlackNotifier(slackClient)

	// Resolve bot user ID once at startup.
	authResp, err := slackClient.AuthTest()
	if err != nil {
		log.Fatalf("slack auth test failed: %v", err)
	}
	botUserID := authResp.UserID
	log.Printf("Bot user ID: %s", botUserID)

	hub := NewHub("/workspace/.bob")

	onJobStart := func(ctx context.Context, jobID, phase string) {
		var msg string
		switch phase {
		case "planning":
			msg = "Working on a plan..."
			if bobURL != "" {
				msg = fmt.Sprintf("Working on a plan... Follow my progress here: <%s/jobs/%s>", bobURL, jobID)
			}
		case "implementation":
			msg = "Implementing the approved plan..."
			if bobURL != "" {
				msg = fmt.Sprintf("Implementing the approved plan... Follow my progress here: <%s/jobs/%s>", bobURL, jobID)
			}
		default:
			msg = "On it!"
			if bobURL != "" {
				msg = fmt.Sprintf("On it! Follow my progress here: <%s/jobs/%s>", bobURL, jobID)
			}
		}
		notifier.Notify(ctx, msg)
	}

	orch := NewOrchestrator(anthropicKey, githubOwner, githubToken, claudeCodeToken, hub, notifier, onJobStart)

	maxPerMinute := 15.0
	if v := os.Getenv("MAX_INBOUND_MESSAGES_PER_MIN"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			maxPerMinute = parsed
		}
	}

	approver := NewApprover(slackClient, hub, orch, botUserID)

	mux := http.NewServeMux()
	mux.Handle("/webhooks/slack", NewSlackHandler(slackClient, signingSecret, orch, hub, botUserID, maxPerMinute))
	mux.Handle("/webhooks/slack/interactions", NewSlackInteractionHandler(slackClient, signingSecret, approver))
	mux.HandleFunc("/events", hub.ServeSSE)
	mux.HandleFunc("/api/jobs/", func(w http.ResponseWriter, r *http.Request) {
		// POST /api/jobs/{id}/approve — web UI approval endpoint.
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/approve") {
			path := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
			jobID := strings.TrimSuffix(path, "/approve")
			if jobID == "" {
				http.Error(w, `{"error":"missing job id"}`, http.StatusBadRequest)
				return
			}

			meta, ok := hub.GetJobMeta(jobID)
			if !ok || meta.Channel == "" || meta.ThreadTS == "" {
				http.Error(w, `{"error":"job not found or missing Slack thread info"}`, http.StatusNotFound)
				return
			}

			go approver.Approve(context.Background(), jobID, meta.Channel, meta.ThreadTS, "web UI")

			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
			return
		}
		hub.ServeJobAPI(w, r)
	})
	mux.HandleFunc("/api/jobs", hub.ServeJobList)
	mux.HandleFunc("/api/stats", hub.ServeStats)
	mux.HandleFunc("/jobs/", serveUI)
	mux.HandleFunc("/", serveUI)

	log.Println("Bob listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
