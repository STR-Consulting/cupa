package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFormatMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  message
		want string
	}{
		{
			name: "basic message",
			msg: message{
				ID:      json.Number("12345"),
				Content: "hello world",
				Date:    json.Number("1704067200000"), // 2024-01-01T00:00:00Z
				UserID:  "user1",
			},
			want: "[2024-01-01T00:00:00Z] user:user1 (id:12345)\nhello world",
		},
		{
			name: "empty content",
			msg: message{
				ID:      json.Number("1"),
				Content: "",
				Date:    json.Number("0"),
				UserID:  "u",
			},
			want: "[1970-01-01T00:00:00Z] user:u (id:1)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMessage(tt.msg)
			if got != tt.want {
				t.Errorf("formatMessage() =\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFormatMessages(t *testing.T) {
	msgs := []message{
		{ID: json.Number("1"), Content: "first", Date: json.Number("0"), UserID: "a"},
		{ID: json.Number("2"), Content: "second", Date: json.Number("0"), UserID: "b"},
	}
	got := formatMessages(msgs)
	want := formatMessage(msgs[0]) + "\n---\n" + formatMessage(msgs[1])
	if got != want {
		t.Errorf("formatMessages() =\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatMessagesEmpty(t *testing.T) {
	got := formatMessages(nil)
	if got != "" {
		t.Errorf("formatMessages(nil) = %q, want empty", got)
	}
}

// withTestServer sets up a mock ClickUp API server and configures the module
// globals so clickupRequest hits it instead of the real API.
func withTestServer(t *testing.T, handler http.Handler) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Override the package-level base URL by swapping clickupBase.
	// Since clickupBase is a const, we use a different approach: override
	// via a test HTTP client. Instead, we'll test the handlers by calling
	// the ClickUp mock directly through the tool handlers.
	//
	// The simplest approach: temporarily point the default HTTP client's
	// transport to redirect requests to our test server.
	origTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = &rewriteTransport{base: srv.URL}
	t.Cleanup(func() { http.DefaultClient.Transport = origTransport })

	// Set the token so clickupRequest doesn't bail early.
	t.Setenv("CLICKUP_TOKEN", "test-token")

	// Reset rate limiter so tests don't wait.
	rateLimiter.mu.Lock()
	rateLimiter.last = time.Time{}
	rateLimiter.mu.Unlock()
}

// rewriteTransport redirects all requests to the test server.
type rewriteTransport struct {
	base string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.base[len("http://"):]
	return http.DefaultTransport.RoundTrip(req)
}

func TestHandlePostNote(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if r.Header.Get("Authorization") != "test-token" {
				t.Errorf("expected raw token auth, got %q", r.Header.Get("Authorization"))
			}
			var body struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Content != "hello" {
				t.Errorf("expected content 'hello', got %q", body.Content)
			}
			resp := message{ID: json.Number("999"), Content: body.Content, Date: json.Number("1704067200000"), UserID: "u1"}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// GET: return recent messages for the post-send context.
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []message{
			{ID: json.Number("999"), Content: "hello", Date: json.Number("1704067200000"), UserID: "u1"},
		}})
	}))

	result, _, err := handlePostNote(context.Background(), nil, postNoteArgs{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !contains(text, "Posted message 999") {
		t.Errorf("expected 'Posted message 999' in result, got: %s", text)
	}
	if !contains(text, "Recent messages") {
		t.Errorf("expected recent messages section in result, got: %s", text)
	}
}

func TestHandlePostNoteWithProject(t *testing.T) {
	oldProject := cfg.Project
	cfg.Project = "myapp"
	t.Cleanup(func() { cfg.Project = oldProject })

	var postedContent string
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			postedContent = body.Content
			resp := message{ID: json.Number("1000"), Content: body.Content, Date: json.Number("1704067200000"), UserID: "u1"}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []message{}})
	}))

	result, _, err := handlePostNote(context.Background(), nil, postNoteArgs{Content: "status update"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	if postedContent != "[myapp] status update" {
		t.Errorf("expected project prefix, got: %s", postedContent)
	}
}

func TestHandlePostNoteEmptyContent(t *testing.T) {
	t.Setenv("CLICKUP_TOKEN", "test-token")
	result, _, err := handlePostNote(context.Background(), nil, postNoteArgs{Content: ""})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for empty content")
	}
}

func TestHandleReadNotes(t *testing.T) {
	msgs := []message{
		{ID: json.Number("3"), Content: "newest", Date: json.Number("1704067200000"), UserID: "a"},
		{ID: json.Number("2"), Content: "middle", Date: json.Number("1704067100000"), UserID: "b"},
		{ID: json.Number("1"), Content: "oldest", Date: json.Number("1704067000000"), UserID: "c"},
	}
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": msgs})
	}))

	result, _, err := handleReadNotes(context.Background(), nil, readNotesArgs{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	// Should contain only the first 2 messages (newest and middle).
	if !contains(text, "newest") || !contains(text, "middle") {
		t.Errorf("expected newest and middle, got: %s", text)
	}
	if contains(text, "oldest") {
		t.Errorf("should not contain oldest with limit=2, got: %s", text)
	}
}

func TestHandleReadNotesDefaultLimit(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []message{}})
	}))

	result, _, err := handleReadNotes(context.Background(), nil, readNotesArgs{Limit: 0})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if text != "No messages found" {
		t.Errorf("expected 'No messages found', got: %s", text)
	}
}

func TestHandleWaitForReplyFound(t *testing.T) {
	msgs := []message{
		{ID: json.Number("200"), Content: "new reply", Date: json.Number("1704067200000"), UserID: "a"},
		{ID: json.Number("100"), Content: "old", Date: json.Number("1704067100000"), UserID: "b"},
	}
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": msgs})
	}))

	result, _, err := handleWaitForReply(context.Background(), nil, waitForReplyArgs{
		AfterMessageID: 150,
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !contains(text, "new reply") {
		t.Errorf("expected 'new reply' in result, got: %s", text)
	}
	if contains(text, "old") {
		t.Errorf("should not contain old message, got: %s", text)
	}
}

func TestHandleWaitForReplyTimeout(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []message{
			{ID: json.Number("50"), Content: "old", Date: json.Number("0"), UserID: "a"},
		}})
	}))

	result, _, err := handleWaitForReply(context.Background(), nil, waitForReplyArgs{
		AfterMessageID: 100,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected timeout error")
	}
}

func TestHandleWaitForReplyCancelled(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []message{}})
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	result, _, err := handleWaitForReply(ctx, nil, waitForReplyArgs{
		AfterMessageID: 100,
		TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected cancellation error")
	}
}

func TestClickupRequestMissingToken(t *testing.T) {
	t.Setenv("CLICKUP_TOKEN", "")
	_, err := clickupRequest(context.Background(), http.MethodGet, "/test", nil)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !contains(err.Error(), "CLICKUP_TOKEN not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClickupRequestAPIError(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 401, "message": "Token invalid"})
	}))

	_, err := clickupRequest(context.Background(), http.MethodGet, messagesPath(), nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !contains(err.Error(), "Token invalid") || !contains(err.Error(), "check_setup") {
		t.Errorf("expected actionable 401 error with 'Token invalid' and 'check_setup', got: %v", err)
	}
}

func TestHandlePostContent(t *testing.T) {
	oldProject := cfg.Project
	cfg.Project = "myapp"
	t.Cleanup(func() { cfg.Project = oldProject })

	// Reset the OnceValues so it re-fetches.
	resetSubtypeCache()

	var postedBody map[string]any
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Return subtypes.
			subtypes := []postSubtype{
				{ID: "sub-1", Name: "Announcement"},
				{ID: "sub-2", Name: "Update"},
				{ID: "sub-3", Name: "Idea"},
			}
			_ = json.NewEncoder(w).Encode(subtypes)
			return
		}
		// POST: capture the body.
		if err := json.NewDecoder(r.Body).Decode(&postedBody); err != nil {
			t.Fatal(err)
		}
		resp := message{ID: json.Number("800"), Content: "posted", Date: json.Number("1704067200000"), UserID: "u1"}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	result, _, err := handlePostContent(context.Background(), nil, postContentArgs{
		Title:   "Build Report",
		Content: "## Results\n\n- All tests passed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !contains(text, "[myapp] Build Report") {
		t.Errorf("expected project-prefixed title in result, got: %s", text)
	}
	if !contains(text, "800") {
		t.Errorf("expected message ID in result, got: %s", text)
	}

	// Verify the posted body structure.
	if postedBody["type"] != "post" {
		t.Errorf("expected type=post, got: %v", postedBody["type"])
	}
	if postedBody["content_format"] != "text/md" {
		t.Errorf("expected content_format=text/md, got: %v", postedBody["content_format"])
	}
	postData, ok := postedBody["post_data"].(map[string]any)
	if !ok {
		t.Fatalf("expected post_data map, got: %T", postedBody["post_data"])
	}
	if postData["title"] != "[myapp] Build Report" {
		t.Errorf("expected prefixed title, got: %v", postData["title"])
	}
	subtype, ok := postData["subtype"].(map[string]any)
	if !ok {
		t.Fatalf("expected subtype map, got: %T", postData["subtype"])
	}
	if subtype["id"] != "sub-2" {
		t.Errorf("expected Update subtype sub-2, got: %v", subtype["id"])
	}
}

func TestHandlePostContentEmptyFields(t *testing.T) {
	t.Setenv("CLICKUP_TOKEN", "test-token")

	result, _, err := handlePostContent(context.Background(), nil, postContentArgs{Title: "", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for empty title")
	}

	result, _, err = handlePostContent(context.Background(), nil, postContentArgs{Title: "x", Content: ""})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for empty content")
	}
}

func TestHandlePostContentCachesSubtype(t *testing.T) {
	resetSubtypeCache()

	subtypeFetches := 0
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			subtypeFetches++
			_ = json.NewEncoder(w).Encode([]postSubtype{{ID: "cached-id", Name: "Update"}})
			return
		}
		resp := message{ID: json.Number("801"), Content: "ok", Date: json.Number("0"), UserID: "u1"}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	// Call twice — subtype should only be fetched once.
	for range 2 {
		result, _, err := handlePostContent(context.Background(), nil, postContentArgs{
			Title:   "Test",
			Content: "body",
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %v", result.Content)
		}
	}

	if subtypeFetches != 1 {
		t.Errorf("expected 1 subtype fetch, got %d", subtypeFetches)
	}
}

func TestHandlePostContentWrappedSubtypes(t *testing.T) {
	resetSubtypeCache()

	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Return subtypes wrapped in an object (as the real API does).
			_ = json.NewEncoder(w).Encode(map[string]any{
				"subtypes": []postSubtype{{ID: "wrapped-id", Name: "Update"}},
			})
			return
		}
		resp := message{ID: json.Number("901"), Content: "ok", Date: json.Number("0"), UserID: "u1"}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	result, _, err := handlePostContent(context.Background(), nil, postContentArgs{
		Title:   "Test",
		Content: "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestHandlePostContentDataWrappedSubtypes(t *testing.T) {
	resetSubtypeCache()

	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Return subtypes wrapped in a "data" key.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []postSubtype{{ID: "data-id", Name: "Announcement"}},
			})
			return
		}
		resp := message{ID: json.Number("902"), Content: "ok", Date: json.Number("0"), UserID: "u1"}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	result, _, err := handlePostContent(context.Background(), nil, postContentArgs{
		Title:   "Test",
		Content: "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestHandlePostContentRetriesOnFailure(t *testing.T) {
	resetSubtypeCache()

	calls := 0
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			calls++
			if calls == 1 {
				// First call: return empty subtypes.
				_ = json.NewEncoder(w).Encode(map[string]any{"subtypes": []postSubtype{}})
				return
			}
			// Second call: return valid subtypes.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"subtypes": []postSubtype{{ID: "retry-id", Name: "Update"}},
			})
			return
		}
		resp := message{ID: json.Number("903"), Content: "ok", Date: json.Number("0"), UserID: "u1"}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	// First call should fail.
	result, _, err := handlePostContent(context.Background(), nil, postContentArgs{
		Title:   "Test",
		Content: "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error on first call with empty subtypes")
	}

	// Second call should retry and succeed.
	result, _, err = handlePostContent(context.Background(), nil, postContentArgs{
		Title:   "Test",
		Content: "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success on retry, got: %v", result.Content)
	}
	if calls != 2 {
		t.Fatalf("expected 2 subtype fetches, got %d", calls)
	}
}

func TestHandleStartChatPostsAndWaitsForReply(t *testing.T) {
	oldProject := cfg.Project
	cfg.Project = "testproj"
	t.Cleanup(func() { cfg.Project = oldProject })

	callCount := 0
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if !contains(body.Content, "[testproj]") {
				t.Errorf("expected project prefix, got: %s", body.Content)
			}
			resp := message{ID: json.Number("500"), Content: body.Content, Date: json.Number("1704067200000"), UserID: "u1"}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// GET: first call returns no new messages, second call returns a reply.
		callCount++
		if callCount <= 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []message{
				{ID: json.Number("500"), Content: "original", Date: json.Number("1704067200000"), UserID: "u1"},
			}})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []message{
				{ID: json.Number("501"), Content: "reply from other agent", Date: json.Number("1704067201000"), UserID: "u2"},
				{ID: json.Number("500"), Content: "original", Date: json.Number("1704067200000"), UserID: "u1"},
			}})
		}
	}))

	// Reset chat state.
	chatMu.Lock()
	chatCancel = nil
	chatLastID = 0
	chatMu.Unlock()

	result, _, err := handleStartChat(context.Background(), nil, startChatArgs{
		Message:        "hello other agent",
		TimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !contains(text, "reply from other agent") {
		t.Errorf("expected reply in result, got: %s", text)
	}
}

func TestHandleStartChatAlreadyActive(t *testing.T) {
	t.Setenv("CLICKUP_TOKEN", "test-token")

	chatMu.Lock()
	_, cancel := context.WithCancel(context.Background())
	chatCancel = cancel
	chatMu.Unlock()
	t.Cleanup(func() {
		cancel()
		chatMu.Lock()
		chatCancel = nil
		chatLastID = 0
		chatMu.Unlock()
	})

	result, _, err := handleStartChat(context.Background(), nil, startChatArgs{Message: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for already active session")
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !contains(text, "already active") {
		t.Errorf("expected 'already active' error, got: %s", text)
	}
}

func TestHandleStartChatCancelled(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []message{}})
	}))

	chatMu.Lock()
	chatCancel = nil
	chatLastID = 0
	chatMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	result, _, err := handleStartChat(ctx, nil, startChatArgs{TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for cancelled context")
	}
}

func TestHandleStopChatNoSession(t *testing.T) {
	chatMu.Lock()
	chatCancel = nil
	chatLastID = 0
	chatMu.Unlock()

	result, _, err := handleStopChat(context.Background(), nil, stopChatArgs{})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !contains(text, "No active chat session") {
		t.Errorf("expected 'No active chat session', got: %s", text)
	}
}

func TestHandleStopChatCancelsSession(t *testing.T) {
	t.Setenv("CLICKUP_TOKEN", "test-token")

	chatMu.Lock()
	_, cancel := context.WithCancel(context.Background())
	chatCancel = cancel
	chatLastID = 100
	chatMu.Unlock()

	result, _, err := handleStopChat(context.Background(), nil, stopChatArgs{})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !contains(text, "Chat session stopped") {
		t.Errorf("expected 'Chat session stopped', got: %s", text)
	}

	// Verify state was reset.
	chatMu.Lock()
	if chatCancel != nil {
		t.Error("chatCancel should be nil after stop")
	}
	if chatLastID != 0 {
		t.Error("chatLastID should be 0 after stop")
	}
	chatMu.Unlock()
}

func TestLoadConfigDefaults(t *testing.T) {
	// Run in a temp dir with no .cupa.yaml.
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkspaceID != "9011518645" {
		t.Errorf("expected default workspace, got: %s", cfg.WorkspaceID)
	}
	if cfg.ChannelID != "6-901113290332-8" {
		t.Errorf("expected default channel, got: %s", cfg.ChannelID)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	_ = os.WriteFile(".cupa.yaml", []byte("workspace_id: \"ws-custom\"\nchannel_id: \"ch-custom\"\n"), 0o600)

	err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkspaceID != "ws-custom" {
		t.Errorf("expected ws-custom, got: %s", cfg.WorkspaceID)
	}
	if cfg.ChannelID != "ch-custom" {
		t.Errorf("expected ch-custom, got: %s", cfg.ChannelID)
	}
}

func TestClickupRequestAPIError404(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 404, "message": "Not found"})
	}))

	_, err := clickupRequest(context.Background(), http.MethodGet, messagesPath(), nil)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !contains(err.Error(), "404") || !contains(err.Error(), ".cupa.yaml") {
		t.Errorf("expected 404 error with config hint, got: %v", err)
	}
}

func TestClickupRequestAPIError429(t *testing.T) {
	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 429, "message": "Rate limited"})
	}))

	_, err := clickupRequest(context.Background(), http.MethodGet, messagesPath(), nil)
	if err == nil {
		t.Fatal("expected error for 429")
	}
	if !contains(err.Error(), "429") || !contains(err.Error(), "rate limited") {
		t.Errorf("expected 429 rate limit error, got: %v", err)
	}
}

func TestResolveSubtypeIDFallback(t *testing.T) {
	// Reset OnceValues to re-fetch.
	resetSubtypeCache()

	withTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Return subtypes without "Update" — should fall back to first.
		_ = json.NewEncoder(w).Encode([]postSubtype{
			{ID: "first-id", Name: "Announcement"},
			{ID: "second-id", Name: "Discussion"},
		})
	}))

	id, err := resolveSubtypeID()
	if err != nil {
		t.Fatal(err)
	}
	if id != "first-id" {
		t.Errorf("expected fallback to first subtype, got: %s", id)
	}
}

func TestHandleCheckSetupTokenSet(t *testing.T) {
	t.Setenv("CLICKUP_TOKEN", "")

	result, _, err := handleCheckSetup(context.Background(), nil, checkSetupArgs{})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !contains(text, "NOT SET") {
		t.Error("expected NOT SET when token is empty")
	}
}

func TestMessagesAfter(t *testing.T) {
	msgs := []message{
		{ID: json.Number("300"), Content: "new"},
		{ID: json.Number("200"), Content: "match"},
		{ID: json.Number("100"), Content: "old"},
	}
	got := messagesAfter(msgs, 150)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Content != "new" || got[1].Content != "match" {
		t.Errorf("unexpected messages: %v", got)
	}

	got = messagesAfter(msgs, 500)
	if len(got) != 0 {
		t.Errorf("expected 0 messages for high afterID, got %d", len(got))
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
