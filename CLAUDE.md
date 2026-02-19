# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Bob

Bob is an LLM helper for a software team, integrated with Slack via the Events API. He runs as a containerized Go service behind a Cloudflare tunnel. When mentioned, Bob uses an LLM to generate responses, with full thread context when mentioned in a thread. Bob can interact with the team's GitHub organization — searching for repositories, cloning them, implementing code changes via Claude Code CLI, and creating pull requests.

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

A named `workspace` volume is mounted at `/workspace` for persistent repo clones across restarts.

Go code is split by webhook source for extensibility:

- `main.go` — HTTP mux, routes mounted at `/webhooks/<source>` and `/jobs/`, `/api/jobs`, `/events`; wires up dependencies and tools
- `slack.go` — Slack handler: signature verification, `url_verification` challenge, `app_mention` event handling with thread context
- `llm.go` — `LLM` interface and `Message`/`Role` types (adapter pattern)
- `anthropic.go` — Anthropic adapter implementing `LLM` using Claude Sonnet via `anthropic-sdk-go`, with tool use loop and monitoring event emission
- `tool.go` — `Tool` type bridging tool definitions and the Anthropic adapter
- `git.go` — GitHub tools: `list_repos` (search org repos via GitHub REST API) and `clone_repo` (shallow clone to `/workspace`)
- `claudecode.go` — `implement_changes` (run Claude Code CLI on a cloned repo, streaming output via `streamingWriter`) and `create_pull_request` (commit, push, open PR via GitHub API)
- `notify.go` — `SlackNotifier` for tools to post mid-execution messages; context keys for channel, threadTS, jobID, and hub
- `monitor.go` — `Hub` (SSE fan-out + JSONL persistence), event types, `streamingWriter`, REST handlers (`/api/jobs`, `/api/jobs/{id}`), SSE handler (`/events`), and dark-terminal web UI served at `/` and `/jobs/{id}`

### Tool use pattern

The Anthropic adapter accepts a `[]Tool` at construction. During `Respond`, it runs a loop (max 15 iterations): if the model returns `stop_reason=tool_use`, execute each requested tool, send results back, and loop. Tool execution errors are returned to the model as `is_error=true` results for graceful recovery.

New tools: create a constructor returning `Tool` (closing over config), register it in `main.go`.

### Monitoring pattern

On the **first `tool_use`** in `Respond()`, a UUID job ID is generated and a monitoring job is created in the `Hub`. Bob posts a Slack message with the job ID via `onJobStart`. All subsequent tool calls, LLM iterations, Claude Code output lines, and Slack notifications are emitted as `Event` values and:
1. Persisted to `/workspace/.bob/{jobID}.jsonl` (one JSON line per event)
2. Fanned out to any connected SSE clients (`/events?job={id}`)

The web UI at the tunnel root lists all jobs; `/jobs/{id}` shows the live event stream. Pure-text LLM responses (no tool use) produce no job and no Slack link.

### Notifier pattern

`SlackNotifier` wraps the Slack client and reads channel/threadTS from context (injected by `slack.go`). Tools receive it at construction and call `notifier.Notify(ctx, text)` to post progress messages mid-execution. It also emits `EventSlackNotification` to the hub if jobID is in context. It no-ops if context values are missing.

New webhook sources (Linear, GitHub, etc.) get their own `<source>.go` file and `/webhooks/<source>` route.

New LLM providers get their own `<provider>.go` file implementing the `LLM` interface.

## Environment variables

Defined in `.env` (gitignored), passed to containers via `compose.yaml`:

- `SLACK_BOT_TOKEN` — Bot User OAuth Token (`xoxb-...`)
- `SLACK_SIGNING_SECRET` — For verifying incoming Slack requests
- `ANTHROPIC_API_KEY` — Anthropic API key for LLM responses
- `GITHUB_TOKEN` — GitHub personal access token for repo access
- `GITHUB_OWNER` — GitHub owner (organization or personal username) to search/clone from
- `CLAUDE_CODE_OAUTH_TOKEN` — OAuth token for Claude Code CLI (used by `implement_changes` tool)
- `CLOUDFLARED_TOKEN` — Cloudflare tunnel token
