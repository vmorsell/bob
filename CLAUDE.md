# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Bob

Bob is an LLM helper for a software team, integrated with Slack via the Events API. He runs as a containerized Go service behind a Cloudflare tunnel. When mentioned, Bob uses an LLM to generate responses, with full thread context when mentioned in a thread.

## Build and run

```bash
# Build and run with Docker Compose (includes cloudflared tunnel)
docker compose up --build

# Build Go binary locally
go build -o bob .
```

There are no tests or linting configured yet.

## Architecture

Two-service Docker Compose setup:

- **bob** — Go HTTP server on `:8080` handling Slack webhooks
- **cloudflared** — Cloudflare tunnel routing your tunnel domain → `http://bob:8080`

Go code is split by webhook source for extensibility:

- `main.go` — HTTP mux, routes mounted at `/webhooks/<source>`, wires up dependencies
- `slack.go` — Slack handler: signature verification, `url_verification` challenge, `app_mention` event handling with thread context
- `llm.go` — `LLM` interface and `Message`/`Role` types (adapter pattern)
- `anthropic.go` — Anthropic adapter implementing `LLM` using Claude Sonnet via `anthropic-sdk-go`

New webhook sources (Linear, GitHub, etc.) get their own `<source>.go` file and `/webhooks/<source>` route.

New LLM providers get their own `<provider>.go` file implementing the `LLM` interface.

## Environment variables

Defined in `.env` (gitignored), passed to containers via `compose.yaml`:

- `SLACK_BOT_TOKEN` — Bot User OAuth Token (`xoxb-...`)
- `SLACK_SIGNING_SECRET` — For verifying incoming Slack requests
- `ANTHROPIC_API_KEY` — Anthropic API key for LLM responses
- `CLOUDFLARED_TOKEN` — Cloudflare tunnel token
