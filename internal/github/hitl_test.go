package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// decisionServer starts a server that returns different JSON for /labels and
// /comments sub-paths on any issue endpoint.
func decisionServer(t *testing.T, labels []string, comments []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/labels") {
			var out []map[string]string
			for _, l := range labels {
				out = append(out, map[string]string{"name": l})
			}
			json.NewEncoder(w).Encode(out) //nolint:errcheck
			return
		}
		if strings.HasSuffix(r.URL.Path, "/comments") {
			var out []map[string]string
			for _, c := range comments {
				out = append(out, map[string]string{"body": c})
			}
			json.NewEncoder(w).Encode(out) //nolint:errcheck
			return
		}
		// Default: empty JSON object (for PostComment etc.)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": float64(1)}) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- PostHITLComment ---

func TestPostHITLComment_BodyContainsFields(t *testing.T) {
	srv, rec := apiServer(t, "", http.StatusCreated, map[string]any{"id": float64(1)})

	req := HITLRequest{
		Trigger:      "new external dependency",
		Proposed:     "add gopkg.in/yaml.v3",
		Context:      "need YAML parsing",
		Alternatives: "encoding/json is insufficient",
	}
	err := testClient(t, srv.URL).PostHITLComment(context.Background(), "o", "r", 7, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body, ok := rec.Body["body"].(string)
	if !ok {
		t.Fatal("body field missing from request")
	}
	for _, want := range []string{
		"HITL Escalation",
		"new external dependency",
		"add gopkg.in/yaml.v3",
		"need YAML parsing",
		"encoding/json is insufficient",
		CommentApprove,
		CommentReject,
		LabelApproved,
		LabelRejected,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("comment body missing %q", want)
		}
	}
}

func TestPostHITLComment_NoAlternativesSection(t *testing.T) {
	srv, rec := apiServer(t, "", http.StatusCreated, map[string]any{"id": float64(1)})

	req := HITLRequest{
		Trigger:  "architectural change",
		Proposed: "introduce message queue",
		Context:  "decouple components",
	}
	err := testClient(t, srv.URL).PostHITLComment(context.Background(), "o", "r", 3, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body, _ := rec.Body["body"].(string)
	if strings.Contains(body, "Alternatives") {
		t.Errorf("body should not contain Alternatives section when empty")
	}
}

// --- GetIssueLabels ---

func TestGetIssueLabels_ReturnNames(t *testing.T) {
	srv := decisionServer(t, []string{LabelApproved, "bug"}, nil)

	labels, err := testClient(t, srv.URL).GetIssueLabels(context.Background(), "o", "r", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(labels) != 2 {
		t.Fatalf("got %d labels, want 2", len(labels))
	}
	if labels[0] != LabelApproved || labels[1] != "bug" {
		t.Errorf("labels = %v, unexpected", labels)
	}
}

func TestGetIssueLabels_Empty(t *testing.T) {
	srv := decisionServer(t, nil, nil)

	labels, err := testClient(t, srv.URL).GetIssueLabels(context.Background(), "o", "r", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(labels) != 0 {
		t.Errorf("expected empty labels, got %v", labels)
	}
}

// --- checkDecision ---

func TestCheckDecision_ApprovedByLabel(t *testing.T) {
	srv := decisionServer(t, []string{LabelApproved}, nil)

	d, err := testClient(t, srv.URL).checkDecision(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != DecisionApproved {
		t.Errorf("decision = %q, want approved", d)
	}
}

func TestCheckDecision_RejectedByLabel(t *testing.T) {
	srv := decisionServer(t, []string{LabelRejected}, nil)

	d, err := testClient(t, srv.URL).checkDecision(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != DecisionRejected {
		t.Errorf("decision = %q, want rejected", d)
	}
}

func TestCheckDecision_ApprovedByComment(t *testing.T) {
	srv := decisionServer(t, nil, []string{CommentApprove})

	d, err := testClient(t, srv.URL).checkDecision(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != DecisionApproved {
		t.Errorf("decision = %q, want approved", d)
	}
}

func TestCheckDecision_RejectedByComment(t *testing.T) {
	srv := decisionServer(t, nil, []string{CommentReject})

	d, err := testClient(t, srv.URL).checkDecision(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != DecisionRejected {
		t.Errorf("decision = %q, want rejected", d)
	}
}

func TestCheckDecision_CommentCaseInsensitive(t *testing.T) {
	srv := decisionServer(t, nil, []string{"/CYPHER APPROVE"})

	d, err := testClient(t, srv.URL).checkDecision(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != DecisionApproved {
		t.Errorf("decision = %q, want approved (case-insensitive match)", d)
	}
}

func TestCheckDecision_Pending(t *testing.T) {
	srv := decisionServer(t, []string{"unrelated-label"}, []string{"some other comment"})

	d, err := testClient(t, srv.URL).checkDecision(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != DecisionPending {
		t.Errorf("decision = %q, want pending", d)
	}
}

// --- WaitForDecision ---

func TestWaitForDecision_ReturnsImmediatelyWhenApproved(t *testing.T) {
	srv := decisionServer(t, []string{LabelApproved}, nil)

	d, err := testClient(t, srv.URL).WaitForDecision(
		context.Background(), "o", "r", 1, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != DecisionApproved {
		t.Errorf("decision = %q, want approved", d)
	}
}

func TestWaitForDecision_ContextCancelledReturnsPending(t *testing.T) {
	// Server always returns pending (no matching labels or comments).
	srv := decisionServer(t, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	d, err := testClient(t, srv.URL).WaitForDecision(ctx, "o", "r", 1, 10*time.Millisecond)
	if err == nil {
		t.Error("expected context error, got nil")
	}
	if d != DecisionPending {
		t.Errorf("decision = %q, want pending on context cancel", d)
	}
}

func TestWaitForDecision_EventuallyApproved(t *testing.T) {
	// First two polls return pending labels; third returns approved.
	pollCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/labels") {
			pollCount++
			if pollCount >= 3 {
				json.NewEncoder(w).Encode([]map[string]string{{"name": LabelApproved}}) //nolint:errcheck
			} else {
				json.NewEncoder(w).Encode([]map[string]string{}) //nolint:errcheck
			}
			return
		}
		json.NewEncoder(w).Encode([]map[string]string{}) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	d, err := testClient(t, srv.URL).WaitForDecision(
		context.Background(), "o", "r", 1, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != DecisionApproved {
		t.Errorf("decision = %q, want approved", d)
	}
	if pollCount < 3 {
		t.Errorf("pollCount = %d, want >= 3", pollCount)
	}
}
