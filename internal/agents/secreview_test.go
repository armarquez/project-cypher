package agents

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/armarquez/project-cypher/internal/github"
)

func TestSecurityReviewer_Run_PostsComment(t *testing.T) {
	gh := &fakeGH{
		pr:    &github.PRDetail{Number: 7, Title: "Add feature", Body: "Some changes"},
		files: []github.PRFile{{Filename: "internal/foo/bar.go", Status: "added"}},
	}
	llm := mockArchitect(t, "### Security & Consistency Review\n\n**Status**: PASS")
	r := NewSecurityReviewer(llm, gh)

	if err := r.Run(context.Background(), "o", "r", 7); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gh.postedBody == "" {
		t.Error("expected a PR comment to be posted")
	}
	if !strings.Contains(gh.postedBody, "Security & Consistency Review") {
		t.Errorf("comment missing expected header, got: %q", gh.postedBody)
	}
}

func TestSecurityReviewer_Run_IncludesPRContextInPrompt(t *testing.T) {
	var captured string
	llm := capturingLLM(t, &captured, "### Security & Consistency Review\n\n**Status**: PASS")
	gh := &fakeGH{
		pr:    &github.PRDetail{Number: 42, Title: "Fix auth handler", Body: "Reworks credential loading"},
		files: []github.PRFile{{Filename: "internal/secrets/resolve.go", Status: "modified"}},
	}
	r := NewSecurityReviewer(llm, gh)
	r.Run(context.Background(), "o", "r", 42) //nolint:errcheck

	for _, want := range []string{"Fix auth handler", "Reworks credential loading", "internal/secrets/resolve.go"} {
		if !strings.Contains(captured, want) {
			t.Errorf("LLM prompt missing %q, got: %q", want, captured)
		}
	}
}

func TestSecurityReviewer_Run_GetPRError(t *testing.T) {
	gh := &fakeGH{prErr: errors.New("not found")}
	llm := mockArchitect(t, "irrelevant")
	r := NewSecurityReviewer(llm, gh)

	if err := r.Run(context.Background(), "o", "r", 1); err == nil {
		t.Fatal("expected error when GetPR fails")
	}
}

func TestSecurityReviewer_Run_LLMError(t *testing.T) {
	gh := &fakeGH{
		pr:    &github.PRDetail{Number: 1, Title: "t", Body: "b"},
		files: nil,
	}
	llm := mockArchitectError(t, http.StatusInternalServerError)
	r := NewSecurityReviewer(llm, gh)

	if err := r.Run(context.Background(), "o", "r", 1); err == nil {
		t.Fatal("expected error when LLM fails")
	}
}
