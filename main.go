package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	clickupBase    = "https://api.clickup.com/api/v3"
	workspaceID    = "9011518645"
	channelID      = "6-901113290332-8"
	messagesPath   = "/workspaces/" + workspaceID + "/chat/channels/" + channelID + "/messages"
	minRequestGap  = 700 * time.Millisecond // ~1.5 req/s
	defaultLimit   = 20
	defaultTimeout = 60
	pollInterval   = 5 * time.Second
)

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
		var apiErr struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		}
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
			return nil, fmt.Errorf("ClickUp API error %d: %s", resp.StatusCode, apiErr.Message)
		}
		return nil, fmt.Errorf("ClickUp API error %d: %s", resp.StatusCode, respBody)
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
	data, err := clickupRequest(ctx, http.MethodGet, messagesPath, nil)
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

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

// --- Tool: post_note ---

type postNoteArgs struct {
	Content string `json:"content" jsonschema:"Message content to post to the Agent Notes channel"`
}

func handlePostNote(ctx context.Context, _ *mcp.CallToolRequest, args postNoteArgs) (*mcp.CallToolResult, any, error) {
	if args.Content == "" {
		return toolError("content is required"), nil, nil
	}

	body := map[string]string{"content": args.Content}
	data, err := clickupRequest(ctx, http.MethodPost, messagesPath, body)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}

	var posted message
	if err := json.Unmarshal(data, &posted); err != nil {
		return toolError(fmt.Sprintf("parse response: %v", err)), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Posted message %s", posted.ID)},
		},
	}, nil, nil
}

// --- Tool: read_notes ---

type readNotesArgs struct {
	Limit int `json:"limit" jsonschema:"Maximum number of messages to return (default 20)"`
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

// --- Tool: wait_for_reply ---

type waitForReplyArgs struct {
	AfterMessageID int64 `json:"after_message_id" jsonschema:"Wait for messages with ID greater than this"`
	TimeoutSeconds int   `json:"timeout_seconds" jsonschema:"How long to wait in seconds (default 60)"`
}

func handleWaitForReply(ctx context.Context, _ *mcp.CallToolRequest, args waitForReplyArgs) (*mcp.CallToolResult, any, error) {
	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	deadline := time.After(time.Duration(timeout) * time.Second)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		messages, err := readMessages(ctx)
		if err != nil {
			return toolError(err.Error()), nil, nil
		}

		// Messages are newest-first; find any with ID > after_message_id.
		var newMessages []message
		for _, m := range messages {
			id, _ := m.ID.Int64()
			if id > args.AfterMessageID {
				newMessages = append(newMessages, m)
			}
		}

		if len(newMessages) > 0 {
			// Return in chronological order (reverse of newest-first).
			slices.Reverse(newMessages)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: formatMessages(newMessages)}},
			}, nil, nil
		}

		select {
		case <-ctx.Done():
			return toolError("cancelled"), nil, nil
		case <-deadline:
			return toolError(fmt.Sprintf("No new messages after %d seconds", timeout)), nil, nil
		case <-ticker.C:
			// Poll again.
		}
	}
}

func main() {
	log.SetOutput(os.Stderr)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "cupa",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "post_note",
		Description: "Post a message to the Agent Notes ClickUp channel",
	}, handlePostNote)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_notes",
		Description: "Read recent messages from the Agent Notes channel (newest first)",
	}, handleReadNotes)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "wait_for_reply",
		Description: "Long-poll until a new message appears after a given message ID",
	}, handleWaitForReply)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
