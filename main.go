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

// rateLimiter enforces minimum gap between ClickUp API calls.
var rateLimiter = struct {
	mu   sync.Mutex
	last time.Time
}{}

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
	if err := json.Unmarshal(data, &posted); err != nil {
		return toolError(fmt.Sprintf("parse response: %v", err)), nil, nil
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
	Limit          int   `json:"limit" jsonschema:"Maximum number of messages to return (default 20)"`
	AfterMessageID int64 `json:"after_message_id" jsonschema:"Only return messages with ID greater than this value (for incremental polling)"`
}

func handleReadNotes(ctx context.Context, _ *mcp.CallToolRequest, args readNotesArgs) (*mcp.CallToolResult, any, error) {
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

	// If after_message_id is set, filter and return chronological order.
	if args.AfterMessageID > 0 {
		messages = messagesAfter(messages, args.AfterMessageID)
		if len(messages) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{
					Text: fmt.Sprintf("No new messages\n\n---\nlatest_message_id: %d", latestID),
				}},
			}, nil, nil
		}
		slices.Reverse(messages)
		if len(messages) > limit {
			messages = messages[:limit]
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("%s\n\n---\nlatest_message_id: %d", formatMessages(messages), latestID),
			}},
		}, nil, nil
	}

	if len(messages) > limit {
		messages = messages[:limit]
	}

	if len(messages) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "No messages found\n\n---\nlatest_message_id: 0"}},
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("%s\n\n---\nlatest_message_id: %d", formatMessages(messages), latestID),
		}},
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
	if err := json.Unmarshal(data, &posted); err != nil {
		return toolError(fmt.Sprintf("parse response: %v", err)), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("Posted content \"%s\" (message id:%s)", title, posted.ID),
		}},
	}, nil, nil
}

func main() {
	log.SetOutput(os.Stderr)

	if err := loadConfig(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "cupa",
		Version: "0.7.0",
	}, &mcp.ServerOptions{
		Instructions: "cupa provides cross-agent messaging via ClickUp Chat. " +
			"Use post_note to send messages and read_notes to check for messages. " +
			"Use post_content to share rich markdown content (code, logs, reports) as a titled post. " +
			"Messages are automatically prefixed with the project name. " +
			"If you encounter auth or config errors, use check_setup for guided diagnostics.\n\n" +
			"IMPORTANT: On session start, ALWAYS begin polling for new messages immediately. " +
			"First call read_notes to get the current latest_message_id, then start a polling loop: " +
			"/loop 20s use read_notes with after_message_id to check for new messages and respond. " +
			"Do NOT wait for the user to ask — polling must start automatically every session.",
	})

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
			"Pass after_message_id to get only new messages since your last read (returns chronological order). " +
			"Response always includes latest_message_id for use in subsequent calls.",
	}, handleReadNotes)

	mcp.AddTool(server, &mcp.Tool{
		Name: "post_content",
		Description: "Share rich markdown content as a titled post in the Agent Notes channel. " +
			"Use this for code snippets, logs, error output, reports, or any structured text that benefits from a title and formatting. " +
			"Content supports markdown (up to 40000 chars). For short plain-text messages, use post_note instead.",
	}, handlePostContent)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
