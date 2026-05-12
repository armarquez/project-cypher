package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureRequest starts a server that records every request header and
// replies with the given status and optional JSON body.
func captureRequest(t *testing.T, status int, body any) (*httptest.Server, *http.Header) {
	t.Helper()
	var captured http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Clone()
		captured = h
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			json.NewEncoder(w).Encode(body) //nolint:errcheck
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func TestClient_SetsRequiredHeaders(t *testing.T) {
	srv, headers := captureRequest(t, http.StatusOK, []any{})
	testClient(t, srv.URL).do(context.Background(), "/repos/o/r/issues", &[]any{}) //nolint:errcheck

	cases := []struct{ header, want string }{
		{"Authorization", "Bearer test-token"},
		{"Accept", "application/vnd.github+json"},
		{"X-Github-Api-Version", "2022-11-28"},
	}
	for _, tc := range cases {
		if got := (*headers).Get(tc.header); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestClient_DoMethod_NoContentTypeWithoutBody(t *testing.T) {
	srv, headers := captureRequest(t, http.StatusOK, []any{})
	testClient(t, srv.URL).do(context.Background(), "/test", &[]any{}) //nolint:errcheck

	if ct := (*headers).Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q on GET with no body, want empty", ct)
	}
}

func TestClient_DoMethod_NilDstSkipsDecoding(t *testing.T) {
	srv, _ := captureRequest(t, http.StatusOK, map[string]string{"key": "val"})
	// Should not panic or error even though response has a body and dst is nil.
	err := testClient(t, srv.URL).doMethod(context.Background(), http.MethodPost, "/test", map[string]string{"x": "y"}, nil)
	if err != nil {
		t.Errorf("unexpected error with nil dst: %v", err)
	}
}

func TestClient_ErrorResponseIncludesMessage(t *testing.T) {
	srv, _ := captureRequest(t, http.StatusNotFound, map[string]string{"message": "Not Found"})
	err := testClient(t, srv.URL).do(context.Background(), "/missing", &struct{}{})
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("error %q does not contain API message %q", err.Error(), "Not Found")
	}
}

func TestClient_ErrorResponseIncludesStatusCode(t *testing.T) {
	srv, _ := captureRequest(t, http.StatusForbidden, map[string]string{"message": "Forbidden"})
	err := testClient(t, srv.URL).do(context.Background(), "/secret", &struct{}{})
	if err == nil {
		t.Fatal("expected error for 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q does not contain status code", err.Error())
	}
}

func TestNewClient_DefaultBaseURL(t *testing.T) {
	c := NewClient("tok", http.DefaultClient, "")
	if c.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultBaseURL)
	}
}

func TestNewClient_CustomBaseURL(t *testing.T) {
	c := NewClient("tok", http.DefaultClient, "http://localhost:9999")
	if c.baseURL != "http://localhost:9999" {
		t.Errorf("baseURL = %q, want custom URL", c.baseURL)
	}
}

