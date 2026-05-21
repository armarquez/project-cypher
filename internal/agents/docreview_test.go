package agents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/armarquez/project-cypher/internal/architect"
	"github.com/armarquez/project-cypher/internal/github"
)

// fakeGH is a test double for ghClient.
type fakeGH struct {
	pr         *github.PRDetail
	prErr      error
	files      []github.PRFile
	filesErr   error
	postedBody string
	postErr    error
}

func (f *fakeGH) GetPR(_ context.Context, _, _ string, _ int) (*github.PRDetail, error) {
	return f.pr, f.prErr
}
func (f *fakeGH) GetPRFiles(_ context.Context, _, _ string, _ int) ([]github.PRFile, error) {
	return f.files, f.filesErr
}
func (f *fakeGH) PostComment(_ context.Context, _, _ string, _ int, body string) error {
	f.postedBody = body
	return f.postErr
}

// capturingLLM returns an *architect.Client whose server captures the last user
// message into *captured and replies with reply.
func capturingLLM(t *testing.T, captured *string, reply string) *architect.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		if len(req.Messages) > 0 {
			*captured = req.Messages[0].Content
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"content": []map[string]string{{"type": "text", "text": reply}},
		})
	}))
	t.Cleanup(srv.Close)
	return architect.NewWithBase("m", "k", srv.URL, nil)
}

func TestDocumentationAgent_Run_PostsReview(t *testing.T) {
	llm := mockArchitect(t, "### Documentation Review\n\n**Status**: PASS")
	gh := &fakeGH{
		pr: &github.PRDetail{
			Number: 7,
			Title:  "feat: add webhook handler",
			Body:   "Adds POST /webhook endpoint",
		},
		files: []github.PRFile{
			{Filename: "internal/gateway/webhook.go", Status: "added"},
			{Filename: "docs/architecture.md", Status: "modified"},
		},
	}
	a := NewDocumentationAgent(llm, gh)
	if err := a.Run(context.Background(), "o", "r", 7, []string{"docs:require-arch-doc-update"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(gh.postedBody, "Documentation Review") {
		t.Errorf("posted comment missing review header, got: %q", gh.postedBody)
	}
}

func TestDocumentationAgent_Run_IncludesPRContextInPrompt(t *testing.T) {
	var captured string
	llm := capturingLLM(t, &captured, "### Documentation Review\n\n**Status**: PASS")
	gh := &fakeGH{
		pr:    &github.PRDetail{Number: 42, Title: "fix: bug in config loader", Body: "fixes parsing"},
		files: []github.PRFile{{Filename: "internal/config/config.go", Status: "modified"}},
	}
	a := NewDocumentationAgent(llm, gh)
	a.Run(context.Background(), "o", "r", 42, []string{"docs:require-readme-update"}) //nolint:errcheck

	if !strings.Contains(captured, "fix: bug in config loader") {
		t.Errorf("LLM message missing PR title, got: %q", captured)
	}
	if !strings.Contains(captured, "internal/config/config.go") {
		t.Errorf("LLM message missing changed file, got: %q", captured)
	}
	if !strings.Contains(captured, "docs:require-readme-update") {
		t.Errorf("LLM message missing active guardrail, got: %q", captured)
	}
}

func TestDocumentationAgent_Run_GetPRError(t *testing.T) {
	llm := mockArchitect(t, "ok")
	gh := &fakeGH{prErr: errors.New("not found")}
	a := NewDocumentationAgent(llm, gh)
	if err := a.Run(context.Background(), "o", "r", 99, nil); err == nil {
		t.Fatal("expected error when GetPR fails")
	}
}

func TestDocumentationAgent_Run_GetFilesError(t *testing.T) {
	llm := mockArchitect(t, "ok")
	gh := &fakeGH{
		pr:       &github.PRDetail{Number: 1, Title: "t", Body: "b"},
		filesErr: errors.New("api error"),
	}
	a := NewDocumentationAgent(llm, gh)
	if err := a.Run(context.Background(), "o", "r", 1, nil); err == nil {
		t.Fatal("expected error when GetPRFiles fails")
	}
}

func TestDocumentationAgent_Run_LLMError(t *testing.T) {
	llm := mockArchitectError(t, 503)
	gh := &fakeGH{
		pr:    &github.PRDetail{Number: 2, Title: "t", Body: "b"},
		files: []github.PRFile{},
	}
	a := NewDocumentationAgent(llm, gh)
	if err := a.Run(context.Background(), "o", "r", 2, nil); err == nil {
		t.Fatal("expected error when LLM fails")
	}
}

func TestDocumentationAgent_Run_PostCommentError(t *testing.T) {
	llm := mockArchitect(t, "### Documentation Review\n\n**Status**: PASS")
	gh := &fakeGH{
		pr:      &github.PRDetail{Number: 3, Title: "t", Body: "b"},
		files:   []github.PRFile{},
		postErr: errors.New("forbidden"),
	}
	a := NewDocumentationAgent(llm, gh)
	if err := a.Run(context.Background(), "o", "r", 3, nil); err == nil {
		t.Fatal("expected error when PostComment fails")
	}
}
