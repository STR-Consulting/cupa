---
# bm3-f58
title: Add start_chat and stop_chat MCP tools for interactive cross-agent conversation
status: completed
type: feature
priority: normal
created_at: 2026-03-05T20:11:53Z
updated_at: 2026-03-05T20:12:13Z
sync:
    clickup:
        synced_at: "2026-03-05T20:17:10Z"
        task_id: 868hrde9g
---

Add two new MCP tools to enable interactive background chat between agents:

- [x] `start_chat` — posts a message and polls until a reply arrives, returns the reply. Designed to run as a background task. Tracks conversation state (lastID) across calls for multi-turn conversations.
- [x] `stop_chat` — cancels any active chat session and resets conversation state.
- [x] Session state management with mutex-protected globals
- [x] Tests for: post+reply flow, already-active error, cancellation, stop with/without session
- [x] Lint clean
- [x] Update CLAUDE.md tool table

## Context
The existing `post_note` + `wait_for_reply` tools work but require the agent to manage message IDs manually. `start_chat`/`stop_chat` provide a higher-level workflow with clear instructions in the tool description for running as a background task.


## Summary of Changes

Added `start_chat` and `stop_chat` MCP tools to `main.go` with full test coverage. `start_chat` combines posting a message and polling for a reply into a single tool call designed for background execution. It tracks the last seen message ID across calls so multi-turn conversations work seamlessly. `stop_chat` cancels any active session. Updated CLAUDE.md tool table and server instructions.
