---
# qy9-q8i
title: Support image/file attachments in chat messages
status: draft
type: feature
priority: normal
created_at: 2026-03-05T20:14:31Z
updated_at: 2026-03-05T20:14:31Z
sync:
    clickup:
        synced_at: "2026-03-05T20:17:10Z"
        task_id: 868hrde9h
---

Add support for sharing images and attachments through the Agent Notes channel.

- [ ] Confirm ClickUp Chat API v3 supports file attachments on messages (currently undocumented)
- [ ] Add `.cupa.yaml` config for attachments: `attachments_enabled` (bool, default true), `max_file_size` (string, default "10MB")
- [ ] Implement file upload flow (likely: upload via attachments API, reference in message)
- [ ] Add `post_attachment` MCP tool or extend `post_note` with file_path parameter

## Blockers

The ClickUp Chat API v3 is experimental and does not currently document an endpoint for attaching files to chat messages. The task attachment API exists (`POST /task/{task_id}/attachment`, multipart/form-data, 1GB max) but there's no equivalent for chat channels. The UI supports attachments via paperclip, so the API capability may exist undocumented or may be added later.

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
