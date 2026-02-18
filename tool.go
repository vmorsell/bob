package main

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tool bridges a tool definition with its execution logic.
type Tool struct {
	Name        string
	Description string
	Schema      anthropic.ToolInputSchemaParam
	Execute     func(ctx context.Context, input json.RawMessage) (string, error)
}
