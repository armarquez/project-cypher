package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
		if r.URL.Path == "/v1.41/info" {
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

func writeFakeOp(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "op")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake op: %v", err)
	}
	return bin
}

func TestCheckSecrets_NoOpRefs(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "ghp_plaintoken")
	op := &secrets.OnePassword{OpPath: "/nonexistent/op"}
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, op)
	if len(results) != 0 {
		t.Errorf("expected no results when no op:// refs, got %d", len(results))
	}
}

func TestCheckSecrets_OpCLIMissing(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "op://vault/item/field")
	op := &secrets.OnePassword{OpPath: "/nonexistent/op"}
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, op)
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if pass(results, "1Password CLI (op) installed") {
		t.Error("expected fail when op CLI is missing")
	}
	r := results[0]
	if r.Fix == "" {
		t.Error("expected fix hint")
	}
}

func TestCheckSecrets_ResolveSuccess(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "op://vault/item/field")
	bin := writeFakeOp(t, "printf '%s' mysecret")
	op := &secrets.OnePassword{OpPath: bin}
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, op)
	if !pass(results, "1Password CLI (op) installed") {
		t.Error("expected op CLI check to pass")
	}
	if !pass(results, "CYPHER_GH_TOKEN accessible via op") {
		t.Errorf("expected secret accessible, results: %v", results)
	}
}

func TestCheckSecrets_ResolveFails(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "op://vault/item/field")
	bin := writeFakeOp(t, "echo 'not signed in' >&2; exit 1")
	op := &secrets.OnePassword{OpPath: bin}
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, op)
	if !pass(results, "1Password CLI (op) installed") {
		t.Error("expected op CLI check to pass")
	}
	if pass(results, "CYPHER_GH_TOKEN accessible via op") {
		t.Error("expected secret check to fail when op read exits non-zero")
	}
}

func TestCheckSecrets_UnsetVarSkipped(t *testing.T) {
	t.Setenv("CYPHER_GH_TOKEN", "")
	op := &secrets.OnePassword{OpPath: "/nonexistent/op"}
	results := CheckSecrets(context.Background(), []string{"CYPHER_GH_TOKEN"}, op)
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
