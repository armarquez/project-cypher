package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockVendor starts a test server that records the last request it received
// and replies with a fixed JSON body.
func mockVendor(t *testing.T, replyStatus int, replyBody string) (*httptest.Server, *http.Request) {
	t.Helper()
	var last *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(replyStatus)
		w.Write([]byte(replyBody)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, last // last is a pointer; dereference after the request is made
}

func buildRouter(t *testing.T, overrides map[Vendor]string) *Router {
	t.Helper()
	return NewRouter(http.DefaultClient, overrides, nil)
}

func doRequest(t *testing.T, router *Router, model string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"model": model, "prompt": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Result()
}

// --- parseVendor unit tests ---

func TestParseVendor(t *testing.T) {
	cases := []struct {
		model   string
		want    Vendor
		wantErr bool
	}{
		{"gemini/gemini-2.0-flash", VendorGemini, false},
		{"anthropic/claude-sonnet-4-5", VendorAnthropic, false},
		{"ollama/qwen2.5-coder", VendorOllama, false},
		{"openai/gpt-4o", VendorOpenAI, false},
		{"GEMINI/gemini-2.0-flash", VendorGemini, false}, // case-insensitive
		{"unknown/model", "", true},
		{"no-slash", "", true},
		{"", "", true},
	}

	for _, tc := range cases {
		body, _ := json.Marshal(map[string]string{"model": tc.model})
		got, err := parseVendor(body)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseVendor(%q): expected error, got nil", tc.model)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVendor(%q): unexpected error: %v", tc.model, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseVendor(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}

func TestParseVendor_MissingModelField(t *testing.T) {
	body := []byte(`{"prompt": "hello"}`)
	if _, err := parseVendor(body); err == nil {
		t.Error("expected error for missing model field, got nil")
	}
}

func TestParseVendor_InvalidJSON(t *testing.T) {
	if _, err := parseVendor([]byte("not json")); err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// --- Proxy routing integration tests ---

func TestProxy_RoutesToGemini(t *testing.T) {
	geminiSrv, _ := mockVendor(t, http.StatusOK, `{"id":"g1"}`)
	router := buildRouter(t, map[Vendor]string{VendorGemini: geminiSrv.URL})

	resp := doRequest(t, router, "gemini/gemini-2.0-flash")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"id":"g1"}` {
		t.Errorf("body = %q, want gemini reply", string(body))
	}
}

func TestProxy_RoutesToAnthropic(t *testing.T) {
	anthropicSrv, _ := mockVendor(t, http.StatusOK, `{"id":"a1"}`)
	router := buildRouter(t, map[Vendor]string{VendorAnthropic: anthropicSrv.URL})

	resp := doRequest(t, router, "anthropic/claude-sonnet-4-5")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestProxy_RoutesToOllama(t *testing.T) {
	ollamaSrv, _ := mockVendor(t, http.StatusOK, `{"id":"o1"}`)
	router := buildRouter(t, map[Vendor]string{VendorOllama: ollamaSrv.URL})

	resp := doRequest(t, router, "ollama/qwen2.5-coder")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestProxy_UnknownVendor(t *testing.T) {
	router := buildRouter(t, nil)
	resp := doRequest(t, router, "unknown/model")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestProxy_MissingModelField(t *testing.T) {
	router := buildRouter(t, nil)
	body := []byte(`{"prompt": "no model field"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestProxy_UpstreamStatusPassthrough(t *testing.T) {
	srv, _ := mockVendor(t, http.StatusTooManyRequests, `{"error":"rate limited"}`)
	router := buildRouter(t, map[Vendor]string{VendorGemini: srv.URL})

	resp := doRequest(t, router, "gemini/gemini-2.0-flash")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 (upstream status should pass through)", resp.StatusCode)
	}
}

func TestProxy_HopByHopHeadersStripped(t *testing.T) {
	var receivedHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	router := buildRouter(t, map[Vendor]string{VendorGemini: upstream.URL})

	body, _ := json.Marshal(map[string]string{"model": "gemini/gemini-2.0-flash"})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("X-Custom-Header", "preserved")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if receivedHeaders.Get("Connection") != "" {
		t.Error("hop-by-hop header Connection should be stripped")
	}
	if receivedHeaders.Get("X-Custom-Header") != "preserved" {
		t.Error("non-hop-by-hop header X-Custom-Header should be forwarded")
	}
}

// --- Health endpoint ---

func TestHealthEndpoint(t *testing.T) {
	router := buildRouter(t, nil)
	srv := NewServer("", router)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("health status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
