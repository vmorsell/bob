package main

import (
	"context"
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
