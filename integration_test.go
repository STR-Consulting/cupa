package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Integration tests hit the real ClickUp API. Run with:
//
//	CLICKUP_TOKEN=pk_... go test -run TestIntegration -count=1
//
// Skipped automatically when CLICKUP_TOKEN is not set.

func skipWithoutToken(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	loadConfigForTest(t)
}

// loadConfigForTest loads config and verifies ClickUp connectivity.
func loadConfigForTest(t *testing.T) {
	t.Helper()
	if err := loadConfig(); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	ctx := context.Background()
	_, err := readMessages(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "CLICKUP_TOKEN not set") {
			t.Skip("CLICKUP_TOKEN not set; skipping integration test")
		}
		t.Fatalf("connectivity check failed: %v", err)
	}
}

func TestIntegrationPollingCycle(t *testing.T) {
	skipWithoutToken(t)
	resetLastRead()

	ctx := context.Background()
	tag := fmt.Sprintf("integration-test-%d", time.Now().UnixMilli())

	// Step 1: Post an initial message.
	result, _, err := handlePostNote(ctx, nil, postNoteArgs{Content: tag + " msg-1"})
	if err != nil {
		t.Fatalf("post msg-1: %v", err)
	}
	if result.IsError {
		t.Fatalf("post msg-1 error: %s", result.Content[0].(*mcp.TextContent).Text)
	}
	t.Logf("posted msg-1")

	// Step 2: First read — establishes cursor, should see msg-1.
	result, _, err = handleReadNotes(ctx, nil, readNotesArgs{})
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, tag+" msg-1") {
		t.Fatalf("initial read should contain msg-1, got:\n%s", text)
	}
	t.Logf("initial read OK — cursor established")

	// Step 3: Poll — should see no new messages.
	result, _, err = handleReadNotes(ctx, nil, readNotesArgs{})
	if err != nil {
		t.Fatalf("poll (no new): %v", err)
	}
	text = result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "No new messages") {
		t.Fatalf("expected no new messages, got:\n%s", text)
	}
	t.Logf("poll returned no new messages — correct")

	// Step 4: Post a second message.
	result, _, err = handlePostNote(ctx, nil, postNoteArgs{Content: tag + " msg-2"})
	if err != nil {
		t.Fatalf("post msg-2: %v", err)
	}
	if result.IsError {
		t.Fatalf("post msg-2 error: %s", result.Content[0].(*mcp.TextContent).Text)
	}
	t.Logf("posted msg-2")

	// Step 5: Poll — should see only msg-2, not msg-1.
	result, _, err = handleReadNotes(ctx, nil, readNotesArgs{})
	if err != nil {
		t.Fatalf("poll (after msg-2): %v", err)
	}
	text = result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, tag+" msg-2") {
		t.Fatalf("poll should contain msg-2, got:\n%s", text)
	}
	if strings.Contains(text, tag+" msg-1") {
		t.Fatalf("poll should NOT contain msg-1, got:\n%s", text)
	}
	t.Logf("poll returned only msg-2 — cursor tracking works")

	// Step 6: Poll again — should be empty.
	result, _, err = handleReadNotes(ctx, nil, readNotesArgs{})
	if err != nil {
		t.Fatalf("final poll: %v", err)
	}
	text = result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "No new messages") {
		t.Fatalf("final poll should show no new messages, got:\n%s", text)
	}
	t.Logf("final poll empty — all good")
}

func TestIntegrationIncludeRead(t *testing.T) {
	skipWithoutToken(t)
	resetLastRead()

	ctx := context.Background()
	tag := fmt.Sprintf("integration-test-%d", time.Now().UnixMilli())

	// Post a message.
	result, _, err := handlePostNote(ctx, nil, postNoteArgs{Content: tag + " history"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if result.IsError {
		t.Fatalf("post error: %s", result.Content[0].(*mcp.TextContent).Text)
	}

	// Read to advance cursor.
	_, _, err = handleReadNotes(ctx, nil, readNotesArgs{})
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}

	// Normal poll — no new messages.
	result, _, err = handleReadNotes(ctx, nil, readNotesArgs{})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "No new messages") {
		t.Fatalf("expected no new messages, got:\n%s", text)
	}

	// include_read — should see the message again.
	result, _, err = handleReadNotes(ctx, nil, readNotesArgs{IncludeRead: true})
	if err != nil {
		t.Fatalf("include_read: %v", err)
	}
	text = result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, tag+" history") {
		t.Fatalf("include_read should return old messages, got:\n%s", text)
	}
	t.Logf("include_read returned old messages — correct")

	// Next normal poll should still be empty (cursor wasn't broken).
	result, _, err = handleReadNotes(ctx, nil, readNotesArgs{})
	if err != nil {
		t.Fatalf("post-include poll: %v", err)
	}
	text = result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "No new messages") {
		t.Fatalf("post-include poll should be empty, got:\n%s", text)
	}
	t.Logf("cursor intact after include_read — all good")
}
