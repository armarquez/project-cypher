package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// --- NewClientFromApp / InstallationTransport ---

func generateTestPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func TestNewClientFromApp_InvalidPEM(t *testing.T) {
	_, err := NewClientFromApp(1, 2, []byte("not a pem"), nil, "")
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestNewClientFromApp_ValidPEM(t *testing.T) {
	pemData := generateTestPEM(t)
	c, err := NewClientFromApp(1, 2, pemData, nil, "http://localhost:9999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.baseURL != "http://localhost:9999" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
	if c.token != "" {
		t.Error("App client should have empty static token")
	}
}

func TestNewClientFromApp_DefaultBaseURL(t *testing.T) {
	pemData := generateTestPEM(t)
	c, err := NewClientFromApp(1, 2, pemData, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultBaseURL)
	}
}

func TestInstallationTransport_FetchesAndCachesToken(t *testing.T) {
	pemData := generateTestPEM(t)
	calls := 0

	// Fake GitHub API: handles JWT-authenticated token exchange.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/access_tokens") {
			http.NotFound(w, r)
			return
		}
		calls++
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_test","expires_at":%q}`,
			time.Now().Add(time.Hour).Format(time.RFC3339))
	}))
	defer srv.Close()

	c, err := NewClientFromApp(42, 7, pemData, srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	tr := c.httpClient.Transport.(*InstallationTransport)
	tok1, err := tr.getToken(context.Background())
	if err != nil {
		t.Fatalf("first getToken: %v", err)
	}
	if tok1 != "ghs_test" {
		t.Errorf("token = %q, want ghs_test", tok1)
	}
	if calls != 1 {
		t.Errorf("expected 1 API call, got %d", calls)
	}

	// Second call should use the cached token.
	tok2, err := tr.getToken(context.Background())
	if err != nil {
		t.Fatalf("second getToken: %v", err)
	}
	if tok2 != tok1 {
		t.Errorf("expected cached token on second call")
	}
	if calls != 1 {
		t.Errorf("expected still 1 API call after cache hit, got %d", calls)
	}
}

func TestInstallationTransport_RefreshesExpiredToken(t *testing.T) {
	pemData := generateTestPEM(t)
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/access_tokens") {
			http.NotFound(w, r)
			return
		}
		calls++
		// Return an already-expired token so the next call always refreshes.
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_v%d","expires_at":%q}`,
			calls, time.Now().Add(-time.Minute).Format(time.RFC3339))
	}))
	defer srv.Close()

	c, _ := NewClientFromApp(1, 2, pemData, srv.Client(), srv.URL)
	tr := c.httpClient.Transport.(*InstallationTransport)

	tr.getToken(context.Background()) //nolint:errcheck — first fetch
	tr.getToken(context.Background()) //nolint:errcheck — should refresh (expired)

	if calls < 2 {
		t.Errorf("expected at least 2 token fetches for expired tokens, got %d", calls)
	}
}

func TestInstallationTransport_InjectsTokenHeader(t *testing.T) {
	pemData := generateTestPEM(t)
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/access_tokens") {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"token":"ghs_injected","expires_at":%q}`,
				time.Now().Add(time.Hour).Format(time.RFC3339))
			return
		}
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]")) //nolint:errcheck
	}))
	defer srv.Close()

	c, err := NewClientFromApp(1, 2, pemData, srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	c.do(context.Background(), "/repos/o/r/issues", &[]any{}) //nolint:errcheck

	if !strings.HasPrefix(capturedAuth, "token ") {
		t.Errorf("Authorization = %q, want 'token ghs_injected'", capturedAuth)
	}
	if !strings.Contains(capturedAuth, "ghs_injected") {
		t.Errorf("Authorization = %q, want ghs_injected token", capturedAuth)
	}
}

// --- Ping ---

func TestClient_Ping_Success(t *testing.T) {
	srv, _ := captureRequest(t, http.StatusOK, nil)
	c := NewClient("test-token", srv.Client(), srv.URL)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_Ping_Unauthorized(t *testing.T) {
	srv, _ := captureRequest(t, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
	c := NewClient("bad-token", srv.Client(), srv.URL)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error for 401 response")
	}
}

