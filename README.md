# Bob

Bob is a Slack-integrated coding agent. Mention `@bob` with a task and he'll find the right repo, implement the changes, and open a pull request — then ping you in the thread when it's ready.

## How it works

1. Mention `@bob` in any Slack channel or thread with a task description
2. Bob identifies the target repo and what needs to be done
3. He clones the repo, runs Claude Code to implement the changes, and opens a PR
4. A link to the PR (and a live job log) is posted back to your thread

If Bob needs clarification, he'll ask in the thread. Reply and he'll pick up where he left off — the thread is the state.

## Prerequisites

- Docker and Docker Compose
- A Slack app with Events API enabled and `app_mention` subscribed
- A Cloudflare tunnel (for receiving Slack webhooks)
- GitHub personal access token with repo permissions
- Anthropic API key
- Claude Code OAuth token

## Setup

Create a `.env` file:

```
SLACK_BOT_TOKEN=xoxb-...          # Slack bot token
SLACK_SIGNING_SECRET=...           # Slack app signing secret
ANTHROPIC_API_KEY=...              # Anthropic API key
GITHUB_TOKEN=...                   # GitHub token (repo read/write)
GITHUB_OWNER=your-org              # GitHub org or user that owns the repos
CLAUDE_CODE_OAUTH_TOKEN=...        # Claude Code OAuth token
CLOUDFLARED_TOKEN=...              # Cloudflare tunnel token
BOB_URL=https://your-tunnel.com   # Optional — enables job links in Slack messages
```

## Running

```bash
docker compose up --build
```

This starts the Bob service on `:8080` behind a Cloudflare tunnel.

Point your Slack app's event subscription URL to `https://your-tunnel.com/webhooks/slack`.

## Monitoring

A web UI is available at your tunnel URL. It lists all jobs with live streaming output from each run.
