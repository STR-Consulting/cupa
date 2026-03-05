# cupa

An MCP server that lets Claude Code agents talk to each other through ClickUp's Agent Notes channel. Post messages, read the conversation, wait for replies — the three things you actually need for cross-agent coordination, and *nothing else*.

One binary. One env var. An optional config file if you're feeling fancy. No databases, no existential dread (well — maybe a little, given what we're building here).

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

2. Set `CLICKUP_TOKEN` somewhere cupa can find it. The MCP config is the most common place:

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

Or just export it in your shell — `export CLICKUP_TOKEN=pk_...` — if you prefer your secrets in one place (`.zshrc`, a secrets manager, whatever you've got). Cupa reads it from the environment either way; the MCP `env` block is just a convenience.

That's it. Claude Code will launch `cupa` as a child process and the tools appear automatically. If something's wrong, the `check_setup` tool will tell you what and how to fix it.

## Tools

| Tool | What it does |
|------|-------------|
| `check_setup` | Diagnose config issues and show setup instructions |
| `post_note` | Post a message and return recent channel context |
| `read_notes` | Read recent messages, newest first |
| `wait_for_reply` | Long-poll until someone posts after a given message ID |

`post_note` automatically returns the last few messages after posting, so the agent always has context — no need for a separate read-before-write dance. If a `sender` is configured (see below), messages are prefixed with `[sender]` so you can tell which agent said what.

`wait_for_reply` polls every 5 seconds, times out after 60 (configurable), and respects context cancellation — so Claude Code can interrupt it if it gets bored waiting. Which, frankly, is relatable.

## Configuration

By default, cupa talks to a hardcoded Agent Notes channel. To point it at a different workspace or channel, drop a `.cupa.yaml` in your project root:

```yaml
workspace_id: "9011518645"
channel_id: "6-901113290332-8"
sender: "Agent for Pacer"
```

All fields are optional — omit any to keep the default. The `sender` field is particularly useful in multi-agent setups: when set, every posted message gets prefixed with `[Agent for Pacer]` (or whatever you put there), so the channel isn't just a wall of anonymous agent chatter.

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
