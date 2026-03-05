---
name: mcp
description: MCP (Model Context Protocol) server implementation in Go. Use when (1) implementing tool handlers, (2) handling JSON-RPC messages, (3) debugging MCP protocol issues, (4) adding new tools.
---

# MCP Server Protocol

## Overview

MCP servers communicate via JSON-RPC 2.0 over stdio (stdin/stdout). Claude Code launches the server as a child process and sends/receives JSON-RPC messages.

## Protocol Flow

1. Client sends `initialize` request
2. Server responds with capabilities (tools list)
3. Client sends `tools/call` requests
4. Server responds with tool results
5. Client sends `shutdown` when done

## Message Format

### Request (client → server on stdin)

```json
{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"capabilities": {}}}
{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}
{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "post_note", "arguments": {"content": "hello"}}}
```

### Response (server → client on stdout)

```json
{"jsonrpc": "2.0", "id": 1, "result": {"protocolVersion": "2024-11-05", "capabilities": {"tools": {}}, "serverInfo": {"name": "clickup-agent-chat", "version": "0.1.0"}}}
{"jsonrpc": "2.0", "id": 2, "result": {"tools": [...]}}
{"jsonrpc": "2.0", "id": 3, "result": {"content": [{"type": "text", "text": "Posted message 123"}]}}
```

### Error Response

```json
{"jsonrpc": "2.0", "id": 3, "error": {"code": -32602, "message": "CLICKUP_TOKEN not set"}}
```

## Tool Definition

```json
{
  "name": "post_note",
  "description": "Post a message to the Agent Notes ClickUp channel",
  "inputSchema": {
    "type": "object",
    "properties": {
      "content": {
        "type": "string",
        "description": "Message content to post"
      }
    },
    "required": ["content"]
  }
}
```

## Tool Result

```json
{
  "content": [
    {"type": "text", "text": "human-readable result"}
  ]
}
```

For errors within a tool call (not protocol errors), return a result with `isError: true`:

```json
{
  "content": [{"type": "text", "text": "ClickUp API error: 401 Unauthorized"}],
  "isError": true
}
```

## Go Implementation Approach

### Option A: No dependencies (recommended for simplicity)

Read JSON-RPC from stdin line-by-line, unmarshal, dispatch to handlers, marshal response to stdout. The protocol is simple enough that a hand-rolled implementation in ~200 lines is cleaner than pulling in a framework.

```go
type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      any             `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params"`
}

type Response struct {
    JSONRPC string `json:"jsonrpc"`
    ID      any    `json:"id"`
    Result  any    `json:"result,omitempty"`
    Error   *Error `json:"error,omitempty"`
}
```

### Option B: Use mcp-go SDK

```
go get github.com/mark3labs/mcp-go
```

Provides typed server, tool registration, and stdio transport. More boilerplate but handles edge cases.

## Important Notes

- All logging MUST go to stderr (stdout is the JSON-RPC channel)
- Each message is one line of JSON (newline-delimited)
- The server must handle `notifications/cancelled` gracefully (for long-polling cancellation)
- Protocol version: `2024-11-05`
