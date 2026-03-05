---
name: clickup-api
description: ClickUp Chat API v3 reference for the Agent Notes channel. Use when working on any ClickUp HTTP calls, message sending/reading, or debugging API responses.
---

# ClickUp Chat API v3

## Authentication

All requests use header: `Authorization: <token>` (raw personal API token, NOT `Bearer`).

Tokens are per-user. Generate at: https://app.clickup.com/settings/apps

## Endpoints

Base: `https://api.clickup.com/api/v3`

### List Channels

```
GET /workspaces/{workspace_id}/chat/channels
```

Response: `{"data": [{"id": "...", "name": "...", "type": "CHANNEL|DM|GROUP_DM"}]}`

### Send Message

```
POST /workspaces/{workspace_id}/chat/channels/{channel_id}/messages
Content-Type: application/json

{"content": "message text"}
```

Response:
```json
{
  "content": "message text",
  "date": 1772734351721,
  "id": 80110035934433,
  "parent_channel": "6-901113290332-8",
  "replies_count": 0,
  "resolved": false,
  "user_id": "87367200",
  "links": {
    "reactions": "/api/v3/.../reactions",
    "replies": "/api/v3/.../replies",
    "tagged_users": "/api/v3/.../tagged_users"
  }
}
```

### Read Messages

```
GET /workspaces/{workspace_id}/chat/channels/{channel_id}/messages
```

Response: `{"data": [{message}, ...]}` — newest first.

Each message:
- `id` — int64 message ID
- `content` — plain text
- `date` — unix milliseconds timestamp
- `user_id` — string user ID of sender
- `replies_count` — int
- `resolved` — bool

### Get Thread / Specific Message

```
GET /workspaces/{workspace_id}/chat/channels/{channel_id}/messages?thread_id={message_id}
```

## IDs for This Project

| Resource | ID |
|----------|----|
| Workspace (Pacer) | `9011518645` |
| Agent Notes channel | `6-901113290332-8` |

## Content Format

ClickUp chat is plain text. Markdown is partially rendered (bold, italic, links, bullet lists) but headers and code blocks are not. Use:
- Line breaks for structure
- Dashes for bullet lists
- `*bold*` sparingly
- No markdown headers (`#`, `##`)

## Rate Limits

ClickUp API rate limit: 100 requests per minute per token. For polling, 3-5 second intervals are safe (12-20 req/min).

## Error Responses

```json
{"status": 401, "message": "Token invalid"}
{"status": 429, "message": "Rate limit exceeded"}
```

Always check HTTP status before parsing response body.
