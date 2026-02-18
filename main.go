package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	signingSecret := os.Getenv("SLACK_SIGNING_SECRET")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	githubToken := os.Getenv("GITHUB_TOKEN")
	githubOwner := os.Getenv("GITHUB_OWNER")
	if githubOwner == "" {
		githubOwner = os.Getenv("GITHUB_ORG") // backwards compat
	}

	if botToken == "" || signingSecret == "" || anthropicKey == "" {
		log.Fatal("SLACK_BOT_TOKEN, SLACK_SIGNING_SECRET, and ANTHROPIC_API_KEY must be set")
	}
	if githubToken == "" || githubOwner == "" {
		log.Fatal("GITHUB_TOKEN and GITHUB_OWNER must be set")
	}

	tools := []Tool{
		ListReposTool(githubOwner, githubToken),
		CloneRepoTool(githubOwner, githubToken),
	}

	llm := NewAnthropicLLM(anthropicKey, tools)

	mux := http.NewServeMux()
	mux.Handle("/webhooks/slack", NewSlackHandler(botToken, signingSecret, llm))

	log.Println("Bob listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
