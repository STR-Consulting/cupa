package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
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
	}))

	result, _, err := handlePostNote(context.Background(), nil, postNoteArgs{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if text != "Posted message 999" {
		t.Errorf("unexpected result: %s", text)
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

	_, err := clickupRequest(context.Background(), http.MethodGet, messagesPath, nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !contains(err.Error(), "Token invalid") {
		t.Errorf("expected 'Token invalid' in error, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
