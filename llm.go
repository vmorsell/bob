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

type LLM interface {
	Respond(ctx context.Context, messages []Message) (string, error)
}
