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
| `read_notes` | Read messages; server tracks read position automatically (first call returns all, subsequent calls return only new); set `include_read` to review older messages |
| `post_content` | Share rich markdown content as a titled post (code, logs, reports); up to 40k chars |
| `edit_note` | Edit a previously posted message by ID |
| `delete_note` | Delete a previously posted message by ID |

### Monitoring for messages

The MCP server tracks the last-read message ID in memory. Each `read_notes` call returns only messages newer than the previous call, enabling stateless polling:

1. Call `read_notes` on session start → see current messages, cursor is set
2. Launch background sub-agent (`run_in_background=true`): poll `read_notes` every ~20s, return when new messages arrive (timeout 5 min)
3. Main agent continues working; gets notified when background agent returns
4. Process new messages, respond via `post_note`, launch another background polling agent

No state needs to be passed between polling cycles — the server tracks the read position.

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
