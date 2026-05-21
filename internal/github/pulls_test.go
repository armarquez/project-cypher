package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// apiServer starts a test server that handles a single path with the given
// status and response body, and records the last inbound request.
type recorded struct {
	Method string
	Path   string
	Body   map[string]any
}

func apiServer(t *testing.T, path string, status int, resp any) (*httptest.Server, *recorded) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Method = r.Method
		rec.Path = r.URL.Path

		if r.Body != nil && r.ContentLength != 0 {
			json.NewDecoder(r.Body).Decode(&rec.Body) //nolint:errcheck
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if resp != nil {
			json.NewEncoder(w).Encode(resp) //nolint:errcheck
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// --- GetDefaultBranchSHA ---

func TestGetDefaultBranchSHA(t *testing.T) {
	srv, _ := apiServer(t, "/repos/o/r/git/ref/heads/main", http.StatusOK, map[string]any{
		"object": map[string]string{"sha": "abc123"},
	})

	sha, err := testClient(t, srv.URL).GetDefaultBranchSHA(context.Background(), "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sha != "abc123" {
		t.Errorf("SHA = %q, want %q", sha, "abc123")
	}
}

func TestGetDefaultBranchSHA_EmptySHA(t *testing.T) {
	srv, _ := apiServer(t, "", http.StatusOK, map[string]any{
		"object": map[string]string{"sha": ""},
	})
	_, err := testClient(t, srv.URL).GetDefaultBranchSHA(context.Background(), "o", "r", "main")
	if err == nil {
		t.Error("expected error for empty SHA, got nil")
	}
}

// --- CreateBranch ---

func TestCreateBranch_SendsCorrectPayload(t *testing.T) {
	srv, rec := apiServer(t, "", http.StatusCreated, map[string]any{})

	err := testClient(t, srv.URL).CreateBranch(context.Background(), "o", "r", "feat/my-branch", "sha456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", rec.Method)
	}
	if rec.Body["ref"] != "refs/heads/feat/my-branch" {
		t.Errorf("ref = %v, want refs/heads/feat/my-branch", rec.Body["ref"])
	}
	if rec.Body["sha"] != "sha456" {
		t.Errorf("sha = %v, want sha456", rec.Body["sha"])
	}
}

func TestCreateBranch_APIError(t *testing.T) {
	srv, _ := apiServer(t, "", http.StatusUnprocessableEntity,
		map[string]string{"message": "Reference already exists"})

	err := testClient(t, srv.URL).CreateBranch(context.Background(), "o", "r", "feat/dup", "sha")
	if err == nil {
		t.Error("expected error for 422, got nil")
	}
}

// --- CreatePR ---

func TestCreatePR_ReturnsCreatedPR(t *testing.T) {
	srv, rec := apiServer(t, "", http.StatusCreated, map[string]any{
		"number":   float64(42),
		"html_url": "https://github.com/o/r/pull/42",
		"node_id":  "PR_node1",
	})

	pr, err := testClient(t, srv.URL).CreatePR(context.Background(), "o", "r", CreatePRRequest{
		Title: "Fix bug",
		Body:  "Details",
		Head:  "feat/fix",
		Base:  "main",
		Draft: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr.Number != 42 {
		t.Errorf("Number = %d, want 42", pr.Number)
	}
	if pr.URL != "https://github.com/o/r/pull/42" {
		t.Errorf("URL = %q, unexpected", pr.URL)
	}
	if rec.Body["draft"] != true {
		t.Errorf("draft = %v, want true", rec.Body["draft"])
	}
	if rec.Body["head"] != "feat/fix" || rec.Body["base"] != "main" {
		t.Errorf("head/base not forwarded correctly: %v", rec.Body)
	}
}

// --- MergePR ---

func TestMergePR_SendsSquashMethod(t *testing.T) {
	srv, rec := apiServer(t, "", http.StatusOK, map[string]string{"sha": "merged"})

	err := testClient(t, srv.URL).MergePR(context.Background(), "o", "r", 42, "feat: my thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Method != http.MethodPut {
		t.Errorf("method = %q, want PUT", rec.Method)
	}
	if rec.Body["merge_method"] != "squash" {
		t.Errorf("merge_method = %v, want squash", rec.Body["merge_method"])
	}
	if rec.Body["commit_title"] != "feat: my thing" {
		t.Errorf("commit_title = %v, want 'feat: my thing'", rec.Body["commit_title"])
	}
	if !strings.Contains(rec.Path, "/42/merge") {
		t.Errorf("path = %q, want to contain /42/merge", rec.Path)
	}
}

func TestMergePR_APIError(t *testing.T) {
	srv, _ := apiServer(t, "", http.StatusMethodNotAllowed,
		map[string]string{"message": "Pull Request is not mergeable"})

	err := testClient(t, srv.URL).MergePR(context.Background(), "o", "r", 42, "title")
	if err == nil {
		t.Error("expected error for 405, got nil")
	}
}

// --- PostComment ---

func TestPostComment_SendsBody(t *testing.T) {
	srv, rec := apiServer(t, "", http.StatusCreated, map[string]any{"id": float64(1)})

	err := testClient(t, srv.URL).PostComment(context.Background(), "o", "r", 7, "LGTM!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", rec.Method)
	}
	if rec.Body["body"] != "LGTM!" {
		t.Errorf("body = %v, want 'LGTM!'", rec.Body["body"])
	}
	if !strings.Contains(rec.Path, "/issues/7/comments") {
		t.Errorf("path = %q, want to contain /issues/7/comments", rec.Path)
	}
}

// --- CloseIssue ---

func TestCloseIssue_SendsPatchWithClosedState(t *testing.T) {
	srv, rec := apiServer(t, "", http.StatusOK, map[string]string{"state": "closed"})

	err := testClient(t, srv.URL).CloseIssue(context.Background(), "o", "r", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", rec.Method)
	}
	if rec.Body["state"] != "closed" {
		t.Errorf("state = %v, want 'closed'", rec.Body["state"])
	}
	if !strings.Contains(rec.Path, "/issues/5") {
		t.Errorf("path = %q, want to contain /issues/5", rec.Path)
	}
}

// --- doMethod sends Content-Type for bodies ---

func TestDoMethod_SetsContentTypeForBody(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	testClient(t, srv.URL).CreateBranch(context.Background(), "o", "r", "b", "sha") //nolint:errcheck
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
}

// --- GetPR ---

func TestGetPR_ReturnsTitleAndBody(t *testing.T) {
	srv, _ := apiServer(t, "/repos/o/r/pulls/42", http.StatusOK, map[string]any{
		"number": 42,
		"title":  "feat: new feature",
		"body":   "implements XYZ",
	})
	pr, err := testClient(t, srv.URL).GetPR(context.Background(), "o", "r", 42)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	if pr.Number != 42 {
		t.Errorf("number = %d, want 42", pr.Number)
	}
	if pr.Title != "feat: new feature" {
		t.Errorf("title = %q, want %q", pr.Title, "feat: new feature")
	}
	if pr.Body != "implements XYZ" {
		t.Errorf("body = %q, want %q", pr.Body, "implements XYZ")
	}
}

func TestGetPR_APIError(t *testing.T) {
	srv, _ := apiServer(t, "/repos/o/r/pulls/99", http.StatusNotFound, map[string]string{"message": "not found"})
	_, err := testClient(t, srv.URL).GetPR(context.Background(), "o", "r", 99)
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

// --- GetPRFiles ---

func TestGetPRFiles_ReturnsFiles(t *testing.T) {
	srv, _ := apiServer(t, "/repos/o/r/pulls/7/files", http.StatusOK, []map[string]string{
		{"filename": "internal/foo/bar.go", "status": "modified"},
		{"filename": "docs/architecture.md", "status": "modified"},
	})
	files, err := testClient(t, srv.URL).GetPRFiles(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatalf("GetPRFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if files[0].Filename != "internal/foo/bar.go" || files[0].Status != "modified" {
		t.Errorf("files[0] = %+v, unexpected", files[0])
	}
}

func TestGetPRFiles_EmptyList(t *testing.T) {
	srv, _ := apiServer(t, "/repos/o/r/pulls/3/files", http.StatusOK, []map[string]string{})
	files, err := testClient(t, srv.URL).GetPRFiles(context.Background(), "o", "r", 3)
	if err != nil {
		t.Fatalf("GetPRFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want 0", len(files))
	}
}

func TestGetPRFiles_APIError(t *testing.T) {
	srv, _ := apiServer(t, "/repos/o/r/pulls/5/files", http.StatusForbidden, nil)
	_, err := testClient(t, srv.URL).GetPRFiles(context.Background(), "o", "r", 5)
	if err == nil {
		t.Fatal("expected error for 403")
	}
}
