---
# j8d-0ti
title: 'MCP server for Agent Notes: live cross-agent conversation'
status: review
type: feature
priority: normal
tags:
    - clickup
    - ai
created_at: 2026-03-05T18:40:17Z
updated_at: 2026-03-05T19:16:20Z
sync:
    clickup:
        synced_at: "2026-03-05T19:17:41Z"
        task_id: 868hrbv73
---

## Problem

Claude Code agents are synchronous — they can post/read Agent Notes via the ClickUp API, but can't monitor for replies. Live conversation between agents requires polling, which needs a persistent process.

## Proposed Solution

Build a lightweight MCP server in Go that exposes Agent Notes as native tools. Any Claude Code instance connects to it via stdio transport.

### Tools to expose

- [ ] `post_note` — post a message to Agent Notes channel (params: content string)
- [ ] `read_notes` — read recent messages (params: limit int, optional since timestamp)
- [ ] `wait_for_reply` — long-poll until a new message appears after a given message ID (params: after_message_id, timeout_seconds)

### Architecture

- Single Go binary, runs locally as MCP server via stdio
- ClickUp Chat API v3 for channel read/write
- CLICKUP_TOKEN env var for auth (each user's own token)
- wait_for_reply: poll every 3-5s until new message or timeout (default 60s)
- Cross-platform: must work on macOS and Windows

### MCP config (per-user)

```json
{
  "mcpServers": {
    "agent-notes": {
      "command": "agent-notes-mcp",
      "env": { "CLICKUP_TOKEN": "pk_..." }
    }
  }
}
```

### Distribution

- Go cross-compile for darwin-arm64, darwin-amd64, windows-amd64
- GitHub release binary (or Homebrew tap for macOS)
- Jon installs binary + adds MCP config — no dev tooling needed

## Open Questions

- Separate repo or cmd/ in this repo?
- Should wait_for_reply filter by user_id to only surface messages from other agents?
- Rate limiting for ClickUp API polling?


## Summary of Changes

Implemented the MCP server with official Go SDK (`github.com/modelcontextprotocol/go-sdk`):

- `main.go` — Single-file MCP server with inline ClickUp HTTP client
  - `post_note` — POST messages to Agent Notes channel
  - `read_notes` — GET recent messages (newest first, configurable limit)
  - `wait_for_reply` — Long-poll for new messages after a given ID
  - Rate limiting at ~1.5 req/s, raw token auth, proper error handling
- `.github/workflows/release.yml` — Tag-triggered release workflow
  - Cross-compiles darwin-arm64 + windows-amd64 (no darwin-amd64)
  - .tar.gz for macOS, .zip for Windows, SHA256 sidecars
  - Auto-updates Homebrew tap and Scoop bucket

Verification: builds clean, go vet clean, golangci-lint clean, MCP handshake + tools/list works.
