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

// postTestNote posts a message and registers it for cleanup (edit then delete).
func postTestNote(t *testing.T, ctx context.Context, ids *[]string, content string) {
	t.Helper()
	result, _, err := handlePostNote(ctx, nil, postNoteArgs{Content: content})
	if err != nil {
		t.Fatalf("post %q: %v", content, err)
	}
	if result.IsError {
		t.Fatalf("post %q error: %s", content, result.Content[0].(*mcp.TextContent).Text)
	}
	// Extract message ID from "Posted message NNNN" response.
	text := result.Content[0].(*mcp.TextContent).Text
	if id, ok := strings.CutPrefix(text, "Posted message "); ok {
		if id, _, ok := strings.Cut(id, "\n"); ok {
			*ids = append(*ids, id)
		}
	}
}

// cleanupMessages edits each message to "[deleted by test]" then deletes it.
func cleanupMessages(t *testing.T, ctx context.Context, ids []string) {
	t.Helper()
	for _, id := range ids {
		// Edit first to validate edit_note works.
		result, _, err := handleEditNote(ctx, nil, editNoteArgs{
			MessageID: id,
			Content:   "[deleted by test]",
		})
		if err != nil {
			t.Logf("cleanup edit %s: %v", id, err)
		} else if result.IsError {
			t.Logf("cleanup edit %s: %s", id, result.Content[0].(*mcp.TextContent).Text)
		}

		// Delete.
		result, _, err = handleDeleteNote(ctx, nil, deleteNoteArgs{MessageID: id})
		if err != nil {
			t.Logf("cleanup delete %s: %v", id, err)
		} else if result.IsError {
			t.Logf("cleanup delete %s: %s", id, result.Content[0].(*mcp.TextContent).Text)
		}
	}
}

func TestIntegrationPollingCycle(t *testing.T) {
	skipWithoutToken(t)
	resetLastRead()

	ctx := context.Background()
	tag := fmt.Sprintf("integration-test-%d", time.Now().UnixMilli())
	var messageIDs []string
	t.Cleanup(func() { cleanupMessages(t, ctx, messageIDs) })

	// Step 1: Post an initial message.
	postTestNote(t, ctx, &messageIDs, tag+" msg-1")
	t.Logf("posted msg-1")

	// Step 2: First read — establishes cursor, should see msg-1.
	result, _, err := handleReadNotes(ctx, nil, readNotesArgs{})
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
	postTestNote(t, ctx, &messageIDs, tag+" msg-2")
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
	var messageIDs []string
	t.Cleanup(func() { cleanupMessages(t, ctx, messageIDs) })

	// Post a message.
	postTestNote(t, ctx, &messageIDs, tag+" history")

	// Read to advance cursor.
	_, _, err := handleReadNotes(ctx, nil, readNotesArgs{})
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}

	// Normal poll — no new messages.
	result, _, err := handleReadNotes(ctx, nil, readNotesArgs{})
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

func TestIntegrationEditAndDelete(t *testing.T) {
	skipWithoutToken(t)

	ctx := context.Background()
	tag := fmt.Sprintf("integration-test-%d", time.Now().UnixMilli())
	var messageIDs []string
	t.Cleanup(func() { cleanupMessages(t, ctx, messageIDs) })

	// Post a message.
	postTestNote(t, ctx, &messageIDs, tag+" to-edit")
	if len(messageIDs) == 0 {
		t.Fatal("no message ID captured")
	}
	msgID := messageIDs[0]
	t.Logf("posted message %s", msgID)

	// Edit it.
	result, _, err := handleEditNote(ctx, nil, editNoteArgs{
		MessageID: msgID,
		Content:   tag + " edited",
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if result.IsError {
		t.Fatalf("edit error: %s", result.Content[0].(*mcp.TextContent).Text)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, msgID) {
		t.Fatalf("edit response should contain message ID, got: %s", text)
	}
	t.Logf("edited message — correct")

	// Delete it (cleanup will also try, but that's fine).
	result, _, err = handleDeleteNote(ctx, nil, deleteNoteArgs{MessageID: msgID})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if result.IsError {
		t.Fatalf("delete error: %s", result.Content[0].(*mcp.TextContent).Text)
	}
	text = result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, msgID) {
		t.Fatalf("delete response should contain message ID, got: %s", text)
	}
	t.Logf("deleted message — correct")

	// Clear from cleanup list since already deleted.
	messageIDs = nil
}
