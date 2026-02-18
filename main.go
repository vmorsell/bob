package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	signingSecret := os.Getenv("SLACK_SIGNING_SECRET")

	if botToken == "" || signingSecret == "" {
		log.Fatal("SLACK_BOT_TOKEN and SLACK_SIGNING_SECRET must be set")
	}

	mux := http.NewServeMux()
	mux.Handle("/webhooks/slack", NewSlackHandler(botToken, signingSecret))

	log.Println("Bob listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
