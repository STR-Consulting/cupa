# cupa

An MCP server that lets Claude Code agents talk to each other through ClickUp's Agent Notes channel. Post messages, read the conversation, wait for replies — the three things you actually need for cross-agent coordination, and *nothing else*.

One binary. One env var. No configuration files, no databases, no existential dread (well — maybe a little, given what we're building here).

## Install

### macOS (Homebrew)

```
brew install STR-Consulting/cupa/cupa
```

### Windows (Scoop)

```
scoop bucket add cupa https://github.com/STR-Consulting/scoop-cupa
scoop install cupa
```

### From source

```
go install github.com/STR-Consulting/cupa@latest
```

## Setup

1. Get a ClickUp personal API token from [Settings > Apps](https://app.clickup.com/settings/apps)

2. Add to your Claude Code MCP config (`~/.claude/settings.json` or project `.mcp.json`):

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

That's it. Claude Code will launch `cupa` as a child process and the tools appear automatically.

## Tools

| Tool | What it does |
|------|-------------|
| `post_note` | Post a message to the Agent Notes channel |
| `read_notes` | Read recent messages, newest first |
| `wait_for_reply` | Long-poll until someone posts after a given message ID |

`wait_for_reply` polls every 5 seconds, times out after 60 (configurable), and respects context cancellation — so Claude Code can interrupt it if it gets bored waiting. Which, frankly, is relatable.

## How it works

Cupa is an [MCP](https://modelcontextprotocol.io/) server that communicates over stdio. It talks to the ClickUp Chat API v3, rate-limited to ~1.5 req/s so you don't get yelled at by their servers. Auth is a raw personal token in the `Authorization` header (not Bearer — ClickUp is *particular* about this).

The whole thing is a single `main.go`. It uses the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) for the protocol bits and the Go stdlib for everything else.

## Development

```bash
go build -o cupa .
go test ./...
golangci-lint run --fix ./...
```

Releases are automated — push a `v*` tag and GitHub Actions builds macOS (arm64) and Windows (amd64) binaries, then updates the [Homebrew tap](https://github.com/STR-Consulting/homebrew-cupa) and [Scoop bucket](https://github.com/STR-Consulting/scoop-cupa).

## License

MIT
