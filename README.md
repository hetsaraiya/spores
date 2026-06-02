# Spore

Single Slack bot for the E2B + Codex flow.

Slack input is sent to Codex inside an E2B sandbox. The bot creates or resolves a GitHub issue, implements it, pushes a branch, and opens a PR.

## Environment

Use one `.env` in this folder:

```env
SLACK_BOT_TOKEN=xoxb-...
SLACK_APP_TOKEN=xapp-...
GITHUB_TOKEN=ghp_...
# or GH_TOKEN=ghp_...
E2B_API_KEY=...
E2B_TEMPLATE_ID=...
CODEX_MODEL=
OPENAI_API_KEY=...
CODEX_AUTH_FILE=auth-codex.json
```

Codex auth can also come from `CODEX_AUTH_JSON`, `auth-codex.json`, or `~/.codex/auth.json`.

## Run

```bash
cd spore
go mod tidy
go run .
```

Use `/issue` or mention the app with a prompt that includes the target repo or an existing GitHub issue URL.
