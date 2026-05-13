package setup

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- GenerateManifest ---

func TestGenerateManifest(t *testing.T) {
	m := GenerateManifest("myorg", "myrepo", "http://localhost:9999/callback")
	if m.Name != "cypher-myorg-myrepo" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.RedirectURL != "http://localhost:9999/callback" {
		t.Errorf("RedirectURL = %q", m.RedirectURL)
	}
	if m.DefaultPermissions["contents"] != "write" {
		t.Error("expected contents:write permission")
	}
	if m.DefaultPermissions["metadata"] != "read" {
		t.Error("expected metadata:read permission")
	}
	found := false
	for _, e := range m.DefaultEvents {
		if e == "issues" {
			found = true
		}
	}
	if !found {
		t.Error("expected issues event")
	}
}

// --- ExchangeCode ---

func TestExchangeCode_Success(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/conversions") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"id":             float64(42),
			"slug":           "cypher-test",
			"pem":            "-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----",
			"client_id":      "Iv1.abc",
			"client_secret":  "secret",
			"webhook_secret": "whsecret",
		})
	}))
	defer s.Close()

	creds, err := ExchangeCode(context.Background(), s.Client(), s.URL, "testcode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.AppID != 42 {
		t.Errorf("AppID = %d, want 42", creds.AppID)
	}
	if creds.Slug != "cypher-test" {
		t.Errorf("Slug = %q", creds.Slug)
	}
}

func TestExchangeCode_NonCreated(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"code expired"}`)) //nolint:errcheck
	}))
	defer s.Close()

	_, err := ExchangeCode(context.Background(), s.Client(), s.URL, "bad-code")
	if err == nil {
		t.Fatal("expected error for non-201 response")
	}
}

// --- ParsePrivateKey ---

func TestParsePrivateKey_InvalidPEM(t *testing.T) {
	_, err := ParsePrivateKey("not a pem")
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestParsePrivateKey_InvalidKeyData(t *testing.T) {
	pemData := "-----BEGIN RSA PRIVATE KEY-----\naW52YWxpZA==\n-----END RSA PRIVATE KEY-----"
	_, err := ParsePrivateKey(pemData)
	if err == nil {
		t.Fatal("expected error for invalid RSA key data")
	}
}

// --- MakeJWT ---

func TestMakeJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	jwt, err := MakeJWT(12345, key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if claims["iss"] != "12345" {
		t.Errorf("iss = %v, want \"12345\"", claims["iss"])
	}
	if _, ok := claims["iat"]; !ok {
		t.Error("missing iat claim")
	}
	if _, ok := claims["exp"]; !ok {
		t.Error("missing exp claim")
	}
}

// --- WriteCredentials ---

func TestWriteCredentials(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	cypherDir := filepath.Join(dir, ".cypher")

	creds := &AppCredentials{
		AppID: 99,
		PEM:   "-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----\n",
	}
	if err := WriteCredentials(envPath, cypherDir, creds, 777); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pemPath := filepath.Join(cypherDir, "app-key.pem")
	pemData, err := os.ReadFile(pemPath)
	if err != nil {
		t.Fatalf("read PEM: %v", err)
	}
	if !strings.Contains(string(pemData), "RSA PRIVATE KEY") {
		t.Error("expected PEM content in app-key.pem")
	}

	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	env := string(envData)
	if !strings.Contains(env, "CYPHER_GH_APP_ID=99") {
		t.Errorf(".env missing CYPHER_GH_APP_ID=99, got:\n%s", env)
	}
	if !strings.Contains(env, "CYPHER_GH_INSTALLATION_ID=777") {
		t.Errorf(".env missing CYPHER_GH_INSTALLATION_ID=777, got:\n%s", env)
	}
	if !strings.Contains(env, "CYPHER_GH_APP_PRIVATE_KEY_FILE=") {
		t.Errorf(".env missing CYPHER_GH_APP_PRIVATE_KEY_FILE, got:\n%s", env)
	}
}

func TestWriteCredentials_UpdatesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	cypherDir := filepath.Join(dir, ".cypher")

	os.WriteFile(envPath, []byte("CYPHER_GH_APP_ID=old\nSOME_OTHER_VAR=keep\n"), 0o600) //nolint:errcheck

	creds := &AppCredentials{
		AppID: 123,
		PEM:   "-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----\n",
	}
	if err := WriteCredentials(envPath, cypherDir, creds, 456); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envData, _ := os.ReadFile(envPath)
	env := string(envData)

	if strings.Contains(env, "CYPHER_GH_APP_ID=old") {
		t.Error("expected old CYPHER_GH_APP_ID to be replaced")
	}
	if !strings.Contains(env, "CYPHER_GH_APP_ID=123") {
		t.Errorf(".env missing CYPHER_GH_APP_ID=123, got:\n%s", env)
	}
	if !strings.Contains(env, "SOME_OTHER_VAR=keep") {
		t.Errorf(".env dropped SOME_OTHER_VAR, got:\n%s", env)
	}
}

// --- PollInstallation ---

func TestPollInstallation_Found(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]interface{}{ //nolint:errcheck
			{"id": float64(42), "account": map[string]string{"login": "myorg"}},
		})
	}))
	defer s.Close()

	id, err := PollInstallation(context.Background(), s.Client(), s.URL, "fake-jwt", "myorg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Errorf("id = %d, want 42", id)
	}
}

func TestPollInstallation_CaseInsensitive(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]interface{}{ //nolint:errcheck
			{"id": float64(7), "account": map[string]string{"login": "MyOrg"}},
		})
	}))
	defer s.Close()

	id, err := PollInstallation(context.Background(), s.Client(), s.URL, "tok", "myorg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 7 {
		t.Errorf("id = %d, want 7", id)
	}
}

func TestPollInstallation_ContextCancelled(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`)) //nolint:errcheck
	}))
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PollInstallation(ctx, s.Client(), s.URL, "tok", "org")
	if err == nil {
		t.Fatal("expected error for server error or cancelled context")
	}
}

// --- Config methods ---

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{}
	if cfg.apiBase() != "https://api.github.com" {
		t.Errorf("apiBase() = %q", cfg.apiBase())
	}
	if cfg.client() == nil {
		t.Error("client() should return non-nil default client")
	}
	if cfg.stdout() == nil {
		t.Error("stdout() should return non-nil writer")
	}
}

func TestConfig_Overrides(t *testing.T) {
	var buf strings.Builder
	cli := &http.Client{Timeout: 1 * time.Second}
	cfg := Config{
		APIBase: "http://example.com",
		Client:  cli,
		Stdout:  &buf,
	}
	if cfg.apiBase() != "http://example.com" {
		t.Errorf("apiBase() = %q", cfg.apiBase())
	}
	if cfg.client() != cli {
		t.Error("client() should return the injected client")
	}
	if cfg.stdout() != &buf {
		t.Error("stdout() should return the injected writer")
	}
}

// --- randomHex ---

func TestRandomHex(t *testing.T) {
	h, err := randomHex(16)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %q", len(h), h)
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex character %q in output", c)
		}
	}
}

// --- autoSubmitPage / successPage ---

func TestAutoSubmitPage(t *testing.T) {
	page := autoSubmitPage(`{"name":"test"}`, "mystate")
	if !strings.Contains(page, "mystate") {
		t.Error("expected state in auto-submit page")
	}
	if !strings.Contains(page, `{"name":"test"}`) {
		t.Error("expected manifest JSON in auto-submit page")
	}
	if !strings.Contains(page, "github.com/settings/apps/new") {
		t.Error("expected GitHub URL in auto-submit page")
	}
}

func TestSuccessPage(t *testing.T) {
	page := successPage()
	if !strings.Contains(page, "successfully") {
		t.Error("expected success message in page")
	}
}

// --- Run (end-to-end) ---

func TestRun_EndToEnd(t *testing.T) {
	// Generate a real RSA key for the fake exchange response.
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	// Fake GitHub API server.
	fakeAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/app-manifests/"):
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"id":   float64(999),
				"slug": "cypher-testorg-testrepo",
				"pem":  string(pemBytes),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/app/installations":
			json.NewEncoder(w).Encode([]map[string]interface{}{ //nolint:errcheck
				{"id": float64(777), "account": map[string]string{"login": "testorg"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fakeAPI.Close()

	dir := t.TempDir()
	var stdout strings.Builder

	cfg := Config{
		TargetRepo: "https://github.com/testorg/testrepo",
		EnvPath:    filepath.Join(dir, ".env"),
		CypherDir:  filepath.Join(dir, ".cypher"),
		APIBase:    fakeAPI.URL,
		Client:     fakeAPI.Client(),
		Stdout:     &stdout,
		OnServerReady: func(port int) {
			// Simulate GitHub's redirect back to the callback.
			time.Sleep(20 * time.Millisecond)
			resp, err := http.Get(fmt.Sprintf("http://localhost:%d/callback?code=testcode&state=ok", port))
			if err == nil {
				io.Copy(io.Discard, resp.Body) //nolint:errcheck
				resp.Body.Close()
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := Run(ctx, cfg); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify .env was written.
	envData, err := os.ReadFile(cfg.EnvPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	env := string(envData)
	if !strings.Contains(env, "CYPHER_GH_APP_ID=999") {
		t.Errorf(".env missing CYPHER_GH_APP_ID=999:\n%s", env)
	}
	if !strings.Contains(env, "CYPHER_GH_INSTALLATION_ID=777") {
		t.Errorf(".env missing CYPHER_GH_INSTALLATION_ID=777:\n%s", env)
	}

	// Verify PEM was written.
	pemPath := filepath.Join(dir, ".cypher", "app-key.pem")
	if _, err := os.Stat(pemPath); err != nil {
		t.Errorf("PEM file not found: %v", err)
	}

	// Verify progress output.
	out := stdout.String()
	if !strings.Contains(out, "App created") {
		t.Errorf("expected 'App created' in output:\n%s", out)
	}
	if !strings.Contains(out, "Installed") {
		t.Errorf("expected 'Installed' in output:\n%s", out)
	}
}

func TestRun_InvalidTargetRepo(t *testing.T) {
	cfg := Config{TargetRepo: "not-a-repo"}
	err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for invalid target_repo")
	}
}
