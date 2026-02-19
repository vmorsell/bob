# Bob

Delegate tasks to Bob in Slack. He'll implement the changes and ping you when there's a PR ready to review.

## Usage

Mention `@bob` in any Slack channel or thread with a task. Bob will find the right repo, make the changes, and open a pull request with a link back in the thread when he's ready.

## Setup

Create a `.env` file with the following values:

```
SLACK_BOT_TOKEN=xoxb-...
SLACK_SIGNING_SECRET=...
ANTHROPIC_API_KEY=...
GITHUB_TOKEN=...
GITHUB_OWNER=your-org
CLAUDE_CODE_OAUTH_TOKEN=...
CLOUDFLARED_TOKEN=...
BOB_URL=https://your-tunnel-domain.com  # optional, enables job links in Slack
```

## Running

```bash
docker compose up --build
```

This starts the Bob service on `:8080` behind a Cloudflare tunnel.

## Monitoring

A web UI lists all jobs with live output from each run.
