package architect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestComplete_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("got method %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("got path %s, want /v1/messages", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing or wrong x-api-key header")
		}
		if r.Header.Get("anthropic-version") != anthropicVersion {
			t.Errorf("missing or wrong anthropic-version header")
		}

		var req messagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "claude-test" {
			t.Errorf("got model %s, want claude-test", req.Model)
		}
		if req.System != "be a classifier" {
			t.Errorf("got system %q, want %q", req.System, "be a classifier")
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "classify this" {
			t.Errorf("unexpected messages: %v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(messagesResponse{ //nolint:errcheck
			Content: []contentBlock{{Type: "text", Text: "the answer"}},
		})
	}))
	defer srv.Close()

	c := newWithBase("claude-test", "test-key", srv.URL, nil)
	got, err := c.Complete(context.Background(), "be a classifier", "classify this")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "the answer" {
		t.Errorf("got %q, want %q", got, "the answer")
	}
}

func TestComplete_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"overloaded"}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newWithBase("claude-test", "test-key", srv.URL, nil)
	_, err := c.Complete(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestComplete_EmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(messagesResponse{Content: []contentBlock{}}) //nolint:errcheck
	}))
	defer srv.Close()

	c := newWithBase("claude-test", "test-key", srv.URL, nil)
	_, err := c.Complete(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestComplete_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newWithBase("claude-test", "test-key", srv.URL, nil)
	_, err := c.Complete(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestComplete_NetworkError(t *testing.T) {
	c := newWithBase("claude-test", "test-key", "http://localhost:1", nil)
	_, err := c.Complete(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestNew_DefaultHTTPClient(t *testing.T) {
	c := New("claude-sonnet-4-6", "key", nil)
	if c.httpClient == nil {
		t.Fatal("expected non-nil httpClient")
	}
	if c.baseURL != defaultBaseURL {
		t.Errorf("got baseURL %q, want %q", c.baseURL, defaultBaseURL)
	}
}
