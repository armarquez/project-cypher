package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/armarquez/project-cypher/internal/architect"
)

func mockArchitect(t *testing.T, reply string) *architect.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"content": []map[string]string{{"type": "text", "text": reply}},
		})
	}))
	t.Cleanup(srv.Close)
	return architect.NewWithBase("claude-test", "key", srv.URL, nil)
}

func mockArchitectError(t *testing.T, status int) *architect.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "error", status)
	}))
	t.Cleanup(srv.Close)
	return architect.NewWithBase("claude-test", "key", srv.URL, nil)
}

func TestOSSEvaluator_Run_ReturnsEvaluation(t *testing.T) {
	llm := mockArchitect(t, "This package looks well-maintained. Recommend: Adopt.")
	e := NewOSSEvaluator(llm)
	result, err := e.Run(context.Background(), "gopkg.in/yaml.v3 — YAML parsing")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result, "Adopt") {
		t.Errorf("result = %q, expected to contain 'Adopt'", result)
	}
}

func TestOSSEvaluator_Run_IncludesPackageDetailInPrompt(t *testing.T) {
	var gotMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		if len(req.Messages) > 0 {
			gotMsg = req.Messages[0].Content
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"content": []map[string]string{{"type": "text", "text": "ok"}},
		})
	}))
	t.Cleanup(srv.Close)

	llm := architect.NewWithBase("m", "k", srv.URL, nil)
	e := NewOSSEvaluator(llm)
	e.Run(context.Background(), "mypkg v1.2.3 — HTTP client") //nolint:errcheck

	if !strings.Contains(gotMsg, "mypkg v1.2.3") {
		t.Errorf("LLM message missing package detail, got: %q", gotMsg)
	}
}

func TestOSSEvaluator_Run_LLMError(t *testing.T) {
	llm := mockArchitectError(t, http.StatusServiceUnavailable)
	e := NewOSSEvaluator(llm)
	_, err := e.Run(context.Background(), "some-package")
	if err == nil {
		t.Fatal("expected error when LLM fails")
	}
}
