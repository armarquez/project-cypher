package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/armarquez/project-cypher/internal/config"
	"github.com/armarquez/project-cypher/internal/github"
	"github.com/armarquez/project-cypher/internal/hitl"
	"github.com/armarquez/project-cypher/internal/session"
	"github.com/armarquez/project-cypher/internal/skills"
)

// WorkerSession is the interface for an active OpenHands container session.
// *session.Session satisfies this interface.
type WorkerSession interface {
	hitl.Session // Pause(ctx), Resume(ctx)
	Destroy(ctx context.Context) error
}

// ghClient is the subset of github.Client the orchestrator uses.
// The same concrete *github.Client satisfies both this interface and the
// unexported hitl.githubClient interface used by hitl.NewGate.
type ghClient interface {
	ListOpenIssues(ctx context.Context, owner, repo, label string) ([]github.Task, error)
	GetDefaultBranchSHA(ctx context.Context, owner, repo, branch string) (string, error)
	CreateBranch(ctx context.Context, owner, repo, branch, baseSHA string) error
	PostComment(ctx context.Context, owner, repo string, number int, body string) error
	PostHITLComment(ctx context.Context, owner, repo string, number int, req github.HITLRequest) error
	WaitForDecision(ctx context.Context, owner, repo string, number int, pollInterval time.Duration) (github.Decision, error)
}

// OHClient is the subset of session.OpenHandsClient the orchestrator uses.
// Exported so callers can construct the func() OHClient factory required by New.
type OHClient interface {
	StartTask(ctx context.Context, prompt string) (string, error)
	FetchEvents(ctx context.Context, conversationID string, sinceID int) ([]session.Event, error)
	GetStatus(ctx context.Context, conversationID string) (session.ConversationStatus, error)
}

// sessionCreator creates fresh worker sessions on demand.
type sessionCreator interface {
	Create(ctx context.Context) (WorkerSession, error)
}

// ManagerAdapter wraps *session.Manager so it satisfies sessionCreator.
// session.Manager.Create returns the concrete *session.Session; this adapter
// converts it to the WorkerSession interface.
type ManagerAdapter struct {
	M *session.Manager
}

// Create implements sessionCreator.
func (a *ManagerAdapter) Create(ctx context.Context) (WorkerSession, error) {
	return a.M.Create(ctx)
}

// ossEvaluator is called before posting a new-dependency HITL comment to
// pre-populate the issue with an OSS security evaluation.
type ossEvaluator interface {
	Run(ctx context.Context, packageDetail string) (string, error)
}

// Orchestrator drives the main Cypher loop: fetches open issues, dispatches a
// worker session per task, monitors for HITL triggers, and handles completion.
type Orchestrator struct {
	owner    string
	repo     string
	cfg      *config.Config
	gh       ghClient
	sessions sessionCreator
	newOH    func() OHClient
	pollInt  time.Duration
	log      *slog.Logger
	ossEval  ossEvaluator              // nil when oss_adoption:evaluate is inactive or no API key
	bundles  map[string]*skills.Bundle // nil when skills dir was not loaded; warns at dispatch time
}

// New returns an Orchestrator. All dependencies are injected, making the
// orchestrator testable without live Docker or GitHub connections.
func New(
	owner, repo string,
	cfg *config.Config,
	gh ghClient,
	sessions sessionCreator,
	newOH func() OHClient,
	pollInt time.Duration,
	log *slog.Logger,
) *Orchestrator {
	if log == nil {
		log = slog.Default()
	}
	return &Orchestrator{
		owner:    owner,
		repo:     repo,
		cfg:      cfg,
		gh:       gh,
		sessions: sessions,
		newOH:    newOH,
		pollInt:  pollInt,
		log:      log,
	}
}

// WithOSSEvaluator sets the OSS evaluator used when the oss_adoption:evaluate
// guardrail is active. Returns the receiver for chaining.
func (o *Orchestrator) WithOSSEvaluator(e ossEvaluator) *Orchestrator {
	o.ossEval = e
	return o
}

// WithBundles sets the skill bundle map used to assemble worker prompts.
// Returns the receiver for chaining.
func (o *Orchestrator) WithBundles(bundles map[string]*skills.Bundle) *Orchestrator {
	o.bundles = bundles
	return o
}

// RunOnce fetches open issues labelled "cypher" and processes the oldest one.
// In a production loop, RunOnce is called repeatedly on a timer.
func (o *Orchestrator) RunOnce(ctx context.Context) error {
	tasks, err := o.gh.ListOpenIssues(ctx, o.owner, o.repo, "cypher")
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}
	if len(tasks) == 0 {
		o.log.InfoContext(ctx, "no open issues labelled cypher")
		return nil
	}
	return o.runTask(ctx, tasks[0])
}

func (o *Orchestrator) runTask(ctx context.Context, task github.Task) error {
	o.log.InfoContext(ctx, "dispatching task", "issue", task.IssueNumber, "title", task.Title)

	branch := issueBranch(task)
	sha, err := o.gh.GetDefaultBranchSHA(ctx, o.owner, o.repo, "main")
	if err != nil {
		return fmt.Errorf("get branch SHA: %w", err)
	}
	if err := o.gh.CreateBranch(ctx, o.owner, o.repo, branch, sha); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	sess, err := o.sessions.Create(ctx)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer sess.Destroy(context.Background()) //nolint:errcheck

	skillsContext, err := o.assembleSkillContext(ctx, task)
	if err != nil {
		return fmt.Errorf("assemble skill context: %w", err)
	}

	oh := o.newOH()
	prompt := buildPrompt(task, o.owner, o.repo, branch, o.cfg, skillsContext)
	convID, err := oh.StartTask(ctx, prompt)
	if err != nil {
		return fmt.Errorf("start task: %w", err)
	}

	gate := hitl.NewGate(o.gh, o.owner, o.repo, task.IssueNumber, o.pollInt)
	return o.pollUntilDone(ctx, sess, gate, oh, task, convID)
}

// assembleSkillContext builds the combined context_pack string from the skill
// bundles listed in cfg.Skills. Returns an empty string (with a warning) when
// bundles have not been loaded, so that test and bare runs degrade gracefully.
func (o *Orchestrator) assembleSkillContext(ctx context.Context, task github.Task) (string, error) {
	if o.bundles == nil {
		if len(o.cfg.Skills) > 0 {
			o.log.WarnContext(ctx, "skill bundles not loaded — worker prompt will lack capability context",
				"issue", task.IssueNumber, "skills", o.cfg.Skills)
		}
		return "", nil
	}
	return skills.AssemblePrompt(o.bundles, o.cfg.Skills)
}

func (o *Orchestrator) pollUntilDone(
	ctx context.Context,
	sess WorkerSession,
	gate *hitl.Gate,
	oh OHClient,
	task github.Task,
	convID string,
) error {
	lastEventID := 0
	for {
		events, err := oh.FetchEvents(ctx, convID, lastEventID)
		if err != nil {
			return fmt.Errorf("fetch events: %w", err)
		}

		for _, ev := range events {
			if ev.ID > lastEventID {
				lastEventID = ev.ID
			}
			for _, t := range hitl.Detect(ev.Text) {
				if !o.guardrailEnabled(t.Kind) {
					continue
				}
				req := github.HITLRequest{
					Trigger:  string(t.Kind),
					Proposed: t.Detail,
					Context:  fmt.Sprintf("issue #%d: %s", task.IssueNumber, task.Title),
				}
				if t.Kind == hitl.TriggerNewDependency && o.ossEval != nil && o.ossAdoptionEnabled() {
					eval, evalErr := o.ossEval.Run(ctx, t.Detail)
					if evalErr != nil {
						o.log.WarnContext(ctx, "oss eval failed — proceeding without pre-assessment", "err", evalErr)
					} else {
						req.OSSEvaluation = eval
					}
				}
				if err := gate.Enforce(ctx, sess, req); err != nil {
					return fmt.Errorf("hitl gate rejected: %w", err)
				}
			}
		}

		status, err := oh.GetStatus(ctx, convID)
		if err != nil {
			return fmt.Errorf("get status: %w", err)
		}
		if status.IsTerminal() {
			if status == session.StatusError {
				return fmt.Errorf("task ended with error status")
			}
			o.log.InfoContext(ctx, "task complete", "issue", task.IssueNumber)
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.pollInt):
		}
	}
}

// ossAdoptionEnabled reports whether oss_adoption:evaluate is active.
// Mirrors the "empty list = all on" behaviour of guardrailEnabled.
func (o *Orchestrator) ossAdoptionEnabled() bool {
	if len(o.cfg.Guardrails) == 0 {
		return true
	}
	return o.cfg.GuardrailEnabled("oss_adoption:evaluate")
}

// guardrailEnabled reports whether the guardrail rule that maps to the given
// HITL trigger kind is active in the project config. Safe to call with a nil
// or empty Guardrails slice — defaults to enforcing all triggers.
func (o *Orchestrator) guardrailEnabled(kind hitl.TriggerKind) bool {
	ids := map[hitl.TriggerKind]string{
		hitl.TriggerNewDependency: "hitl:new-dependency",
		hitl.TriggerArchitectural: "hitl:architectural-change",
		hitl.TriggerSecurity:      "hitl:security",
	}
	id, ok := ids[kind]
	if !ok {
		return false
	}
	if len(o.cfg.Guardrails) == 0 {
		return true // no explicit list → all triggers on
	}
	return o.cfg.GuardrailEnabled(id)
}

// issueBranch derives a valid Git branch name from the task.
func issueBranch(task github.Task) string {
	slug := strings.ToLower(task.Title)
	var b strings.Builder
	prevHyphen := false
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen {
			b.WriteRune('-')
			prevHyphen = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		return fmt.Sprintf("feat/issue-%d", task.IssueNumber)
	}
	return fmt.Sprintf("feat/issue-%d-%s", task.IssueNumber, s)
}

// buildPrompt constructs the task description sent to the OpenHands worker.
// It includes HITL marker instructions so the worker knows how to escalate.
// skillsContext is the assembled context_pack text from the project's skill
// bundles; pass an empty string when no bundles are loaded.
func buildPrompt(task github.Task, owner, repo, branch string, cfg *config.Config, skillsContext string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "You are an AI coding assistant working on a GitHub repository.\n\n")
	fmt.Fprintf(&sb, "Repository: %s/%s\n", owner, repo)
	fmt.Fprintf(&sb, "Issue:      #%d — %s\n", task.IssueNumber, task.Title)
	fmt.Fprintf(&sb, "Branch:     %s\n\n", branch)
	fmt.Fprintf(&sb, "Description:\n%s\n\n", task.Body)
	if cfg.DesignConstraints != "" {
		fmt.Fprintf(&sb, "Design constraints: %s\n\n", cfg.DesignConstraints)
	}
	fmt.Fprintf(&sb, "Test command: %s\n\n", cfg.TestCommand)
	if skillsContext != "" {
		fmt.Fprintf(&sb, "## Available capabilities\n\n%s\n\n", skillsContext)
	}
	type marker struct {
		guardrailID string
		line        string
	}
	markers := []marker{
		{"hitl:new-dependency", "  [HITL:new-dependency] <package and reason>    — any new external dependency\n"},
		{"hitl:architectural-change", "  [HITL:architectural-change] <what and why>    — changes to component communication\n"},
		{"hitl:security", "  [HITL:security] <description>                 — any security-sensitive change\n"},
	}
	var activeMarkers []marker
	for _, m := range markers {
		if len(cfg.Guardrails) == 0 || cfg.GuardrailEnabled(m.guardrailID) {
			activeMarkers = append(activeMarkers, m)
		}
	}
	if len(activeMarkers) > 0 {
		sb.WriteString("## HITL escalation markers\n\n")
		sb.WriteString("Emit the appropriate marker on its own line if you encounter any of these:\n\n")
		for _, m := range activeMarkers {
			sb.WriteString(m.line)
		}
		sb.WriteString("\nAfter emitting a marker, pause your work. The orchestrator will seek human approval\n")
		sb.WriteString("and resume your session when a decision is made.\n")
	}
	return sb.String()
}
