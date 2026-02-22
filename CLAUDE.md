# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Bob

Bob is an LLM helper for a software team, integrated with Slack via the Events API. He runs as a containerized Go service behind a Cloudflare tunnel. When mentioned, Bob parses the intent of the request with a single Claude Haiku call, then drives a deterministic plan-first workflow: he explores the codebase, presents a plan for approval, and only implements and creates a pull request after the user approves.

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

- `main.go` — HTTP mux, routes mounted at `/webhooks/<source>`, `/webhooks/slack/interactions`, `/jobs/`, `/api/jobs`, `/events`; wires up dependencies; `POST /api/jobs/{id}/approve` endpoint for web UI approval
- `slack.go` — Slack event handler: signature verification, `url_verification` challenge, `app_mention` event handling with thread context and Block Kit plan messages; `NewSlackInteractionHandler` for Slack button click callbacks
- `approve.go` — `Approver`: shared approval path for both Slack button and web UI; atomic double-approval guard via `Hub.TryStartImplementation`
- `llm.go` — `Message`/`Role` types (used by intent parser and slack thread parsing)
- `intent.go` — `ParseIntent`: single Claude Haiku call that extracts `{Repo, Task, Question, PlanApproved, PlanFeedback}` from a Slack conversation; detects plan state via the `📋 *Plan*` marker
- `orchestrator.go` — `Orchestrator`: three-path dispatch (planning / plan-feedback / implementation); calls `ParseIntent`, then routes to `executePlanning` or `executeImplementation`
- `git.go` — Plain functions: `FindRepo` (GitHub REST API), `CloneRepo` (shallow clone to `/workspace`), `CreatePullRequest` (commit + push + GitHub API)
- `claudecode.go` — `runClaudeCode` (shared CLI logic), `GeneratePlan` (prompt-enforced read-only), `ImplementChanges` (implementation mode); `claudeStreamParser` for streaming JSON events
- `util.go` — `truncate` helper
- `notify.go` — `SlackNotifier` for posting mid-execution messages; context keys for channel, threadTS, jobID, and hub
- `monitor.go` — `Hub` (SSE fan-out + JSONL persistence), event types, `streamingWriter`, REST handlers (`/api/jobs`, `/api/jobs/{id}`), SSE handler (`/events`), dark-terminal web UI, and approval state tracking (`implementingJobs`, `planMsgTS`, `jobMeta` sync.Maps)

### Orchestration pattern (plan-first workflow)

Every Slack mention triggers one Claude Haiku call (`ParseIntent`) that returns `{Repo, Task, Question, PlanApproved, PlanFeedback}`. The orchestrator then dispatches to one of three paths:

**Fresh request or plan feedback → `executePlanning`:**
1. `FindRepo` — verify repo exists via GitHub API
2. `getOrCreateJob` — reuse the active job for this Slack thread, or create a new one
3. `CloneRepo` — shallow clone to `/workspace`
4. `GeneratePlan` — run Claude Code CLI with prompt-enforced read-only mode (no `--permission-mode plan`; uses `--dangerously-skip-permissions` with explicit "do not modify files" instructions)
5. Format plan with `📋 *Plan*` marker and approval footer → reply to Slack
6. Job stays **open** — same job ID is reused for feedback rounds and implementation

**Plan approval → `executeImplementation`:**
1. `extractPlanFromThread` — find the most recent `📋 *Plan*` in thread
2. `getOrCreateJob` — reuse the active job for this thread (same monitoring link)
3. `CloneRepo` → `ImplementChanges` with the approved plan in the prompt
4. `completed` → `CreatePullRequest` → reply with PR URL → job **closed**

**Question → return clarification (no job created)**

**Unified job per thread:** A single monitoring job spans the entire planning → feedback → implementation lifecycle within a Slack thread. `Hub.threadJobs` maps `channel:threadTS` to the active job ID. The job is created on first mention and only closed when a PR is created, an error occurs, or the thread starts a fresh request. This means one monitoring link per thread — the user sees planning events and implementation events in the same timeline.

Plan state detection: Haiku detects the `📋 *Plan*` marker in assistant messages. If present and the user's latest message is an approval ("go", "lgtm", etc.), `PlanApproved` is set. If the user provides feedback, `PlanFeedback` is set. Thread-as-state handles unlimited revision rounds.

### Interactive plan approval

Plans are posted as Slack Block Kit messages with an "Approve" button. Two approval paths converge on a shared `Approver`:

**Slack button:** `/webhooks/slack/interactions` receives `block_actions` callbacks. The handler verifies the Slack signature, extracts the job ID from the button value, returns 200 immediately, then calls `approver.Approve` in a goroutine.

**Web UI:** `POST /api/jobs/{id}/approve` triggers `approver.Approve` using stored `JobMeta` (channel + threadTS) for the Slack thread coordinates.

**Text-based:** Typing "go" / "lgtm" in the thread still works — `ParseIntent` detects `PlanApproved` and the orchestrator calls `executeImplementation` directly.

`Approver.Approve` flow: atomic guard via `Hub.TryStartImplementation` (prevents double-approval) → update plan message to remove button ("Approved by ...") → post "Implementing..." message → fetch thread → `orchestrator.ImplementApprovedPlan` → post result (PR URL or error). On error, `ClearImplementation` allows retry.

When feedback triggers a revised plan, `handleMention` updates the old plan message (removes its button, labels it "superseded by updated plan") before posting the new plan with a fresh button.

**Why prompt-based planning:** `--permission-mode plan` doesn't actually restrict tools when combined with `--dangerously-skip-permissions` (the skip flag overrides plan mode). So planning uses prompt instructions to enforce read-only behavior instead.

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
