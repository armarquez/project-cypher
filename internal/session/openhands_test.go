package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testOpenHandsClient(t *testing.T, baseURL string) *OpenHandsClient {
	t.Helper()
	return NewOpenHandsClient(http.DefaultClient, baseURL)
}

// ohServer is a test OpenHands API server.
type ohServer struct {
	*dockerServer // reuse the routing helper from docker_test.go
}

func newOHServer(t *testing.T) *ohServer {
	return &ohServer{newDockerServer(t)}
}

// --- ConversationStatus ---

func TestConversationStatus_IsTerminal(t *testing.T) {
	cases := []struct {
		status   ConversationStatus
		terminal bool
	}{
		{StatusRunning, false},
		{StatusFinished, true},
		{StatusError, true},
		{"unknown", false},
	}
	for _, tc := range cases {
		if got := tc.status.IsTerminal(); got != tc.terminal {
			t.Errorf("%q.IsTerminal() = %v, want %v", tc.status, got, tc.terminal)
		}
	}
}

// --- StartTask ---

func TestStartTask_ReturnsConversationID(t *testing.T) {
	ds := newOHServer(t)
	ds.on("POST", "/api/conversations",
		ds.replyJSON(http.StatusOK, map[string]string{"conversation_id": "conv-abc"}))

	id, err := testOpenHandsClient(t, ds.srv.URL).StartTask(context.Background(), "fix issue #5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "conv-abc" {
		t.Errorf("id = %q, want conv-abc", id)
	}
}

func TestStartTask_SendsPromptInBody(t *testing.T) {
	var gotBody map[string]string
	ds := newOHServer(t)
	ds.on("POST", "/api/conversations", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"conversation_id": "x"}) //nolint:errcheck
	})

	testOpenHandsClient(t, ds.srv.URL).StartTask(context.Background(), "my task prompt") //nolint:errcheck

	if gotBody["initial_user_msg"] != "my task prompt" {
		t.Errorf("initial_user_msg = %q, want 'my task prompt'", gotBody["initial_user_msg"])
	}
}

func TestStartTask_EmptyIDError(t *testing.T) {
	ds := newOHServer(t)
	ds.on("POST", "/api/conversations",
		ds.replyJSON(http.StatusOK, map[string]string{"conversation_id": ""}))

	_, err := testOpenHandsClient(t, ds.srv.URL).StartTask(context.Background(), "task")
	if err == nil {
		t.Fatal("expected error for empty conversation ID")
	}
}

func TestStartTask_APIError(t *testing.T) {
	ds := newOHServer(t)
	ds.on("POST", "/api/conversations",
		ds.replyJSON(http.StatusUnprocessableEntity, map[string]string{"detail": "invalid prompt"}))

	_, err := testOpenHandsClient(t, ds.srv.URL).StartTask(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for 422, got nil")
	}
	if !strings.Contains(err.Error(), "invalid prompt") {
		t.Errorf("error %q missing API detail", err.Error())
	}
}

// --- FetchEvents ---

func TestFetchEvents_ReturnsEvents(t *testing.T) {
	ds := newOHServer(t)
	ds.on("GET", "/api/conversations/c1/events",
		ds.replyJSON(http.StatusOK, map[string]any{
			"events": []map[string]any{
				{"id": 1, "source": "agent", "action": "message",
					"args": map[string]any{"content": "Starting task"}},
				{"id": 2, "source": "environment", "observation": "run",
					"content": "exit 0"},
			},
		}))

	events, err := testOpenHandsClient(t, ds.srv.URL).FetchEvents(context.Background(), "c1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Text != "Starting task" {
		t.Errorf("events[0].Text = %q, want 'Starting task'", events[0].Text)
	}
	if events[1].Text != "exit 0" {
		t.Errorf("events[1].Text = %q, want 'exit 0'", events[1].Text)
	}
}

func TestFetchEvents_SendsSinceIDAsStartID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"events": []any{}}) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	testOpenHandsClient(t, srv.URL).FetchEvents(context.Background(), "conv-1", 42) //nolint:errcheck

	if !strings.Contains(gotPath, "start_id=42") {
		t.Errorf("path = %q, want start_id=42", gotPath)
	}
}

func TestFetchEvents_EmptyList(t *testing.T) {
	ds := newOHServer(t)
	ds.on("GET", "/api/conversations/c2/events",
		ds.replyJSON(http.StatusOK, map[string]any{"events": []any{}}))

	events, err := testOpenHandsClient(t, ds.srv.URL).FetchEvents(context.Background(), "c2", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

func TestFetchEvents_APIError(t *testing.T) {
	ds := newOHServer(t)
	ds.on("GET", "/api/conversations/bad/events",
		ds.replyJSON(http.StatusNotFound, map[string]string{"detail": "conversation not found"}))

	_, err := testOpenHandsClient(t, ds.srv.URL).FetchEvents(context.Background(), "bad", 0)
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

// --- GetStatus ---

func TestGetStatus_ReturnsStatus(t *testing.T) {
	cases := []struct {
		wire   string
		status ConversationStatus
	}{
		{"running", StatusRunning},
		{"finished", StatusFinished},
		{"error", StatusError},
	}
	for _, tc := range cases {
		ds := newOHServer(t)
		ds.on("GET", "/api/conversations/c1",
			ds.replyJSON(http.StatusOK, map[string]string{"status": tc.wire}))

		status, err := testOpenHandsClient(t, ds.srv.URL).GetStatus(context.Background(), "c1")
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", tc.wire, err)
		}
		if status != tc.status {
			t.Errorf("status = %q, want %q", status, tc.status)
		}
	}
}

func TestGetStatus_APIError(t *testing.T) {
	ds := newOHServer(t)
	ds.on("GET", "/api/conversations/c1",
		ds.replyJSON(http.StatusNotFound, map[string]string{"detail": "not found"}))

	_, err := testOpenHandsClient(t, ds.srv.URL).GetStatus(context.Background(), "c1")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

// --- extractText ---

func TestExtractText_AgentActionWithContent(t *testing.T) {
	e := rawEvent{
		Source: "agent",
		Action: "message",
		Args:   map[string]any{"content": "I will fix the bug"},
	}
	if got := extractText(e); got != "I will fix the bug" {
		t.Errorf("extractText = %q, want 'I will fix the bug'", got)
	}
}

func TestExtractText_ObservationContent(t *testing.T) {
	e := rawEvent{
		Source:      "environment",
		Observation: "run",
		Content:     "stdout output here",
	}
	if got := extractText(e); got != "stdout output here" {
		t.Errorf("extractText = %q, want 'stdout output here'", got)
	}
}

func TestExtractText_NoContent(t *testing.T) {
	e := rawEvent{Source: "user", Action: "message"}
	if got := extractText(e); got != "" {
		t.Errorf("extractText = %q, want empty", got)
	}
}
