package hitl

import (
	"context"
	"fmt"
	"time"

	"github.com/armarquez/project-cypher/internal/github"
)

// Session is implemented by the session manager to suspend and resume
// an active worker. Pause must be idempotent; Resume is a no-op if never paused.
type Session interface {
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
}

// githubClient is the subset of github.Client required by Gate.
// Defined as an interface so tests can substitute a fake without an HTTP server.
type githubClient interface {
	PostHITLComment(ctx context.Context, owner, repo string, number int, req github.HITLRequest) error
	WaitForDecision(ctx context.Context, owner, repo string, number int, pollInterval time.Duration) (github.Decision, error)
}

// Gate enforces the HITL protocol: it pauses a worker session, escalates to
// the human via a GitHub comment, waits for approval or rejection, then
// resumes or aborts accordingly.
type Gate struct {
	client       githubClient
	owner        string
	repo         string
	issueNumber  int
	pollInterval time.Duration
}

// NewGate returns a Gate that posts escalations on the given issue and polls
// at the given interval. pollInterval is typically 30s–60s in production.
func NewGate(client githubClient, owner, repo string, issueNumber int, pollInterval time.Duration) *Gate {
	return &Gate{
		client:       client,
		owner:        owner,
		repo:         repo,
		issueNumber:  issueNumber,
		pollInterval: pollInterval,
	}
}

// Enforce pauses the session, posts a structured HITL escalation comment, and
// blocks until the human approves or rejects the proposal.
//
// On approval: session is resumed and nil is returned.
// On rejection: an error is returned and the session remains paused.
// If ctx is cancelled while waiting: an error is returned and session remains paused.
func (g *Gate) Enforce(ctx context.Context, session Session, req github.HITLRequest) error {
	if err := session.Pause(ctx); err != nil {
		return fmt.Errorf("hitl: pause session: %w", err)
	}

	if err := g.client.PostHITLComment(ctx, g.owner, g.repo, g.issueNumber, req); err != nil {
		return fmt.Errorf("hitl: post escalation comment: %w", err)
	}

	decision, err := g.client.WaitForDecision(ctx, g.owner, g.repo, g.issueNumber, g.pollInterval)
	if err != nil {
		return fmt.Errorf("hitl: wait for decision: %w", err)
	}

	switch decision {
	case github.DecisionApproved:
		if err := session.Resume(ctx); err != nil {
			return fmt.Errorf("hitl: resume session after approval: %w", err)
		}
		return nil
	case github.DecisionRejected:
		return fmt.Errorf("hitl: proposal rejected by human")
	default:
		return fmt.Errorf("hitl: unexpected decision %q", decision)
	}
}
