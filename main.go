package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/slack-go/slack"
)

func main() {
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	signingSecret := os.Getenv("SLACK_SIGNING_SECRET")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	githubToken := os.Getenv("GITHUB_TOKEN")
	githubOwner := os.Getenv("GITHUB_OWNER")
	claudeCodeToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
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
		ImplementChangesTool(claudeCodeToken, notifier),
		CreatePullRequestTool(githubOwner, githubToken),
	}

	onJobStart := func(ctx context.Context, jobID string) {
		notifier.Notify(ctx, fmt.Sprintf("On it! Job ID: %s", jobID))
	}

	llm := NewAnthropicLLM(anthropicKey, tools, hub, onJobStart)

	mux := http.NewServeMux()
	mux.Handle("/webhooks/slack", NewSlackHandler(slackClient, signingSecret, llm, hub))
	mux.HandleFunc("/events", hub.ServeSSE)
	mux.HandleFunc("/api/jobs/", hub.ServeJobAPI)
	mux.HandleFunc("/api/jobs", hub.ServeJobList)
	mux.HandleFunc("/jobs/", serveUI)
	mux.HandleFunc("/", serveUI)

	log.Println("Bob listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
