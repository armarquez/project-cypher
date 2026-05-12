package hitl

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/armarquez/project-cypher/internal/github"
)

// --- test doubles ---

type fakeGitHub struct {
	postErr    error
	decision   github.Decision
	waitErr    error
	postedReqs []github.HITLRequest
}

func (f *fakeGitHub) PostHITLComment(_ context.Context, _, _ string, _ int, req github.HITLRequest) error {
	f.postedReqs = append(f.postedReqs, req)
	return f.postErr
}

func (f *fakeGitHub) WaitForDecision(_ context.Context, _, _ string, _ int, _ time.Duration) (github.Decision, error) {
	return f.decision, f.waitErr
}

type fakeSession struct {
	paused    bool
	resumed   bool
	pauseErr  error
	resumeErr error
}

func (s *fakeSession) Pause(_ context.Context) error {
	s.paused = true
	return s.pauseErr
}

func (s *fakeSession) Resume(_ context.Context) error {
	s.resumed = true
	return s.resumeErr
}

func newGate(gh *fakeGitHub) *Gate {
	return NewGate(gh, "owner", "repo", 42, time.Millisecond)
}

var testReq = github.HITLRequest{
	Trigger:  "new external dependency",
	Proposed: "add gopkg.in/yaml.v3",
	Context:  "config parsing",
}

// --- Gate.Enforce tests ---

func TestEnforce_ApprovalResumesSession(t *testing.T) {
	gh := &fakeGitHub{decision: github.DecisionApproved}
	sess := &fakeSession{}

	err := newGate(gh).Enforce(context.Background(), sess, testReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sess.paused {
		t.Error("session was not paused")
	}
	if !sess.resumed {
		t.Error("session was not resumed after approval")
	}
	if len(gh.postedReqs) != 1 {
		t.Errorf("posted %d comments, want 1", len(gh.postedReqs))
	}
}

func TestEnforce_RejectionDoesNotResumeSession(t *testing.T) {
	gh := &fakeGitHub{decision: github.DecisionRejected}
	sess := &fakeSession{}

	err := newGate(gh).Enforce(context.Background(), sess, testReq)
	if err == nil {
		t.Fatal("expected error on rejection, got nil")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error %q does not mention rejection", err.Error())
	}
	if !sess.paused {
		t.Error("session was not paused")
	}
	if sess.resumed {
		t.Error("session must not be resumed after rejection")
	}
}

func TestEnforce_PauseErrorAbortsEarly(t *testing.T) {
	gh := &fakeGitHub{}
	sess := &fakeSession{pauseErr: errors.New("container unreachable")}

	err := newGate(gh).Enforce(context.Background(), sess, testReq)
	if err == nil {
		t.Fatal("expected error when pause fails")
	}
	if len(gh.postedReqs) != 0 {
		t.Error("should not post comment if pause fails")
	}
	if sess.resumed {
		t.Error("should not resume if pause failed")
	}
}

func TestEnforce_PostCommentErrorAbortsEarly(t *testing.T) {
	gh := &fakeGitHub{postErr: errors.New("github rate limit")}
	sess := &fakeSession{}

	err := newGate(gh).Enforce(context.Background(), sess, testReq)
	if err == nil {
		t.Fatal("expected error when PostHITLComment fails")
	}
	if !strings.Contains(err.Error(), "github rate limit") {
		t.Errorf("error %q does not propagate underlying cause", err.Error())
	}
	if sess.resumed {
		t.Error("should not resume if comment post failed")
	}
}

func TestEnforce_WaitErrorPropagatesToCaller(t *testing.T) {
	gh := &fakeGitHub{waitErr: context.DeadlineExceeded}
	sess := &fakeSession{}

	err := newGate(gh).Enforce(context.Background(), sess, testReq)
	if err == nil {
		t.Fatal("expected error when WaitForDecision fails")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded in error chain, got: %v", err)
	}
	if sess.resumed {
		t.Error("should not resume if wait failed")
	}
}

func TestEnforce_ResumeErrorPropagates(t *testing.T) {
	gh := &fakeGitHub{decision: github.DecisionApproved}
	sess := &fakeSession{resumeErr: errors.New("cannot restart container")}

	err := newGate(gh).Enforce(context.Background(), sess, testReq)
	if err == nil {
		t.Fatal("expected error when Resume fails")
	}
	if !strings.Contains(err.Error(), "cannot restart container") {
		t.Errorf("error %q does not propagate resume failure", err.Error())
	}
}

func TestEnforce_PostedCommentMatchesRequest(t *testing.T) {
	gh := &fakeGitHub{decision: github.DecisionApproved}
	sess := &fakeSession{}

	req := github.HITLRequest{
		Trigger:      "security implication",
		Proposed:     "write token to disk",
		Context:      "worker needs to cache credentials",
		Alternatives: "use in-memory store",
	}
	newGate(gh).Enforce(context.Background(), sess, req) //nolint:errcheck

	if len(gh.postedReqs) != 1 {
		t.Fatalf("got %d posted requests, want 1", len(gh.postedReqs))
	}
	got := gh.postedReqs[0]
	if got.Trigger != req.Trigger || got.Proposed != req.Proposed {
		t.Errorf("posted request = %+v, want %+v", got, req)
	}
}
