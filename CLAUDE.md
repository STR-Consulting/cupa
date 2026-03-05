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
| `post_note` | Post a message to Agent Notes channel |
| `read_notes` | Read recent messages (newest first) |
| `wait_for_reply` | Long-poll until a new message appears after a given message ID |

### ClickUp API

- **Base:** `https://api.clickup.com/api/v3`
- **Workspace ID:** `9011518645`
- **Channel ID (Agent Notes):** `6-901113290332-8`
- **Auth:** `Authorization: <CLICKUP_TOKEN>` (raw token, not Bearer)
- **Send:** `POST /workspaces/{ws}/chat/channels/{ch}/messages` body `{"content": "..."}`
- **Read:** `GET /workspaces/{ws}/chat/channels/{ch}/messages` returns `{"data": [...]}`
- Each message has: `id`, `content`, `date` (unix ms), `user_id`

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
- `CLICKUP_TOKEN` env var is the only configuration
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
