---
# moe-kh2
title: GitHub release action with Windows binary and Homebrew tap
status: ready
type: task
priority: normal
created_at: 2026-03-05T18:43:15Z
updated_at: 2026-03-05T18:54:01Z
parent: j8d-0ti
sync:
    clickup:
        synced_at: "2026-03-05T19:17:42Z"
        task_id: 868hrbv7f
---

Set up GitHub Actions release pipeline triggered by version tags, producing installable binaries and updating a Homebrew companion repo.

Reference implementation: toba/xc-mcp (.github/workflows/release.yml)

## Build matrix

- [ ] darwin-arm64
- [ ] darwin-amd64
- [ ] windows-amd64 (.exe)
c
## Release workflow (.github/workflows/release.yml)

- [ ] Trigger on tag push matching `v*`
- [ ] Build Go binaries for all three platforms (`go build` with GOOS/GOARCH)
- [ ] Package each binary into a tarball (or zip for Windows)
- [ ] Generate SHA256 checksums for each archive
- [ ] Create GitHub Release via `softprops/action-gh-release@v2` with archives + checksums
- [ ] Auto-generate release notes from commits since previous tag

## Homebrew tap update

- [ ] Create companion repo `STR-Consulting/homebrew-clickup-agent-chat` with `Formula/clickup-agent-chat.rb`
- [ ] Add `update-homebrew` job that runs after build job succeeds
- [ ] Download SHA256 from release artifacts
- [ ] Clone tap repo, regenerate formula with new version/URL/SHA256
- [ ] Commit and push to tap repo using `CLICKUP_TAP_TOKEN` secret
- [ ] Formula should install darwin-arm64 and darwin-amd64 binaries (use `on_arm`/`on_intel` blocks)

## Scoop bucket update (Windows)

- [ ] Create companion repo `STR-Consulting/scoop-clickup-agent-chat` with `bucket/clickup-agent-chat.json`
- [ ] Add `update-scoop` job that runs after build job succeeds
- [ ] Download SHA256 from release artifacts
- [ ] Clone bucket repo, update manifest JSON with new version/URL/hash
- [ ] Commit and push to bucket repo using `CLICKUP_TAP_TOKEN` secret

## Secrets needed

- `CLICKUP_TAP_TOKEN` — GitHub PAT with push access to both companion repos (homebrew tap + scoop bucket)
