package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/armarquez/project-cypher/internal/config"
	"github.com/armarquez/project-cypher/internal/github"
	"github.com/armarquez/project-cypher/internal/session"
)

// --- test doubles ---

type fakeGH struct {
	tasks    []github.Task
	listErr  error
	sha      string
	shaErr   error
	branchErr error
}

func (f *fakeGH) ListOpenIssues(_ context.Context, _, _, _ string) ([]github.Task, error) {
	return f.tasks, f.listErr
}
func (f *fakeGH) GetDefaultBranchSHA(_ context.Context, _, _, _ string) (string, error) {
	return f.sha, f.shaErr
}
func (f *fakeGH) CreateBranch(_ context.Context, _, _, _, _ string) error {
	return f.branchErr
}
func (f *fakeGH) PostComment(_ context.Context, _ string, _ string, _ int, _ string) error {
	return nil
}
func (f *fakeGH) PostHITLComment(_ context.Context, _, _ string, _ int, _ github.HITLRequest) error {
	return nil
}
func (f *fakeGH) WaitForDecision(_ context.Context, _, _ string, _ int, _ time.Duration) (github.Decision, error) {
	return github.DecisionApproved, nil
}

type fakeSession struct {
	paused    bool
	resumed   bool
	destroyed bool
}

func (s *fakeSession) Pause(_ context.Context) error   { s.paused = true; return nil }
func (s *fakeSession) Resume(_ context.Context) error  { s.resumed = true; return nil }
func (s *fakeSession) Destroy(_ context.Context) error { s.destroyed = true; return nil }

type fakeSessionCreator struct {
	sess *fakeSession
	err  error
}

func (f *fakeSessionCreator) Create(_ context.Context) (WorkerSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.sess, nil
}

type fakeOH struct {
	convID    string
	startErr  error
	events    [][]session.Event // events[i] returned on i-th FetchEvents call
	fetchCall int
	statuses  []session.ConversationStatus // statuses[i] returned on i-th GetStatus call
	statusCall int
}

func (f *fakeOH) StartTask(_ context.Context, _ string) (string, error) {
	return f.convID, f.startErr
}
func (f *fakeOH) FetchEvents(_ context.Context, _ string, _ int) ([]session.Event, error) {
	if f.fetchCall < len(f.events) {
		ev := f.events[f.fetchCall]
		f.fetchCall++
		return ev, nil
	}
	return nil, nil
}
func (f *fakeOH) GetStatus(_ context.Context, _ string) (session.ConversationStatus, error) {
	if f.statusCall < len(f.statuses) {
		s := f.statuses[f.statusCall]
		f.statusCall++
		return s, nil
	}
	return session.StatusRunning, nil
}

func testCfg() *config.Config {
	return &config.Config{
		TargetRepo:        "https://github.com/o/r",
		WorkerModel:       "gemini/gemini-2.0-flash",
		ArchitectModel:    "anthropic/claude-sonnet-4-6",
		TestCommand:       "just check",
		Skills:            []string{"git-operations"},
		DesignConstraints: "no global state",
		Guardrails:        config.StandardGuardrails,
	}
}

func testOrch(gh *fakeGH, sc *fakeSessionCreator, oh *fakeOH) *Orchestrator {
	return New(
		"o", "r",
		testCfg(),
		gh,
		sc,
		func() OHClient { return oh },
		time.Millisecond, // fast poll for tests
		nil,              // nil slog.Logger is valid; InfoContext is a no-op
	)
}

// --- issueBranch ---

func TestIssueBranch(t *testing.T) {
	cases := []struct {
		number int
		title  string
		want   string
	}{
		{42, "Fix null pointer dereference", "feat/issue-42-fix-null-pointer-dereference"},
		{1, "Add feature: foo/bar", "feat/issue-1-add-feature-foo-bar"},
		{7, "  leading spaces  ", "feat/issue-7-leading-spaces"},
		{5, "", "feat/issue-5"},
		{3, "a" + strings.Repeat("b", 50), "feat/issue-3-a" + strings.Repeat("b", 39)},
	}
	for _, tc := range cases {
		task := github.Task{IssueNumber: tc.number, Title: tc.title}
		if got := issueBranch(task); got != tc.want {
			t.Errorf("issueBranch(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

// --- buildPrompt ---

func TestBuildPrompt_ContainsRequiredFields(t *testing.T) {
	task := github.Task{IssueNumber: 42, Title: "Fix bug", Body: "Details here"}
	prompt := buildPrompt(task, "myorg", "myrepo", "feat/issue-42-fix-bug", testCfg())

	for _, want := range []string{
		"myorg/myrepo",
		"#42",
		"Fix bug",
		"Details here",
		"feat/issue-42-fix-bug",
		"just check",
		"no global state",
		"[HITL:new-dependency]",
		"[HITL:architectural-change]",
		"[HITL:security]",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildPrompt_NoDesignConstraints(t *testing.T) {
	cfg := testCfg()
	cfg.DesignConstraints = ""
	task := github.Task{IssueNumber: 1, Title: "t", Body: "b"}
	prompt := buildPrompt(task, "o", "r", "feat/issue-1-t", cfg)
	if strings.Contains(prompt, "Design constraints") {
		t.Error("prompt should not contain Design constraints section when empty")
	}
}

// --- RunOnce ---

func TestRunOnce_NoIssues(t *testing.T) {
	gh := &fakeGH{}
	orch := testOrch(gh, &fakeSessionCreator{sess: &fakeSession{}}, &fakeOH{})
	if err := orch.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error with no issues: %v", err)
	}
}

func TestRunOnce_ListError(t *testing.T) {
	gh := &fakeGH{listErr: errors.New("rate limited")}
	orch := testOrch(gh, &fakeSessionCreator{sess: &fakeSession{}}, &fakeOH{})
	if err := orch.RunOnce(context.Background()); err == nil {
		t.Error("expected error when list fails")
	}
}

func TestRunOnce_HappyPath(t *testing.T) {
	sess := &fakeSession{}
	gh := &fakeGH{
		tasks: []github.Task{{IssueNumber: 5, Title: "Fix bug", Body: "b"}},
		sha:   "abc123",
	}
	oh := &fakeOH{
		convID:   "conv-1",
		statuses: []session.ConversationStatus{session.StatusFinished},
	}
	orch := testOrch(gh, &fakeSessionCreator{sess: sess}, oh)

	if err := orch.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sess.destroyed {
		t.Error("session was not destroyed after task completion")
	}
}

func TestRunOnce_SessionErrorStopsTask(t *testing.T) {
	gh := &fakeGH{
		tasks: []github.Task{{IssueNumber: 1, Title: "t", Body: "b"}},
		sha:   "sha",
	}
	sc := &fakeSessionCreator{err: errors.New("daemon down")}
	orch := testOrch(gh, sc, &fakeOH{})
	if err := orch.RunOnce(context.Background()); err == nil {
		t.Error("expected error when session create fails")
	}
}

func TestRunOnce_ErrorStatusReturnsError(t *testing.T) {
	sess := &fakeSession{}
	gh := &fakeGH{
		tasks: []github.Task{{IssueNumber: 2, Title: "t", Body: "b"}},
		sha:   "sha",
	}
	oh := &fakeOH{
		convID:   "conv-2",
		statuses: []session.ConversationStatus{session.StatusError},
	}
	orch := testOrch(gh, &fakeSessionCreator{sess: sess}, oh)
	if err := orch.RunOnce(context.Background()); err == nil {
		t.Error("expected error when task ends with error status")
	}
	if !sess.destroyed {
		t.Error("session must be destroyed even on task error")
	}
}

// --- HITL trigger detection in poll loop ---

func TestRunOnce_HITLTriggerPausesSession(t *testing.T) {
	sess := &fakeSession{}
	gh := &fakeGH{
		tasks: []github.Task{{IssueNumber: 3, Title: "Add dep", Body: "b"}},
		sha:   "sha",
	}
	oh := &fakeOH{
		convID: "conv-3",
		// First fetch returns a HITL-marked event; second returns nothing.
		events: [][]session.Event{
			{{ID: 1, Source: "agent", Text: "[HITL:new-dependency] gopkg.in/yaml.v3"}},
		},
		// After HITL resolution, status is finished.
		statuses: []session.ConversationStatus{session.StatusFinished},
	}
	orch := testOrch(gh, &fakeSessionCreator{sess: sess}, oh)

	if err := orch.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sess.paused {
		t.Error("session should have been paused by HITL gate")
	}
	if !sess.resumed {
		t.Error("session should have been resumed after approval")
	}
}

// --- context cancellation ---

func TestRunOnce_ContextCancelledDuringPoll(t *testing.T) {
	sess := &fakeSession{}
	gh := &fakeGH{
		tasks: []github.Task{{IssueNumber: 4, Title: "t", Body: "b"}},
		sha:   "sha",
	}
	// OH always returns running — the context cancel drives exit.
	oh := &fakeOH{convID: "conv-4"}
	orch := testOrch(gh, &fakeSessionCreator{sess: sess}, oh)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := orch.RunOnce(ctx)
	if err == nil {
		t.Error("expected error on context cancel")
	}
	if !sess.destroyed {
		t.Error("session must be destroyed even when context is cancelled")
	}
}
