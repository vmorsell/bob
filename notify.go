package main

import (
	"context"
	"log"

	"github.com/slack-go/slack"
)

type ctxKey int

const (
	ctxKeyChannel   ctxKey = iota
	ctxKeyThreadTS  ctxKey = iota
	ctxKeyJobID     ctxKey = iota
	ctxKeyHub       ctxKey = iota
	ctxKeyMentionTS ctxKey = iota
)

// WithSlackThread returns a context carrying the Slack channel and thread timestamp.
func WithSlackThread(ctx context.Context, channel, threadTS string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyChannel, channel)
	ctx = context.WithValue(ctx, ctxKeyThreadTS, threadTS)
	return ctx
}

// WithJobID returns a context carrying the monitoring job ID.
func WithJobID(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, ctxKeyJobID, jobID)
}

// JobIDFromCtx extracts the job ID from the context.
func JobIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyJobID).(string)
	return v
}

// WithMentionTS returns a context carrying the timestamp of the Slack message
// that triggered the mention (where the "working" reaction lives).
func WithMentionTS(ctx context.Context, ts string) context.Context {
	return context.WithValue(ctx, ctxKeyMentionTS, ts)
}

// WithHub returns a context carrying the monitoring Hub.
func WithHub(ctx context.Context, hub *Hub) context.Context {
	return context.WithValue(ctx, ctxKeyHub, hub)
}

// HubFromCtx extracts the Hub from the context.
func HubFromCtx(ctx context.Context) *Hub {
	v, _ := ctx.Value(ctxKeyHub).(*Hub)
	return v
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

	// Remove the "working" reaction before the first reply appears.
	if mentionTS, _ := ctx.Value(ctxKeyMentionTS).(string); mentionTS != "" {
		removeReaction(n.client, channel, mentionTS)
	}

	// Emit monitoring event before posting.
	hub := HubFromCtx(ctx)
	jobID := JobIDFromCtx(ctx)
	hub.Emit(jobID, EventSlackNotification, map[string]any{"text": text})

	_, _, err := n.client.PostMessage(channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("notifier: failed to post message: %v", err)
	}
}
