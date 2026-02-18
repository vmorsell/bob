package main

import (
	"context"
	"log"

	"github.com/slack-go/slack"
)

type ctxKey int

const (
	ctxKeyChannel  ctxKey = iota
	ctxKeyThreadTS
)

// WithSlackThread returns a context carrying the Slack channel and thread timestamp.
func WithSlackThread(ctx context.Context, channel, threadTS string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyChannel, channel)
	ctx = context.WithValue(ctx, ctxKeyThreadTS, threadTS)
	return ctx
}

// SlackNotifier lets tools post messages to the originating Slack thread.
type SlackNotifier struct {
	client *slack.Client
}

func NewSlackNotifier(client *slack.Client) *SlackNotifier {
	return &SlackNotifier{client: client}
}

// Notify posts a message to the Slack thread stored in ctx.
// It silently no-ops if the context values are missing.
func (n *SlackNotifier) Notify(ctx context.Context, text string) {
	channel, _ := ctx.Value(ctxKeyChannel).(string)
	threadTS, _ := ctx.Value(ctxKeyThreadTS).(string)
	if channel == "" || threadTS == "" {
		return
	}

	_, _, err := n.client.PostMessage(channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("notifier: failed to post message: %v", err)
	}
}
