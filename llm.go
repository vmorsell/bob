package main

import "context"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role
	Content string
}

type LLMResponse struct {
	Text  string // text reply (used for non-job responses like clarifying questions)
	IsJob bool   // true if a monitoring job was started
	PRURL string // set if create_pull_request succeeded
}

type LLM interface {
	Respond(ctx context.Context, messages []Message) (LLMResponse, error)
}
