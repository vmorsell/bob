package main

import (
	"context"
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
	apiToken := os.Getenv("BOB_API_TOKEN")
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
	if apiToken == "" {
		log.Fatal("BOB_API_TOKEN must be set")
	}

	slackClient := slack.New(botToken)

	// Resolve bot user ID once at startup.
	authResp, err := slackClient.AuthTest()
	if err != nil {
		log.Fatalf("slack auth test failed: %v", err)
	}
	botUserID := authResp.UserID
	log.Printf("Bot user ID: %s", botUserID)

	hub := NewHub("/workspace/.bob")

	allowedRepos := parseAllowedRepos(os.Getenv("ALLOWED_REPOS"))
	if allowedRepos != nil {
		log.Printf("Repo allowlist active: %v", allowedRepos)
	}

	orch := NewOrchestrator(anthropicKey, githubOwner, githubToken, claudeCodeToken, hub, allowedRepos)

	maxPerMinute := 15.0
	if v := os.Getenv("MAX_INBOUND_MESSAGES_PER_MIN"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			maxPerMinute = parsed
		}
	}

	approver := NewApprover(slackClient, hub, orch)

	mux := http.NewServeMux()
	mux.Handle("/webhooks/slack", NewSlackHandler(slackClient, signingSecret, orch, hub, botUserID, approver, bobURL, apiToken, maxPerMinute))
	mux.Handle("/webhooks/slack/interactions", NewSlackInteractionHandler(slackClient, signingSecret, approver))
	mux.Handle("/webhooks/slack/commands", NewSlashCommandHandler(signingSecret, hub))
	mux.Handle("/events", requireAuthFunc(apiToken, hub.ServeSSE))
	mux.Handle("/api/jobs/", requireAuth(apiToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// POST /api/jobs/{id}/approve — web UI approval endpoint.
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/approve") {
			path := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
			jobID := strings.TrimSuffix(path, "/approve")
			if jobID == "" {
				http.Error(w, `{"error":"missing job id"}`, http.StatusBadRequest)
				return
			}

			state, ok := hub.GetJobState(jobID)
			if !ok || state.Channel == "" || state.ThreadTS == "" {
				http.Error(w, `{"error":"job not found or missing Slack thread info"}`, http.StatusNotFound)
				return
			}

			go approver.Approve(context.Background(), jobID, state.Channel, state.ThreadTS, "web UI")

			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
			return
		}
		hub.ServeJobAPI(w, r)
	})))
	mux.Handle("/api/jobs", requireAuthFunc(apiToken, hub.ServeJobList))
	mux.Handle("/api/stats", requireAuthFunc(apiToken, hub.ServeStats))
	ui := serveUI()
	mux.Handle("/assets/", ui)
	mux.Handle("/jobs/", ui)
	mux.Handle("/", ui)

	log.Println("Bob listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
