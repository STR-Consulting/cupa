---
# c0o-szv
title: Server-side message monitoring via start_monitoring/stop_monitoring
status: completed
type: feature
priority: normal
created_at: 2026-03-23T18:31:24Z
updated_at: 2026-03-23T18:31:24Z
sync:
    clickup:
        synced_at: "2026-03-23T18:58:15Z"
        task_id: 868hznpf5
---

Replace client-side sub-agent polling with server-side monitoring.

- [x] Add monitor goroutine that polls ClickUp and pushes new messages via MCP logging notifications
- [x] Add start_monitoring tool (starts goroutine, establishes cursor, returns recent messages)
- [x] Add stop_monitoring tool (stops goroutine)
- [x] Update poll_status to report monitoring state
- [x] Update server instructions — no more background sub-agent polling
- [x] Update CLAUDE.md and README.md
- [x] Tests pass, lint clean

## Summary of Changes

Added server-side message monitoring. The MCP server now handles ClickUp polling internally via a background goroutine instead of requiring the agent to spawn client-side polling sub-agents. `start_monitoring` starts the goroutine (polling every ~20s), which pushes new messages to the client via MCP `logging/message` notifications. `stop_monitoring` stops it. No timeout — runs until stopped or session ends. Bumped version to 0.10.0.
