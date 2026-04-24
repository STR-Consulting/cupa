---
# o1r-k5t
title: read_notes tool description should instruct agents to default include_read to false
status: completed
type: feature
priority: normal
created_at: 2026-04-24T17:43:03Z
updated_at: 2026-04-24T17:47:37Z
sync:
    clickup:
        synced_at: "2026-04-24T17:53:40Z"
        task_id: 868jcyx5u
---

## Problem

Agents repeatedly call `read_notes` with `include_read: true` even when the user just wants to see new/unread messages. This has been a recurring issue despite instructions in SKILL.md and CLAUDE.md — agents don't always read those before calling the tool.

## Solution

Update the `read_notes` tool description (the MCP tool schema description) to explicitly instruct agents:

> **Always use `include_read: false` (the default) unless the user explicitly asks to re-read old messages.** Requests like "read notes", "check agent notes", or "read latest" mean "show me what's new" — use `include_read: false`.

The tool description is the one place every agent sees immediately before calling the tool, so this is the most reliable place to enforce the behavior.

## Acceptance

- [x] `read_notes` tool description includes the instruction
- [x] `include_read` parameter description clarifies it should only be true when user explicitly asks for old messages


## Summary of Changes

Updated two places in `main.go`:
1. `read_notes` tool description now explicitly instructs agents to always use `include_read: false` unless the user explicitly asks to re-read old messages
2. `include_read` struct tag description changed from neutral phrasing to directive: "Only set to true when the user explicitly asks to re-read old/previous messages"
