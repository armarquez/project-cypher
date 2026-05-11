package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockGitHub starts a test server that serves a fixed issues response.
func mockGitHub(t *testing.T, issues []ghIssue, statusCode int) (*httptest.Server, *[]string) {
	t.Helper()
	var capturedPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPaths = append(capturedPaths, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(issues) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, &capturedPaths
}

func testClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return NewClient("test-token", http.DefaultClient, baseURL)
}

func TestListOpenIssues_ReturnsTasksFromAPI(t *testing.T) {
	issues := []ghIssue{
		{Number: 1, Title: "Fix bug", Body: "Details", HTMLURL: "https://github.com/o/r/issues/1",
			Labels: []struct{ Name string `json:"name"` }{{Name: "cypher"}}},
		{Number: 2, Title: "Add feature", Body: "More details", HTMLURL: "https://github.com/o/r/issues/2",
			Labels: []struct{ Name string `json:"name"` }{{Name: "cypher"}, {Name: "enhancement"}}},
	}
	srv, _ := mockGitHub(t, issues, http.StatusOK)

	tasks, err := testClient(t, srv.URL).ListOpenIssues(context.Background(), "owner", "repo", "cypher")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}

	if tasks[0].IssueNumber != 1 || tasks[0].Title != "Fix bug" {
		t.Errorf("task[0] = %+v, unexpected", tasks[0])
	}
	if tasks[1].IssueNumber != 2 || len(tasks[1].Labels) != 2 {
		t.Errorf("task[1] = %+v, unexpected", tasks[1])
	}
}

func TestListOpenIssues_SetsRepoOwnerAndName(t *testing.T) {
	srv, _ := mockGitHub(t, []ghIssue{{Number: 5, Title: "t", HTMLURL: "u"}}, http.StatusOK)

	tasks, err := testClient(t, srv.URL).ListOpenIssues(context.Background(), "myorg", "myrepo", "cypher")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tasks[0].RepoOwner != "myorg" || tasks[0].RepoName != "myrepo" {
		t.Errorf("RepoOwner=%q RepoName=%q, want myorg/myrepo", tasks[0].RepoOwner, tasks[0].RepoName)
	}
}

func TestListOpenIssues_EmptyList(t *testing.T) {
	srv, _ := mockGitHub(t, []ghIssue{}, http.StatusOK)

	tasks, err := testClient(t, srv.URL).ListOpenIssues(context.Background(), "o", "r", "cypher")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0", len(tasks))
	}
}

func TestListOpenIssues_SendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]ghIssue{}) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	NewClient("my-secret-pat", http.DefaultClient, srv.URL).
		ListOpenIssues(context.Background(), "o", "r", "cypher") //nolint:errcheck

	if gotAuth != "Bearer my-secret-pat" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer my-secret-pat")
	}
}

func TestListOpenIssues_SendsCorrectQueryParams(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]ghIssue{}) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	NewClient("tok", http.DefaultClient, srv.URL).
		ListOpenIssues(context.Background(), "org", "repo", "cypher") //nolint:errcheck

	for _, want := range []string{"state=open", "labels=cypher", "sort=created", "direction=asc", "per_page=100"} {
		if !strings.Contains(gotPath, want) {
			t.Errorf("query %q missing %q", gotPath, want)
		}
	}
}

func TestListOpenIssues_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	_, err := testClient(t, srv.URL).ListOpenIssues(context.Background(), "o", "r", "cypher")
	if err == nil {
		t.Error("expected error for 401 response, got nil")
	}
}

func TestListOpenIssues_Pagination(t *testing.T) {
	// First page: 100 issues (full page → fetch next). Second page: 1 issue (partial → stop).
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		var batch []ghIssue
		if callCount == 1 {
			batch = make([]ghIssue, 100)
			for i := range batch {
				batch[i] = ghIssue{Number: i + 1, Title: "issue", HTMLURL: "u"}
			}
		} else {
			batch = []ghIssue{{Number: 101, Title: "last", HTMLURL: "u"}}
		}
		json.NewEncoder(w).Encode(batch) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	tasks, err := testClient(t, srv.URL).ListOpenIssues(context.Background(), "o", "r", "cypher")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 101 {
		t.Errorf("got %d tasks, want 101 (100 + 1 from page 2)", len(tasks))
	}
	if callCount != 2 {
		t.Errorf("made %d API calls, want 2", callCount)
	}
}

