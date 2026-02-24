# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Bob

Bob is an LLM helper for a software team, integrated with Slack via the Events API. He runs as a containerized Go service behind a Cloudflare tunnel. When mentioned, Bob parses the intent of the request with a single Claude Haiku call, then drives a deterministic plan-first workflow: he explores the codebase using Claude Code with `--resume` for session continuity, presents a plan for approval, and only implements and creates a pull request after the user approves.

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
- `slack.go` — Slack event handler: signature verification, `url_verification` challenge, `app_mention` dispatch based on job state (new request vs reply vs approval); `isApprovalText` for text-based approvals; `NewSlackInteractionHandler` for Slack button click callbacks
- `approve.go` — `Approver`: shared approval path for both Slack button and web UI; uses `Hub.TryStartImplementation` phase CAS guard; calls `orchestrator.HandleApproval` directly using `JobState`
- `llm.go` — `Message`/`Role` types (used by intent parser and slack thread parsing)
- `intent.go` — `ParseIntent`: single Claude Haiku call that extracts `{Repo, Task, Question}` from a Slack conversation (first mention only; no plan state detection)
- `orchestrator.go` — `Orchestrator` with three entry points: `HandleNewRequest` (parse intent → plan), `HandleReply` (resume planning session), `HandleApproval` (fresh execution session); `resetRepo`, `readPlanFile`, `formatPlanBlocks`, `formatApprovedPlanBlocks`, `taskBranchName`
- `git.go` — Plain functions: `FindRepo` (GitHub REST API), `CloneRepo` (shallow clone to `/workspace`), `CreatePullRequest` (commit + push + GitHub API)
- `claudecode.go` — `RunSession` (unified CLI executor with `--resume` and `--permission-mode` support), `resetRepo`, `claudeStreamParser` (detects `system/init` session ID, `AskUserQuestion`, `ExitPlanMode`, `Write` to `.claude/plans/`, result events); system prompt constants `planSystemPrompt` and `executeSystemPrompt`
- `util.go` — `truncate` helper
- `notify.go` — Context key helpers for channel, threadTS, jobID, hub, mentionTS
- `monitor.go` — `Hub` (SSE fan-out + JSONL persistence), `JobPhase`/`JobState` types, event types, REST handlers (`/api/jobs`, `/api/jobs/{id}`), SSE handler (`/events`), dark-terminal web UI

### Orchestration pattern (session-continuous plan-first workflow)

The first Slack mention in a thread triggers a single Claude Haiku call (`ParseIntent`) that returns `{Repo, Task, Question}`. Subsequent mentions in the same thread use `--resume` to continue the planning session without re-parsing intent.

**`HandleNewRequest` (first mention):**
1. `ParseIntent` → repo + task (or clarifying question)
2. `FindRepo` — verify repo exists via GitHub API
3. `createJob` — register job with `Hub`, set phase=planning
4. `CloneRepo` + `resetRepo` — shallow clone to `/workspace`, reset to clean main
5. `RunSession(plan mode, new session)` — Claude Code CLI with `--permission-mode plan` and `planSystemPrompt`
6. Inspect `SessionResult`:
   - `Question` → phase=awaiting_question, return question to Slack
   - `PlanExited` → read plan file, cache in `JobState.PlanContent`, phase=awaiting_approval, return plan blocks
   - `IsError` → close job, return error
   - Fallback → use `ResultText` as plan

**`HandleReply` (subsequent mentions with active job):**
1. Get `JobState` (has SessionID, Repo)
2. `RunSession(plan mode, --resume <sessionID>, prompt=userText)` — NO git reset, NO system prompt (already in session context)
3. Inspect result same as above, update phase and SessionID

**`HandleApproval` (plan approved via button, text, or web UI):**
1. Get `JobState`, `TryStartImplementation` phase CAS guard
2. Phase=implementing
3. `resetRepo` — clean state from main (matching what the plan was based on)
4. **Fresh session** (NO --resume): `RunSession(acceptEdits mode, executeSystemPrompt, prompt=task+planContent)`
5. On success: `CreatePullRequest(...)`, close job, return PR URL
6. On error: `ClearImplementation`, return error

**`--resume` is used only within planning.** When transitioning to execution, a fresh session starts with the plan as prompt context. The plan must be self-contained (file paths, code snippets, function signatures) so implementation doesn't require re-exploration.

### Job state model

`JobState` in `Hub.jobStates` (sync.Map) tracks the full lifecycle per thread:

```
JobState {
    SessionID    // planning session ID (for --resume within planning)
    Repo, Task   // from initial ParseIntent
    Phase        // planning → awaiting_question → planning → awaiting_approval → implementing → done
    PlanFilePath // Write to .claude/plans/ detected
    PlanContent  // cached plan text
    Channel, ThreadTS, PlanMsgTS  // Slack coordinates
}
```

Phases: `PhasePlanning`, `PhaseAwaitingQuestion`, `PhaseAwaitingApproval`, `PhaseImplementing`, `PhaseDone`. `TryStartImplementation` is a phase CAS from `awaiting_approval` to `implementing`.

### Interactive plan approval

Plans are posted as Slack Block Kit messages with an "Approve" button. Three approval paths:

**Slack button:** `/webhooks/slack/interactions` receives `block_actions` callbacks → `approver.Approve` in goroutine.

**Web UI:** `POST /api/jobs/{id}/approve` → reads `JobState` for Slack thread coordinates → `approver.Approve`.

**Text-based:** `isApprovalText` map lookup (`"go"`, `"lgtm"`, `"approved"`, etc.) in `handleMention` → `approver.Approve`.

`Approver.Approve` flow: `TryStartImplementation` guard → update plan message (remove button, "Approved by ...") → post "Implementing..." → `orchestrator.HandleApproval` → post result.

When feedback triggers a revised plan, `handleMention` updates the old plan message (removes its button, labels it "superseded by updated plan") before posting the new plan with a fresh button.

### Stream parser and signal detection

`claudeStreamParser` processes `--output-format stream-json` lines and detects structural signals (no LLM needed):

- `system` event with `subtype=init` → capture `session_id` (for `--resume`)
- `assistant` → `tool_use` → `AskUserQuestion` (main agent only, `parent_tool_use_id == ""`) → extract question
- `assistant` → `tool_use` → `ExitPlanMode` → set `planExited`
- `assistant` → `tool_use` → `Write` where `file_path` contains `.claude/plans/` → record `planFilePath`
- `result` event → capture result text, set `isError` if error subtype

All events are emitted to the `Hub` for web UI monitoring. No Slack notifications from the parser — Slack messaging is handled by `slack.go` and `approve.go` directly.

### Monitoring pattern

When a job starts, a UUID job ID is created and all subsequent tool calls and Claude Code output lines are emitted as `Event` values:
1. Persisted to `/workspace/.bob/{jobID}.jsonl` (one JSON line per event)
2. Fanned out to any connected SSE clients (`/events?job={id}`)

The web UI at the tunnel root lists all jobs; `/jobs/{id}` shows the live event stream. Clarification responses (no job started) produce no job entry.

### Slack dispatch pattern

`handleMention` checks for an active job in the thread via `Hub.ActiveJobForThread`:

- **No active job** → `orch.HandleNewRequest(ctx, messages)` (full intent parsing, new planning session)
- **Active job, phase=awaiting_approval, approval text** → `approver.Approve(...)` (text-based approval)
- **Active job, any other phase** → `orch.HandleReply(ctx, jobID, userText)` (resume planning session)

New webhook sources (Linear, GitHub, etc.) get their own `<source>.go` file and `/webhooks/<source>` route.
