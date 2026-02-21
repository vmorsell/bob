# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Bob

Bob is an LLM helper for a software team, integrated with Slack via the Events API. He runs as a containerized Go service behind a Cloudflare tunnel. When mentioned, Bob parses the intent of the request with a single Claude Haiku call, then drives a deterministic workflow to implement code changes and create pull requests.

## Build and run

```bash
# Build and run with Docker Compose (includes cloudflared tunnel)
docker compose up --build

# Build Go binary locally
go build -o bob .
```

## Architecture

Two-service Docker Compose setup:

- **bob** — Go HTTP server on `:8080` handling Slack webhooks
- **cloudflared** — Cloudflare tunnel routing your tunnel domain → `http://bob:8080`

A named `workspace` volume is mounted at `/workspace` for persistent repo clones across restarts.

Go code is organized by concern:

- `main.go` — HTTP mux, routes mounted at `/webhooks/<source>` and `/jobs/`, `/api/jobs`, `/events`; wires up dependencies
- `slack.go` — Slack handler: signature verification, `url_verification` challenge, `app_mention` event handling with thread context
- `llm.go` — `Message`/`Role` types (used by intent parser and slack thread parsing)
- `intent.go` — `ParseIntent`: single Claude Haiku call that extracts `{Repo, Task}` or `{Question}` from a Slack conversation
- `orchestrator.go` — `Orchestrator`: deterministic state machine driving verify → clone → implement → PR; calls `ParseIntent`, then `FindRepo`, `CloneRepo`, `ImplementChanges`, `CreatePullRequest` in sequence
- `git.go` — Plain functions: `FindRepo` (GitHub REST API), `CloneRepo` (shallow clone to `/workspace`), `CreatePullRequest` (commit + push + GitHub API)
- `claudecode.go` — `ImplementChanges`: runs Claude Code CLI, parses `TerminalState` JSON from output; `claudeStreamParser` for streaming JSON events
- `util.go` — `truncate` helper
- `notify.go` — `SlackNotifier` for posting mid-execution messages; context keys for channel, threadTS, jobID, and hub
- `monitor.go` — `Hub` (SSE fan-out + JSONL persistence), event types, `streamingWriter`, REST handlers (`/api/jobs`, `/api/jobs/{id}`), SSE handler (`/events`), and dark-terminal web UI served at `/` and `/jobs/{id}`

### Orchestration pattern

Every Slack mention triggers one Claude Haiku call (`ParseIntent`) that returns either a `{Repo, Task}` pair or a `{Question}` for clarification. If a task is identified, the `Orchestrator` runs a deterministic sequence — no LLM involvement after intent parsing:

1. `FindRepo` — verify repo exists via GitHub API; reply + stop if not found
2. Create monitoring job, post "On it!" to Slack
3. `CloneRepo` — shallow clone to `/workspace`
4. `ImplementChanges` — run Claude Code CLI; parses `TerminalState` from output
5. Switch on `TerminalState.Status`:
   - `completed` → `CreatePullRequest` → reply with PR URL
   - `needs_information` → relay Claude Code's question to Slack
   - `error` → relay error to Slack

### Terminal state protocol

`ImplementChanges` appends to every task prompt:

```
At the very end of your work, output a single JSON line (no code block):
{"status":"completed","message":"Brief summary of what was done"}
or {"status":"needs_information","message":"Specific question"}
or {"status":"error","message":"What went wrong"}
```

`claudeStreamParser` scans every assistant text block for this JSON pattern, captures it as `TerminalState`, and suppresses it from Slack notifications. If absent (e.g. Claude Code crashed), `ImplementChanges` falls back to checking `git status`.

### Multi-turn clarification

Every Slack mention re-runs intent parsing on the full thread. Haiku sees the full conversation (including any prior "which repo?" exchange) and naturally extracts the now-complete intent. No state tracking needed — the thread is the state.

### Monitoring pattern

When a job starts (step 2 above), a UUID job ID is created and all subsequent tool calls and Claude Code output lines are emitted as `Event` values:
1. Persisted to `/workspace/.bob/{jobID}.jsonl` (one JSON line per event)
2. Fanned out to any connected SSE clients (`/events?job={id}`)

The web UI at the tunnel root lists all jobs; `/jobs/{id}` shows the live event stream. Clarification responses (no job started) produce no job entry.

### Notifier pattern

`SlackNotifier` wraps the Slack client and reads channel/threadTS from context (injected by `slack.go`). `ImplementChanges` passes it to `claudeStreamParser`, which calls `notifier.Notify(ctx, text)` for each assistant text block (minus the terminal state JSON). It also emits `EventSlackNotification` to the hub. It no-ops if context values are missing.

New webhook sources (Linear, GitHub, etc.) get their own `<source>.go` file and `/webhooks/<source>` route.
