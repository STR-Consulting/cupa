# cupa

MCP server in Go that exposes ClickUp Agent Notes as native Claude Code tools. Enables live cross-agent conversation via the Agent Notes channel.

## Project Info

- **Language:** Go
- **Binary:** `cupa` (MCP server, stdio transport)
- **Repo:** github.com/STR-Consulting/cupa
- **Parent issue:** zen-ovs in pacer/core

## Architecture

Single Go binary, runs as MCP server via stdio. Claude Code launches it as a child process.

### MCP Tools

| Tool | Description |
|------|-------------|
| `check_setup` | Check config status, show setup instructions for token/workspace/channel |
| `post_note` | Post a message (auto-prefixed with sender if configured), returns recent messages for context |
| `read_notes` | Read recent messages (newest first) |
| `wait_for_reply` | Long-poll until a new message appears after a given message ID |
| `post_content` | Share rich markdown content as a titled post (code, logs, reports); up to 40k chars |
| `start_chat` | Post a message and poll for a reply (run as background task); tracks conversation state across calls |
| `stop_chat` | Cancel an active `start_chat` session and reset conversation state |

### ClickUp API

- **Base:** `https://api.clickup.com/api/v3`
- **Workspace ID:** `9011518645`
- **Channel ID (Agent Notes):** `6-901113290332-8`
- **Auth:** `Authorization: <CLICKUP_TOKEN>` (raw token, not Bearer)
- **Send:** `POST /workspaces/{ws}/chat/channels/{ch}/messages` body `{"content": "..."}`
- **Read:** `GET /workspaces/{ws}/chat/channels/{ch}/messages` returns `{"data": [...]}`
- Each message has: `id`, `content`, `date` (unix ms), `user_id`

### Project config (`.cupa.yaml`)

Optional file in the working directory to override the default workspace/channel:

```yaml
workspace_id: "9011518645"
channel_id: "6-901113290332-8"
```

- If absent, the defaults above are used (Agent Notes channel).
- Project name is auto-detected from git remote (or directory name) and prefixed on all messages as `[project]`.

### MCP config (end-user)

```json
{
  "mcpServers": {
    "agent-notes": {
      "command": "cupa",
      "env": { "CLICKUP_TOKEN": "pk_..." }
    }
  }
}
```

## Dev Guidelines

- Keep it simple — single `main.go` or minimal packages
- No external dependencies beyond the Go standard library and an MCP SDK if needed
- Cross-platform: must build for darwin-arm64, windows-amd64
- `CLICKUP_TOKEN` env var for auth; `.cupa.yaml` for workspace/channel targeting
- Always run `golangci-lint run --fix ./...` after modifying Go code
- Always run `shellcheck` after modifying shell scripts

## Build & Test

```bash
go build -o cupa .
go test ./...
```

### Cross-compile

```bash
GOOS=darwin GOARCH=arm64 go build -o dist/cupa-darwin-arm64 .
GOOS=windows GOARCH=amd64 go build -o dist/cupa-windows-amd64.exe .
```

## Skill Reference

Read the linked SKILL.md before starting any task in that area.

### Core
clickup-api|ClickUp Chat API v3 reference|.claude/skills/clickup-api/SKILL.md
mcp|MCP server protocol and Go implementation|.claude/skills/mcp/SKILL.md
