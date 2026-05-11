package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInject_Gemini(t *testing.T) {
	cs := NewCredentialStore(map[Vendor]string{VendorGemini: "gemini-key-123"})
	h := make(http.Header)
	cs.Inject(VendorGemini, h)
	if got := h.Get("Authorization"); got != "Bearer gemini-key-123" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer gemini-key-123")
	}
}

func TestInject_Anthropic(t *testing.T) {
	cs := NewCredentialStore(map[Vendor]string{VendorAnthropic: "ant-key-456"})
	h := make(http.Header)
	cs.Inject(VendorAnthropic, h)
	if got := h.Get("x-api-key"); got != "ant-key-456" {
		t.Errorf("x-api-key = %q, want %q", got, "ant-key-456")
	}
	if got := h.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want %q", got, "2023-06-01")
	}
}

func TestInject_Anthropic_VersionNotOverridden(t *testing.T) {
	cs := NewCredentialStore(map[Vendor]string{VendorAnthropic: "ant-key-456"})
	h := make(http.Header)
	h.Set("anthropic-version", "2024-01-01")
	cs.Inject(VendorAnthropic, h)
	if got := h.Get("anthropic-version"); got != "2024-01-01" {
		t.Errorf("anthropic-version = %q, want caller's value %q", got, "2024-01-01")
	}
}

func TestInject_Anthropic_VersionSetEvenWithoutKey(t *testing.T) {
	cs := NewCredentialStore(map[Vendor]string{}) // no Anthropic key
	h := make(http.Header)
	cs.Inject(VendorAnthropic, h)
	if got := h.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want %q even without key", got, "2023-06-01")
	}
}

func TestInject_OpenAI(t *testing.T) {
	cs := NewCredentialStore(map[Vendor]string{VendorOpenAI: "oai-key-789"})
	h := make(http.Header)
	cs.Inject(VendorOpenAI, h)
	if got := h.Get("Authorization"); got != "Bearer oai-key-789" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer oai-key-789")
	}
}

func TestInject_Ollama_NoAuth(t *testing.T) {
	cs := NewCredentialStore(map[Vendor]string{})
	h := make(http.Header)
	cs.Inject(VendorOllama, h)
	if got := h.Get("Authorization"); got != "" {
		t.Errorf("Ollama should not set Authorization, got %q", got)
	}
}

func TestInject_NilStore(t *testing.T) {
	var cs *CredentialStore
	h := make(http.Header)
	cs.Inject(VendorGemini, h) // must not panic
	if len(h) != 0 {
		t.Error("nil store should not set any headers")
	}
}

func TestInject_MissingKey(t *testing.T) {
	cs := NewCredentialStore(map[Vendor]string{}) // empty
	h := make(http.Header)
	cs.Inject(VendorGemini, h)
	if got := h.Get("Authorization"); got != "" {
		t.Errorf("missing key should not set Authorization, got %q", got)
	}
}

// End-to-end: router with credentials injects auth header into upstream request.
func TestRouter_InjectsCredentials(t *testing.T) {
	var receivedKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	creds := NewCredentialStore(map[Vendor]string{VendorAnthropic: "secret-ant-key"})
	router := NewRouter(http.DefaultClient, map[Vendor]string{VendorAnthropic: upstream.URL}, creds)

	body, _ := json.Marshal(map[string]string{"model": "anthropic/claude-sonnet-4-5"})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if receivedKey != "secret-ant-key" {
		t.Errorf("upstream received x-api-key = %q, want %q", receivedKey, "secret-ant-key")
	}
}
