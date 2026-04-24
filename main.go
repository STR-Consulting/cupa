package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	clickupBase    = "https://api.clickup.com/api/v3"
	configFile     = ".cupa.yaml"
	minRequestGap  = 700 * time.Millisecond // ~1.5 req/s
	defaultLimit   = 20
	postTypePost    = "post"
	contentFormatMD = "text/md"
)

type config struct {
	WorkspaceID string
	ChannelID   string
	Project     string
}

var cfg config

// detectProject returns a project name from git remote or directory name.
func detectProject() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err == nil {
		url := strings.TrimSpace(string(out))
		// git@github.com:org/repo.git or https://github.com/org/repo.git
		url = strings.TrimSuffix(url, ".git")
		if i := strings.LastIndex(url, "/"); i >= 0 {
			return url[i+1:]
		}
		if i := strings.LastIndex(url, ":"); i >= 0 {
			return url[i+1:]
		}
	}
	// Fall back to current directory name.
	if wd, err := os.Getwd(); err == nil {
		return filepath.Base(wd)
	}
	return ""
}

func loadConfig() error {
	cfg = config{
		WorkspaceID: "9011518645",
		ChannelID:   "6-901113290332-8",
		Project:     detectProject(),
	}

	f, err := os.Open(configFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open %s: %w", configFile, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		switch key {
		case "workspace_id":
			cfg.WorkspaceID = val
		case "channel_id":
			cfg.ChannelID = val
		}
	}
	return scanner.Err()
}

func messagesPath() string {
	return "/workspaces/" + cfg.WorkspaceID + "/chat/channels/" + cfg.ChannelID + "/messages"
}

func messagePath(id string) string {
	return "/workspaces/" + cfg.WorkspaceID + "/chat/messages/" + id
}

// rateLimiter enforces minimum gap between ClickUp API calls.
var rateLimiter = struct {
	mu   sync.Mutex
	last time.Time
}{}

// lastRead tracks the most recent message ID returned by read_notes,
// enabling automatic incremental polling without caller-managed state.
// Persisted to ~/.cupa/cursors/<channel_id> across sessions.
var lastRead struct {
	mu       sync.Mutex
	id       int64
	lastPoll time.Time // when read_notes was last called
}

// cursorDir returns the directory for persisted cursors (~/.cupa/cursors).
func cursorDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cupa", "cursors"), nil
}

// cursorPath returns the file path for the current channel's cursor.
func cursorPath() (string, error) {
	dir, err := cursorDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cfg.ChannelID), nil
}

// loadCursor reads the persisted cursor for the current channel.
func loadCursor() {
	path, err := cursorPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var id int64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &id); err == nil && id > 0 {
		lastRead.mu.Lock()
		lastRead.id = id
		lastRead.mu.Unlock()
	}
}

// saveCursor persists the current cursor for the current channel.
func saveCursor(id int64) {
	path, err := cursorPath()
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, fmt.Appendf(nil, "%d\n", id), 0o600)
}

func clickupRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	token := os.Getenv("CLICKUP_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("CLICKUP_TOKEN not set")
	}

	// Rate limit: compute wait outside the lock to avoid blocking other callers.
	rateLimiter.mu.Lock()
	wait := minRequestGap - time.Since(rateLimiter.last)
	rateLimiter.mu.Unlock()
	if wait > 0 {
		time.Sleep(wait)
	}
	rateLimiter.mu.Lock()
	rateLimiter.last = time.Now()
	rateLimiter.mu.Unlock()

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, clickupBase+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var detail string
		var apiErr struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		}
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
			detail = apiErr.Message
		} else {
			detail = string(respBody)
		}

		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("ClickUp API 401: %s — token is invalid or expired. Use check_setup to get a new token", detail)
		case http.StatusNotFound:
			return nil, fmt.Errorf("ClickUp API 404: %s — workspace or channel not found. Check workspace_id and channel_id in .cupa.yaml", detail)
		case http.StatusTooManyRequests:
			return nil, fmt.Errorf("ClickUp API 429: rate limited — wait a moment and retry. Detail: %s", detail)
		default:
			return nil, fmt.Errorf("ClickUp API error %d: %s", resp.StatusCode, detail)
		}
	}

	return respBody, nil
}

type message struct {
	ID      json.Number `json:"id"`
	Content string      `json:"content"`
	Date    json.Number `json:"date"`
	UserID  string      `json:"user_id"`
}

func readMessages(ctx context.Context) ([]message, error) {
	data, err := clickupRequest(ctx, http.MethodGet, messagesPath(), nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []message `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}
	return resp.Data, nil
}

func formatMessage(m message) string {
	ms, _ := m.Date.Int64()
	t := time.UnixMilli(ms).UTC().Format(time.RFC3339)
	return fmt.Sprintf("[%s] user:%s (id:%s)\n%s", t, m.UserID, m.ID, m.Content)
}

func formatMessages(msgs []message) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(formatMessage(m))
	}
	return b.String()
}

func messagesAfter(msgs []message, afterID int64) []message {
	var out []message
	for _, m := range msgs {
		if id, _ := m.ID.Int64(); id > afterID {
			out = append(out, m)
		}
	}
	return out
}

func prefixProject(s string) string {
	if cfg.Project != "" {
		return "[" + cfg.Project + "] " + s
	}
	return s
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

func unmarshalResponse(data []byte, v any) *mcp.CallToolResult {
	if err := json.Unmarshal(data, v); err != nil {
		return toolError(fmt.Sprintf("parse response: %v", err))
	}
	return nil
}

// --- Tool: check_setup ---

type checkSetupArgs struct{}

func handleCheckSetup(_ context.Context, _ *mcp.CallToolRequest, _ checkSetupArgs) (*mcp.CallToolResult, any, error) {
	var b strings.Builder

	// Check CLICKUP_TOKEN
	token := os.Getenv("CLICKUP_TOKEN")
	if token == "" {
		b.WriteString("## CLICKUP_TOKEN: NOT SET\n\n")
		b.WriteString("The CLICKUP_TOKEN environment variable is required for authentication.\n\n")
		b.WriteString("### How to create a ClickUp API token\n\n")
		b.WriteString("1. Log in to ClickUp\n")
		b.WriteString("2. Click your avatar (bottom-left) → Settings\n")
		b.WriteString("3. Go to Apps (in the sidebar)\n")
		b.WriteString("4. Under \"API Token\", click Generate\n")
		b.WriteString("5. Copy the token (starts with pk_)\n\n")
		b.WriteString("### Add the token to your MCP config\n\n")
		b.WriteString("In your Claude Code MCP settings (`.claude/mcp.json` or global):\n\n")
		b.WriteString("```json\n")
		b.WriteString("{\n")
		b.WriteString("  \"mcpServers\": {\n")
		b.WriteString("    \"agent-notes\": {\n")
		b.WriteString("      \"command\": \"cupa\",\n")
		b.WriteString("      \"env\": { \"CLICKUP_TOKEN\": \"pk_...\" }\n")
		b.WriteString("    }\n")
		b.WriteString("  }\n")
		b.WriteString("}\n")
		b.WriteString("```\n\n")
	} else {
		b.WriteString("## CLICKUP_TOKEN: set\n\n")
	}

	// Check .cupa.yaml
	_, err := os.Stat(configFile)
	if err != nil {
		b.WriteString("## Config file: not found (using defaults)\n\n")
	} else {
		b.WriteString("## Config file: " + configFile + "\n\n")
	}

	b.WriteString("## Active configuration\n\n")
	b.WriteString("- **Workspace ID:** " + cfg.WorkspaceID + "\n")
	b.WriteString("- **Channel ID:** " + cfg.ChannelID + "\n")
	if cfg.Project != "" {
		b.WriteString("- **Project:** " + cfg.Project + " (messages prefixed with `[" + cfg.Project + "]`)\n")
	} else {
		b.WriteString("- **Project:** (unknown — messages posted without prefix)\n")
	}
	b.WriteString("\n")

	b.WriteString("### Targeting a different channel\n\n")
	b.WriteString("Create a `.cupa.yaml` file in your project root:\n\n")
	b.WriteString("```yaml\n")
	b.WriteString("workspace_id: \"YOUR_WORKSPACE_ID\"\n")
	b.WriteString("channel_id: \"YOUR_CHANNEL_ID\"\n")
	b.WriteString("```\n\n")
	b.WriteString("The project name is auto-detected from the git remote (or directory name) and prefixed on all messages.\n\n")
	b.WriteString("To find these IDs:\n\n")
	b.WriteString("1. **Workspace ID:** ClickUp Settings → Workspaces → look at the URL: `app.clickup.com/{workspace_id}/...`\n")
	b.WriteString("2. **Channel ID:** Open the Chat channel in ClickUp → the channel ID is in the URL after `/chat/`\n")

	// Try a connectivity check if token is set
	if token != "" {
		b.WriteString("\n## Connectivity\n\n")
		req, reqErr := http.NewRequest(http.MethodGet, clickupBase+"/user", nil)
		if reqErr == nil {
			req.Header.Set("Authorization", token)
			resp, doErr := http.DefaultClient.Do(req)
			if doErr != nil {
				b.WriteString("- **API check:** FAILED — " + doErr.Error() + "\n")
			} else {
				_ = resp.Body.Close()
				if resp.StatusCode == 200 {
					b.WriteString("- **API check:** OK (authenticated)\n")
				} else {
					fmt.Fprintf(&b, "- **API check:** FAILED — HTTP %d (token may be invalid or expired)\n", resp.StatusCode)
				}
			}
		}
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}

// --- Tool: post_note ---

type postNoteArgs struct {
	Content string `json:"content" jsonschema:"Message content to post to the Agent Notes channel"`
}

func handlePostNote(ctx context.Context, _ *mcp.CallToolRequest, args postNoteArgs) (*mcp.CallToolResult, any, error) {
	if args.Content == "" {
		return toolError("content is required"), nil, nil
	}

	body := map[string]string{"content": prefixProject(args.Content)}
	data, err := clickupRequest(ctx, http.MethodPost, messagesPath(), body)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}

	var posted message
	if errResult := unmarshalResponse(data, &posted); errResult != nil {
		return errResult, nil, nil
	}

	// Return recent messages alongside confirmation so the agent has context.
	var result strings.Builder
	fmt.Fprintf(&result, "Posted message %s\n\n", posted.ID)

	recent, readErr := readMessages(ctx)
	if readErr == nil && len(recent) > 0 {
		recent = recent[:min(len(recent), 5)]
		result.WriteString("## Recent messages\n\n")
		result.WriteString(formatMessages(recent))
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: result.String()}},
	}, nil, nil
}

// --- Tool: read_notes ---

type readNotesArgs struct {
	Limit       int  `json:"limit" jsonschema:"Maximum number of messages to return (default 20)"`
	IncludeRead bool `json:"include_read" jsonschema:"Only set to true when the user explicitly asks to re-read old/previous messages. Default false -- returns only new unread messages."`
}

func handleReadNotes(ctx context.Context, _ *mcp.CallToolRequest, args readNotesArgs) (*mcp.CallToolResult, any, error) {
	// Record poll timestamp.
	lastRead.mu.Lock()
	lastRead.lastPoll = time.Now()
	lastRead.mu.Unlock()

	limit := args.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	messages, err := readMessages(ctx)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}

	// Determine the latest message ID before any filtering.
	var latestID int64
	if len(messages) > 0 {
		latestID, _ = messages[0].ID.Int64()
	}

	// Get the server-side cursor (skip if include_read).
	var afterID int64
	if !args.IncludeRead {
		lastRead.mu.Lock()
		afterID = lastRead.id
		lastRead.mu.Unlock()
	}

	// Advance the cursor to the latest message.
	if latestID > 0 {
		lastRead.mu.Lock()
		advanced := latestID > lastRead.id
		if advanced {
			lastRead.id = latestID
		}
		lastRead.mu.Unlock()
		if advanced {
			saveCursor(latestID)
		}
	}

	// If we have a cursor, filter and return chronological order.
	if afterID > 0 {
		messages = messagesAfter(messages, afterID)
		if len(messages) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "No new messages"}},
			}, nil, nil
		}
		slices.Reverse(messages)
		if len(messages) > limit {
			messages = messages[:limit]
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatMessages(messages)}},
		}, nil, nil
	}

	if len(messages) > limit {
		messages = messages[:limit]
	}

	if len(messages) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "No messages found"}},
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: formatMessages(messages)}},
	}, nil, nil
}

// --- Tool: post_content ---

// postSubtype holds a cached ClickUp post subtype.
type postSubtype struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func subtypesPath() string {
	return "/workspaces/" + cfg.WorkspaceID + "/comments/types/post/subtypes"
}

// fetchSubtypeID fetches available post subtypes and returns the "Update" subtype ID.
// Falls back to the first available subtype if "Update" isn't found.
func fetchSubtypeID() (string, error) {
	data, err := clickupRequest(context.Background(), http.MethodGet, subtypesPath(), nil)
	if err != nil {
		return "", fmt.Errorf("fetch post subtypes: %w", err)
	}

	// API may return a bare array, an object like {"subtypes": [...]},
	// or an object with a "data" key like {"data": [...]}.
	var subtypes []postSubtype
	if err := json.Unmarshal(data, &subtypes); err != nil {
		var wrapper struct {
			Subtypes []postSubtype `json:"subtypes"`
			Data     []postSubtype `json:"data"`
		}
		if err2 := json.Unmarshal(data, &wrapper); err2 != nil {
			return "", fmt.Errorf("parse post subtypes: %w (raw: %s)", err, truncate(data, 200))
		}
		subtypes = wrapper.Subtypes
		if len(subtypes) == 0 {
			subtypes = wrapper.Data
		}
	}
	if len(subtypes) == 0 {
		return "", fmt.Errorf("no post subtypes available in this workspace (raw: %s)", truncate(data, 200))
	}

	// Prefer "Update" as the default subtype for agent content sharing.
	chosen := subtypes[0]
	for _, st := range subtypes {
		if strings.EqualFold(st.Name, "Update") {
			chosen = st
			break
		}
	}

	return chosen.ID, nil
}

// truncate returns s limited to n bytes, appending "..." if truncated.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

var (
	subtypeIDOnce  sync.Once
	subtypeIDValue string
	subtypeIDErr   error
)

// resolveSubtypeID fetches and caches the subtype ID. On error, retries on subsequent calls.
func resolveSubtypeID() (string, error) {
	subtypeIDOnce.Do(func() {
		subtypeIDValue, subtypeIDErr = fetchSubtypeID()
	})
	if subtypeIDErr != nil {
		// Reset so next call retries.
		subtypeIDOnce = sync.Once{}
		return "", subtypeIDErr
	}
	return subtypeIDValue, nil
}

// resetSubtypeCache clears the cached subtype ID so the next call re-fetches.
func resetSubtypeCache() {
	subtypeIDOnce = sync.Once{}
	subtypeIDValue = ""
	subtypeIDErr = nil
}

type postContentArgs struct {
	Title   string `json:"title" jsonschema:"Title for the post (max 255 chars)"`
	Content string `json:"content" jsonschema:"Markdown content to share (max 40000 chars). Use for code snippets, logs, reports, or any structured text."`
}

func handlePostContent(ctx context.Context, _ *mcp.CallToolRequest, args postContentArgs) (*mcp.CallToolResult, any, error) {
	if args.Content == "" {
		return toolError("content is required"), nil, nil
	}
	if args.Title == "" {
		return toolError("title is required"), nil, nil
	}

	subtypeID, err := resolveSubtypeID()
	if err != nil {
		return toolError(err.Error()), nil, nil
	}

	title := prefixProject(args.Title)

	body := map[string]any{
		"type":           postTypePost,
		"content":        args.Content,
		"content_format": contentFormatMD,
		"post_data": map[string]any{
			"title":   title,
			"subtype": map[string]string{"id": subtypeID},
		},
	}

	data, err := clickupRequest(ctx, http.MethodPost, messagesPath(), body)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}

	var posted message
	if errResult := unmarshalResponse(data, &posted); errResult != nil {
		return errResult, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("Posted content \"%s\" (message id:%s)", title, posted.ID),
		}},
	}, nil, nil
}

// --- Tool: edit_note ---

type editNoteArgs struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message to edit"`
	Content   string `json:"content" jsonschema:"New content for the message"`
}

func handleEditNote(ctx context.Context, _ *mcp.CallToolRequest, args editNoteArgs) (*mcp.CallToolResult, any, error) {
	if args.MessageID == "" {
		return toolError("message_id is required"), nil, nil
	}
	if args.Content == "" {
		return toolError("content is required"), nil, nil
	}

	body := map[string]string{"content": args.Content}
	data, err := clickupRequest(ctx, http.MethodPatch, messagePath(args.MessageID), body)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}

	var edited message
	if errResult := unmarshalResponse(data, &edited); errResult != nil {
		return errResult, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("Edited message %s", edited.ID),
		}},
	}, nil, nil
}

// --- Tool: delete_note ---

type deleteNoteArgs struct {
	MessageID string `json:"message_id" jsonschema:"ID of the message to delete"`
}

func handleDeleteNote(ctx context.Context, _ *mcp.CallToolRequest, args deleteNoteArgs) (*mcp.CallToolResult, any, error) {
	if args.MessageID == "" {
		return toolError("message_id is required"), nil, nil
	}

	_, err := clickupRequest(ctx, http.MethodDelete, messagePath(args.MessageID), nil)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("Deleted message %s", args.MessageID),
		}},
	}, nil, nil
}

// --- Monitoring subsystem ---

const defaultMonitorInterval = 20 * time.Second

// mcpServer is set in main() so the monitor goroutine can send notifications.
var mcpServer *mcp.Server

var monitor struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
}

func isMonitoring() bool {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	return monitor.running
}

func startMonitor(interval time.Duration) bool {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if monitor.running {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	monitor.cancel = cancel
	monitor.running = true
	go monitorLoop(ctx, interval)
	return true
}

func stopMonitor() bool {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if !monitor.running {
		return false
	}
	monitor.cancel()
	monitor.cancel = nil
	monitor.running = false
	return true
}

func monitorLoop(ctx context.Context, interval time.Duration) {
	defer func() {
		monitor.mu.Lock()
		monitor.running = false
		monitor.mu.Unlock()
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msgs, err := readMessages(ctx)
			if err != nil {
				log.Printf("monitor: read error: %v", err)
				continue
			}

			lastRead.mu.Lock()
			afterID := lastRead.id
			lastRead.mu.Unlock()

			newMsgs := messagesAfter(msgs, afterID)
			if len(newMsgs) == 0 {
				continue
			}

			// Update cursor.
			var latestID int64
			for _, m := range newMsgs {
				if id, _ := m.ID.Int64(); id > latestID {
					latestID = id
				}
			}
			if latestID > 0 {
				lastRead.mu.Lock()
				if latestID > lastRead.id {
					lastRead.id = latestID
				}
				lastRead.lastPoll = time.Now()
				lastRead.mu.Unlock()
				saveCursor(latestID)
			}

			// Format and push to all sessions via logging notification.
			slices.Reverse(newMsgs)
			text := fmt.Sprintf("New agent notes (%d):\n\n%s", len(newMsgs), formatMessages(newMsgs))

			for ss := range mcpServer.Sessions() {
				if err := ss.Log(ctx, &mcp.LoggingMessageParams{
					Level:  "notice",
					Logger: "monitor",
					Data:   text,
				}); err != nil {
					log.Printf("monitor: log notify error: %v", err)
				}
			}
		}
	}
}

// --- Tool: start_monitoring ---

type startMonitoringArgs struct {
	Interval int `json:"interval" jsonschema:"Polling interval in seconds (default 20, min 10)"`
}

func handleStartMonitoring(ctx context.Context, _ *mcp.CallToolRequest, args startMonitoringArgs) (*mcp.CallToolResult, any, error) {
	if isMonitoring() {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Already monitoring — call stop_monitoring first to restart with different settings"}},
		}, nil, nil
	}

	interval := time.Duration(args.Interval) * time.Second
	if interval < 10*time.Second {
		interval = defaultMonitorInterval
	}

	// Do an initial read to establish the cursor before starting the monitor.
	msgs, err := readMessages(ctx)
	if err != nil {
		return toolError(fmt.Sprintf("Failed to read initial messages: %v", err)), nil, nil
	}

	// Set cursor to latest message so monitor only reports truly new messages.
	if len(msgs) > 0 {
		if latestID, _ := msgs[0].ID.Int64(); latestID > 0 {
			lastRead.mu.Lock()
			if latestID > lastRead.id {
				lastRead.id = latestID
			}
			lastRead.lastPoll = time.Now()
			lastRead.mu.Unlock()
			saveCursor(latestID)
		}
	}

	startMonitor(interval)

	var result strings.Builder
	fmt.Fprintf(&result, "Monitoring started (polling every %s). New messages will be delivered via notifications.\n", interval)
	fmt.Fprintf(&result, "Call stop_monitoring to stop.\n\n")

	// Return current messages for context.
	if len(msgs) > 0 {
		show := msgs[:min(len(msgs), 5)]
		result.WriteString("## Recent messages\n\n")
		result.WriteString(formatMessages(show))
	} else {
		result.WriteString("No messages yet.")
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: result.String()}},
	}, nil, nil
}

// --- Tool: await_messages ---

type awaitMessagesArgs struct {
	Interval int `json:"interval" jsonschema:"Polling interval in seconds (default 20, min 10)"`
}

func handleAwaitMessages(ctx context.Context, _ *mcp.CallToolRequest, args awaitMessagesArgs) (*mcp.CallToolResult, any, error) {
	interval := time.Duration(args.Interval) * time.Second
	if interval < 10*time.Second {
		interval = defaultMonitorInterval
	}

	// Snapshot the cursor at call time so we don't race with the monitor goroutine.
	lastRead.mu.Lock()
	afterID := lastRead.id
	lastRead.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "Stopped waiting (session ended)"}},
			}, nil, nil
		case <-ticker.C:
			msgs, err := readMessages(ctx)
			if err != nil {
				log.Printf("await_messages: read error: %v", err)
				continue
			}

			newMsgs := messagesAfter(msgs, afterID)
			if len(newMsgs) == 0 {
				continue
			}

			// Update the shared cursor.
			var latestID int64
			for _, m := range newMsgs {
				if id, _ := m.ID.Int64(); id > latestID {
					latestID = id
				}
			}
			if latestID > 0 {
				lastRead.mu.Lock()
				if latestID > lastRead.id {
					lastRead.id = latestID
				}
				lastRead.lastPoll = time.Now()
				lastRead.mu.Unlock()
				saveCursor(latestID)
			}

			slices.Reverse(newMsgs)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{
					Text: fmt.Sprintf("New messages (%d):\n\n%s", len(newMsgs), formatMessages(newMsgs)),
				}},
			}, nil, nil
		}
	}
}

// --- Tool: stop_monitoring ---

type stopMonitoringArgs struct{}

func handleStopMonitoring(_ context.Context, _ *mcp.CallToolRequest, _ stopMonitoringArgs) (*mcp.CallToolResult, any, error) {
	if stopMonitor() {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Monitoring stopped"}},
		}, nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Not currently monitoring"}},
	}, nil, nil
}

// --- Tool: poll_status ---

type pollStatusArgs struct{}

func handlePollStatus(_ context.Context, _ *mcp.CallToolRequest, _ pollStatusArgs) (*mcp.CallToolResult, any, error) {
	lastRead.mu.Lock()
	lastPoll := lastRead.lastPoll
	lastRead.mu.Unlock()

	monitoring := isMonitoring()

	var status string
	if monitoring {
		if lastPoll.IsZero() {
			status = "Monitoring active — no messages received yet"
		} else {
			ago := time.Since(lastPoll).Truncate(time.Second)
			status = fmt.Sprintf("Monitoring active — last checked %s ago", ago)
		}
	} else {
		if lastPoll.IsZero() {
			status = "Inactive — call start_monitoring to begin"
		} else {
			ago := time.Since(lastPoll).Truncate(time.Second)
			status = fmt.Sprintf("Inactive — monitoring stopped, last checked %s ago. Call start_monitoring to resume.", ago)
		}
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: status}},
	}, nil, nil
}

func main() {
	log.SetOutput(os.Stderr)

	if err := loadConfig(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	loadCursor()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "cupa",
		Version: "0.10.0",
	}, &mcp.ServerOptions{
		Instructions: "cupa provides cross-agent messaging via ClickUp Chat. " +
			"Use post_note to send messages and read_notes to check for messages. " +
			"Use post_content to share rich markdown content (code, logs, reports) as a titled post. " +
			"Messages are automatically prefixed with the project name. " +
			"If you encounter auth or config errors, use check_setup for guided diagnostics.\n\n" +
			"IMPORTANT: On session start, call start_monitoring to begin watching for new messages. " +
			"The server polls ClickUp automatically and delivers new messages via logging notifications. " +
			"Monitoring runs until you call stop_monitoring or the session ends — no background sub-agents needed. " +
			"Use read_notes for on-demand reading. Use poll_status to check monitoring state.\n\n" +
			"When working with tool results, write down any important information you might need later " +
			"in your response, as the original tool result may be cleared later.",
	})
	mcpServer = server

	mcp.AddTool(server, &mcp.Tool{
		Name:        "check_setup",
		Description: "Check cupa configuration status and show setup instructions for ClickUp token, workspace, and channel",
	}, handleCheckSetup)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "post_note",
		Description: "Post a message to the Agent Notes ClickUp channel",
	}, handlePostNote)

	mcp.AddTool(server, &mcp.Tool{
		Name: "read_notes",
		Description: "Read messages from the Agent Notes channel. " +
			"The server tracks your read position automatically — " +
			"the first call returns existing messages, subsequent calls return only new messages. " +
			"Always use include_read: false (the default) unless the user explicitly asks to re-read old messages. " +
			"Requests like \"read notes\", \"check agent notes\", or \"read latest\" mean \"show me what's new\" — use include_read: false.",
	}, handleReadNotes)

	mcp.AddTool(server, &mcp.Tool{
		Name: "post_content",
		Description: "Share rich markdown content as a titled post in the Agent Notes channel. " +
			"Use this for code snippets, logs, error output, reports, or any structured text that benefits from a title and formatting. " +
			"Content supports markdown (up to 40000 chars). For short plain-text messages, use post_note instead.",
	}, handlePostContent)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "edit_note",
		Description: "Edit a previously posted message. Use the message ID from read_notes or post_note output.",
	}, handleEditNote)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_note",
		Description: "Delete a previously posted message. Use the message ID from read_notes or post_note output.",
	}, handleDeleteNote)

	mcp.AddTool(server, &mcp.Tool{
		Name: "start_monitoring",
		Description: "Start monitoring the Agent Notes channel for new messages. " +
			"The server polls ClickUp in the background and delivers new messages via logging notifications. " +
			"Runs until stop_monitoring is called or the session ends. Call this on session start.",
	}, handleStartMonitoring)

	mcp.AddTool(server, &mcp.Tool{
		Name: "await_messages",
		Description: "Block until new messages arrive in the Agent Notes channel, then return them. " +
			"The server polls ClickUp internally — the caller just waits. " +
			"Use this in a background sub-agent for reliable message delivery. " +
			"When it returns, process the messages and call await_messages again.",
	}, handleAwaitMessages)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "stop_monitoring",
		Description: "Stop monitoring the Agent Notes channel. The background polling goroutine is terminated.",
	}, handleStopMonitoring)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "poll_status",
		Description: "Check monitoring status — whether the background monitor is active and when it last checked for messages.",
	}, handlePollStatus)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
