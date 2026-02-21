package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

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

	hub := NewHub("/workspace/.bob")

	tools := []Tool{
		ListReposTool(githubOwner, githubToken),
		CloneRepoTool(githubOwner, githubToken),
		ImplementChangesTool(githubOwner, claudeCodeToken, notifier),
		RunTestsTool(githubOwner),
		CreatePullRequestTool(githubOwner, githubToken),
	}

	onJobStart := func(ctx context.Context, jobID string) {
		msg := "On it!"
		if bobURL != "" {
			msg = fmt.Sprintf("On it! Follow my progress here: <%s/jobs/%s>", bobURL, jobID)
		}
		notifier.Notify(ctx, msg)
	}

	llm := NewAnthropicLLM(anthropicKey, tools, hub, onJobStart)

	maxPerMinute := 15.0
	if v := os.Getenv("MAX_INBOUND_MESSAGES_PER_MIN"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			maxPerMinute = parsed
		}
	}

	mux := http.NewServeMux()
	mux.Handle("/webhooks/slack", NewSlackHandler(slackClient, signingSecret, llm, hub, maxPerMinute))
	mux.HandleFunc("/events", hub.ServeSSE)
	mux.HandleFunc("/api/jobs/", hub.ServeJobAPI)
	mux.HandleFunc("/api/jobs", hub.ServeJobList)
	mux.HandleFunc("/api/stats", hub.ServeStats)
	mux.HandleFunc("/jobs/", serveUI)
	mux.HandleFunc("/", serveUI)

	log.Println("Bob listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
