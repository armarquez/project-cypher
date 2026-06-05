package doctor

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/armarquez/project-cypher/internal/secrets"
)

// --- helpers ---

func srv(t *testing.T, status int, body string, header ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.WriteHeader(status)
		w.Write([]byte(body)) //nolint:errcheck
	}))
}

func pass(results []Result, name string) bool {
	for _, r := range results {
		if r.Name == name {
			return r.Pass
		}
	}
	return false
}

func detail(results []Result, name string) string {
	for _, r := range results {
		if r.Name == name {
			return r.Detail
		}
	}
	return ""
}

// --- CheckTokenSet ---

func TestCheckTokenSet_Set(t *testing.T) {
	r := CheckTokenSet("ghp_abc", "CYPHER_GH_TOKEN_ORG_REPO", false)
	if !r.Pass {
		t.Error("expected pass when token is non-empty")
	}
	if r.Name != "CYPHER_GH_TOKEN_ORG_REPO set" {
		t.Errorf("unexpected name: %q", r.Name)
	}
}

func TestCheckTokenSet_Empty(t *testing.T) {
	r := CheckTokenSet("", "CYPHER_GH_TOKEN_ORG_REPO", false)
	if r.Pass {
		t.Error("expected fail when token is empty")
	}
	if r.Fix == "" {
		t.Error("expected fix hint")
	}
}

// --- CheckGitHub ---

func TestCheckGitHub_ValidToken(t *testing.T) {
	s := srv(t, 200, `{"login":"armarquez"}`,
		"X-OAuth-Scopes", "repo, workflow")
	defer s.Close()

	results := CheckGitHub(context.Background(), s.Client(), s.URL, "ghp_tok", "CYPHER_GH_TOKEN")
	if !pass(results, "GitHub token valid") {
		t.Error("expected GitHub token valid to pass")
	}
	if d := detail(results, "GitHub token valid"); d != "user: armarquez" {
		t.Errorf("detail = %q", d)
	}
	if !pass(results, "GitHub token scopes") {
		t.Error("expected scopes check to pass with repo scope present")
	}
}

func TestCheckGitHub_MissingRepoScope(t *testing.T) {
	s := srv(t, 200, `{"login":"user"}`,
		"X-OAuth-Scopes", "read:org")
	defer s.Close()

	results := CheckGitHub(context.Background(), s.Client(), s.URL, "ghp_tok", "CYPHER_GH_TOKEN")
	if !pass(results, "GitHub token valid") {
		t.Error("token should be valid even with wrong scopes")
	}
	if pass(results, "GitHub token scopes") {
		t.Error("expected scopes check to fail when repo scope is absent")
	}
}

func TestCheckGitHub_FineGrainedToken(t *testing.T) {
	// Fine-grained tokens omit X-OAuth-Scopes header.
	s := srv(t, 200, `{"login":"user"}`)
	defer s.Close()

	results := CheckGitHub(context.Background(), s.Client(), s.URL, "ghp_tok", "CYPHER_GH_TOKEN")
	if !pass(results, "GitHub token valid") {
		t.Error("expected pass for valid fine-grained token")
	}
	if !pass(results, "GitHub token scopes") {
		t.Error("expected scopes to pass for fine-grained token (header absent)")
	}
}

func TestCheckGitHub_Unauthorized(t *testing.T) {
	s := srv(t, 401, `{"message":"Bad credentials"}`)
	defer s.Close()

	results := CheckGitHub(context.Background(), s.Client(), s.URL, "ghp_tok", "CYPHER_GH_TOKEN")
	if pass(results, "GitHub token valid") {
		t.Error("expected fail for 401 response")
	}
}

func TestCheckGitHub_EmptyToken(t *testing.T) {
	results := CheckGitHub(context.Background(), http.DefaultClient, "http://unused", "", "CYPHER_GH_TOKEN")
	if pass(results, "GitHub token valid") {
		t.Error("expected fail when token is empty")
	}
}

// --- CheckConfig ---

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, `
target_repo: https://github.com/org/repo
worker_model: gemini/gemini-2.0-flash
architect_model: anthropic/claude-sonnet-4-6
test_command: go test ./...
skills: []
`)
	// skills is empty but that fails config.Load — use a valid skill list
	cfgPath = writeConfig(t, dir, `
target_repo: https://github.com/org/repo
worker_model: gemini/gemini-2.0-flash
architect_model: anthropic/claude-sonnet-4-6
test_command: go test ./...
skills: [git-operations]
`)
	skillsDir := t.TempDir()
	os.WriteFile(filepath.Join(skillsDir, "git-operations.yaml"), []byte("name: git-operations\n"), 0o644) //nolint:errcheck

	results := CheckConfig(cfgPath, skillsDir)
	if !pass(results, "config file readable") {
		t.Error("expected config file readable to pass")
	}
	if !pass(results, "config file valid") {
		t.Error("expected config file valid to pass")
	}
}

func TestCheckConfig_MissingFile(t *testing.T) {
	results := CheckConfig(filepath.Join(t.TempDir(), "nonexistent.yaml"), t.TempDir())
	if pass(results, "config file valid") {
		t.Error("expected fail for missing config file")
	}
}

func TestCheckConfig_ValidationErrors(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, `
target_repo: https://github.com/org/repo
worker_model: gemini/gemini-2.0-flash
architect_model: anthropic/claude-sonnet-4-6
test_command: go test ./...
skills: [missing-bundle]
`)
	// skills dir has no bundles
	results := CheckConfig(cfgPath, t.TempDir())
	if !pass(results, "config file readable") {
		t.Error("expected config file readable to pass")
	}
	if pass(results, "config file valid") {
		t.Error("expected config file valid to fail with missing skill bundle")
	}
}

// --- CheckDocker ---

func dockerSrv(t *testing.T, imageStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// image inspect
		w.WriteHeader(imageStatus)
	}))
}

func TestCheckDocker_SocketReachable_ImageFound(t *testing.T) {
	s := dockerSrv(t, http.StatusOK)
	defer s.Close()

	results := CheckDocker(context.Background(), s.Client(), s.URL, "my-image:latest")
	if !pass(results, "Docker socket reachable") {
		t.Error("expected Docker socket reachable to pass")
	}
	if !pass(results, "worker image available") {
		t.Error("expected worker image available to pass")
	}
}

func TestCheckDocker_SocketReachable_ImageMissing(t *testing.T) {
	s := dockerSrv(t, http.StatusNotFound)
	defer s.Close()

	results := CheckDocker(context.Background(), s.Client(), s.URL, "missing-image:latest")
	if !pass(results, "Docker socket reachable") {
		t.Error("expected Docker socket reachable to pass")
	}
	if pass(results, "worker image available") {
		t.Error("expected worker image available to fail when image is not found")
	}
}

func TestCheckDocker_SocketUnreachable(t *testing.T) {
	client := &http.Client{}
	results := CheckDocker(context.Background(), client, "http://127.0.0.1:0", "image")
	if pass(results, "Docker socket reachable") {
		t.Error("expected Docker socket reachable to fail")
	}
	if pass(results, "worker image available") {
		t.Error("expected worker image available to fail when socket unreachable")
	}
}

func TestCheckDocker_InfoNonOK(t *testing.T) {
	s := srv(t, 500, "")
	defer s.Close()

	results := CheckDocker(context.Background(), s.Client(), s.URL, "image")
	if pass(results, "Docker socket reachable") {
		t.Error("expected fail when Docker info returns non-200")
	}
}

func TestCheckDocker_ImageUnexpectedStatus(t *testing.T) {
	s := dockerSrv(t, http.StatusInternalServerError)
	defer s.Close()

	results := CheckDocker(context.Background(), s.Client(), s.URL, "image:tag")
	if !pass(results, "Docker socket reachable") {
		t.Error("socket should be reachable")
	}
	if pass(results, "worker image available") {
		t.Error("expected fail when image inspect returns non-200/non-404")
	}
}

// --- CheckClockSkew ---

func TestCheckClockSkew_InSync(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()
	r := CheckClockSkew(context.Background(), s.Client(), s.URL)
	if !r.Pass {
		t.Errorf("expected pass for synced clock, got: %s", r.Fix)
	}
}

func TestCheckClockSkew_Skewed(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Report server time 5 minutes behind local time (simulates local clock ahead).
		w.Header().Set("Date", time.Now().Add(-5*time.Minute).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()
	r := CheckClockSkew(context.Background(), s.Client(), s.URL)
	if r.Pass {
		t.Error("expected fail for skewed clock")
	}
	if !strings.Contains(r.Fix, "sync-clock") {
		t.Errorf("expected sync-clock hint in fix, got: %s", r.Fix)
	}
}

// --- CheckOpenHands ---

func TestCheckOpenHands_Responding(t *testing.T) {
	s := srv(t, 200, "ok")
	defer s.Close()

	r := CheckOpenHands(context.Background(), s.Client(), s.URL)
	if !r.Pass {
		t.Errorf("expected pass, got fix: %s", r.Fix)
	}
}

func TestCheckOpenHands_NotRunning(t *testing.T) {
	r := CheckOpenHands(context.Background(), &http.Client{}, "http://127.0.0.1:0")
	if r.Pass {
		t.Error("expected fail when endpoint is not reachable")
	}
}

func TestCheckOpenHands_ServerError(t *testing.T) {
	s := srv(t, 503, "")
	defer s.Close()

	r := CheckOpenHands(context.Background(), s.Client(), s.URL)
	if r.Pass {
		t.Error("expected fail on 5xx response")
	}
}

// --- CheckSecrets ---

func TestCheckSecrets_NoOpRefs(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "ghp_plaintoken")
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, secrets.NewFakeVault())
	if len(results) != 0 {
		t.Errorf("expected no results when no op:// refs, got %d", len(results))
	}
}

func TestCheckSecrets_VaultNotReady(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "op://vault/item/field")
	fv := &secrets.FakeVault{PreflightErr: fmt.Errorf("1Password CLI not ready: binary not found")}
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, fv)
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if pass(results, "1Password CLI ready") {
		t.Error("expected fail when vault preflight fails")
	}
	if results[0].Fix == "" {
		t.Error("expected fix hint")
	}
}

func TestCheckSecrets_ResolveSuccess(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "op://vault/item/field")
	fv := secrets.NewFakeVault()
	// Pre-populate so Get succeeds.
	fv.Store(context.Background(), "vault", "item", "field", "mysecret") //nolint:errcheck
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, fv)
	if !pass(results, "1Password CLI ready") {
		t.Error("expected vault ready check to pass")
	}
	if !pass(results, "CYPHER_GH_TOKEN accessible via op") {
		t.Errorf("expected secret accessible, results: %v", results)
	}
}

func TestCheckSecrets_ResolveFails(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "op://vault/item/field")
	fv := &secrets.FakeVault{GetErr: fmt.Errorf("not signed in")}
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, fv)
	if !pass(results, "1Password CLI ready") {
		t.Error("expected vault ready check to pass")
	}
	if pass(results, "CYPHER_GH_TOKEN accessible via op") {
		t.Error("expected secret check to fail when Get returns error")
	}
}

func TestCheckSecrets_UnsetVarSkipped(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "")
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, secrets.NewFakeVault())
	if len(results) != 0 {
		t.Errorf("expected no results for unset var, got %d", len(results))
	}
}

// --- CheckPEMFile ---

func TestCheckPEMFile_Unset(t *testing.T) {
	t.Setenv("CYPHER_GH_APP_PRIVATE_KEY_FILE", "")
	results := CheckPEMFile()
	if len(results) != 0 {
		t.Errorf("expected no results when var is unset, got %d", len(results))
	}
}

func TestCheckPEMFile_Set(t *testing.T) {
	t.Setenv("CYPHER_GH_APP_PRIVATE_KEY_FILE", ".cypher/app-key.pem")
	results := CheckPEMFile()
	if len(results) == 0 {
		t.Fatal("expected a result when plaintext file is configured")
	}
	if pass(results, "GitHub App key storage") {
		t.Error("expected fail for plaintext PEM file")
	}
	if results[0].Fix == "" {
		t.Error("expected migration hint in Fix")
	}
}

// --- CheckAppCredentials ---

func generateDocTestPEM(t *testing.T) []byte {
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

// appCredSrv starts a server that handles both the installation token exchange
// and an arbitrary subsequent API call (e.g. /rate_limit).
func appCredSrv(t *testing.T, tokenStatus, pingStatus int) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "access_tokens") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tokenStatus)
			if tokenStatus == http.StatusCreated {
				exp := time.Now().Add(time.Hour).Format(time.RFC3339)
				fmt.Fprintf(w, `{"token":"ghs_test","expires_at":%q}`, exp) //nolint:errcheck
			}
			return
		}
		w.WriteHeader(pingStatus)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestCheckAppCredentials_MissingAppID(t *testing.T) {
	results := CheckAppCredentials(context.Background(), nil, "", "", "7", "pem")
	if pass(results, "CYPHER_GH_APP_ID set") {
		t.Error("expected fail when app ID is empty")
	}
}

func TestCheckAppCredentials_MissingInstallID(t *testing.T) {
	results := CheckAppCredentials(context.Background(), nil, "", "42", "", "pem")
	if pass(results, "CYPHER_GH_INSTALLATION_ID set") {
		t.Error("expected fail when installation ID is empty")
	}
}

func TestCheckAppCredentials_MissingPEM(t *testing.T) {
	results := CheckAppCredentials(context.Background(), nil, "", "42", "7", "")
	if pass(results, "CYPHER_GH_APP_PRIVATE_KEY set") {
		t.Error("expected fail when PEM is empty")
	}
}

func TestCheckAppCredentials_InvalidPEM(t *testing.T) {
	results := CheckAppCredentials(context.Background(), nil, "http://unused", "42", "7", "not-a-pem")
	if pass(results, "GitHub App credentials valid") {
		t.Error("expected fail for invalid PEM")
	}
}

func TestCheckAppCredentials_APIError(t *testing.T) {
	pemData := generateDocTestPEM(t)
	s := appCredSrv(t, http.StatusUnauthorized, http.StatusOK)
	results := CheckAppCredentials(context.Background(), s.Client(), s.URL, "42", "7", string(pemData))
	if pass(results, "GitHub App credentials valid") {
		t.Error("expected fail when token exchange returns 401")
	}
}

func TestCheckAppCredentials_Success(t *testing.T) {
	pemData := generateDocTestPEM(t)
	s := appCredSrv(t, http.StatusCreated, http.StatusOK)
	results := CheckAppCredentials(context.Background(), s.Client(), s.URL, "42", "7", string(pemData))
	if !pass(results, "GitHub App credentials valid") {
		t.Errorf("expected pass, got %+v", results)
	}
	if !pass(results, "CYPHER_GH_APP_ID set") {
		t.Error("expected CYPHER_GH_APP_ID set to pass")
	}
	if !pass(results, "CYPHER_GH_INSTALLATION_ID set") {
		t.Error("expected CYPHER_GH_INSTALLATION_ID set to pass")
	}
}
