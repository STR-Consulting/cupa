---
# qy9-q8i
title: Support file sharing via channel-linked Tasks and Docs
status: completed
type: feature
priority: normal
created_at: 2026-03-05T20:14:31Z
updated_at: 2026-03-05T20:25:39Z
sync:
    clickup:
        synced_at: "2026-03-05T20:29:53Z"
        task_id: 868hrde9h
---

Enable agents to share rich content and files through the Agent Notes channel.

## API Research Findings

The v3 OpenAPI spec reveals the `createChatMessage` endpoint supports more than plain text:

```
POST /api/v3/workspaces/{ws}/chat/channels/{ch}/messages

Body:
  type: "message" | "post"       # post = rich inline content
  content: string (max 40k chars)  # supports text/md format
  content_format: "text/md" | "text/plain"
  post_data:                       # required when type="post"
    title: string (max 255 chars)
    subtype: { id: string }        # workspace-specific subtype IDs
  triaged_object_id: string        # link message to a task/doc
  triaged_object_type: number      # type of linked object
```

**Attachments cannot be added to chat messages or channels** — the entity attachment API only accepts `attachments` (tasks) and `custom_fields` as entity types. No `message` or `channel` entity type exists.

## Recommended Approach

Two tiers, ordered by agent discoverability and efficiency:

### Tier 1: Post-type messages (single API call, recommended)

Use `type: "post"` messages for sharing structured content directly in the channel. This is a **single API call** with up to 40k chars of markdown content and a title. Ideal for:
- Code snippets, logs, error output
- Structured reports and summaries
- Any content an agent would otherwise put in an attachment

Requires discovering the workspace's post subtype IDs first (one-time setup).

- [x] Call "Get Post Subtype IDs" endpoint to discover available subtypes
- [x] Add `post_content` MCP tool (params: `title`, `content`) that creates a `type: "post"` message
- [x] Support `content_format: "text/md"` for markdown rendering

### Tier 2: Task-based file sharing (multi-step, for binary files only)

For actual binary files (images, PDFs, archives) that can't be inlined as text:
1. Create a task in a designated List
2. Upload file to task via `POST /api/v2/task/{id}/attachment` (multipart/form-data)
3. Post a message with `triaged_object_id` referencing the task

This is heavier (3 API calls) but is the only path for binary files.

- [ ] Add `.cupa.yaml` config: `attachments_list_id` (ClickUp List for file-bearing tasks), `max_file_size` (default "10MB")
- [ ] Add `post_file` MCP tool (params: `file_path`, `description`) that orchestrates the 3-step flow
- [ ] Investigate `triaged_object_id`/`triaged_object_type` to link the task inline in the message

## Decision: Start with Tier 1

Tier 1 (`post_content`) covers 90%+ of agent sharing needs (code, logs, reports) in a single efficient API call. Agents don't need to discover Lists or manage tasks — they just post content. Tier 2 can be added later if binary file sharing proves necessary.

## References

- OpenAPI v3 spec: https://developer.clickup.com/openapi/ClickUp_PUBLIC_API_V3.yaml
- Send a message: https://developer.clickup.com/reference/createchatmessage
- Task attachments: https://developer.clickup.com/reference/createtaskattachment
- Attachments docs: https://developer.clickup.com/docs/attachments

## References

- ClickUp Chat API: https://developer.clickup.com/docs/chat
- ClickUp Attachments: https://developer.clickup.com/docs/attachments
- Send a message: https://developer.clickup.com/reference/createchatmessage
- Add attachments to messages (UI): https://help.clickup.com/hc/en-us/articles/30093501131287-Add-attachments-to-messages

## Desired config (.cupa.yaml)

```yaml
workspace_id: "9011518645"
channel_id: "6-901113290332-8"
attachments_enabled: true
max_file_size: "10MB"
```


## Summary of Changes

Implemented `post_content` MCP tool (Tier 1). Fetches workspace post subtype IDs on first use (cached), prefers "Update" subtype. Creates `type: "post"` messages with markdown content format, project-prefixed titles, up to 40k chars. Tests cover: full post flow with body verification, empty field validation, subtype caching. CLAUDE.md updated.
