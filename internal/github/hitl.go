package github

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Decision represents a human's response to a HITL escalation.
type Decision string

const (
	DecisionApproved Decision = "approved"
	DecisionRejected Decision = "rejected"
	DecisionPending  Decision = "pending"

	// Approval/rejection signals recognized in issue comments and labels.
	LabelApproved = "cypher:approved"
	LabelRejected = "cypher:rejected"
	CommentApprove = "/cypher approve"
	CommentReject  = "/cypher reject"
)

// HITLRequest describes a proposed change that requires human sign-off.
type HITLRequest struct {
	Trigger       string // e.g. "new external dependency", "architectural change"
	Proposed      string // what is being proposed
	Context       string // why it is being proposed
	Alternatives  string // alternatives considered
	OSSEvaluation string // pre-populated OSS security evaluation (new-dependency triggers only)
}

func (r HITLRequest) format() string {
	var b strings.Builder
	b.WriteString("## 🛑 HITL Escalation — Human Approval Required\n\n")
	fmt.Fprintf(&b, "**Trigger:** %s\n\n", r.Trigger)
	fmt.Fprintf(&b, "**Proposed:** %s\n\n", r.Proposed)
	fmt.Fprintf(&b, "**Context:** %s\n\n", r.Context)
	if r.Alternatives != "" {
		fmt.Fprintf(&b, "**Alternatives considered:** %s\n\n", r.Alternatives)
	}
	if r.OSSEvaluation != "" {
		b.WriteString("### OSS Evaluation (Architect pre-assessment)\n\n")
		fmt.Fprintf(&b, "%s\n\n", r.OSSEvaluation)
	}
	b.WriteString("---\n\n")
	b.WriteString("To approve: add label `cypher:approved` or comment `/cypher approve`\n")
	b.WriteString("To reject:  add label `cypher:rejected` or comment `/cypher reject`\n")
	return b.String()
}

// PostHITLComment posts a structured HITL escalation comment on an issue or PR.
func (c *Client) PostHITLComment(ctx context.Context, owner, repo string, number int, req HITLRequest) error {
	return c.PostComment(ctx, owner, repo, number, req.format())
}

// ghLabel is used to decode the labels endpoint response.
type ghLabel struct {
	Name string `json:"name"`
}

// ghComment is used to decode the comments endpoint response.
type ghComment struct {
	Body string `json:"body"`
}

// GetIssueLabels returns the label names on the given issue.
func (c *Client) GetIssueLabels(ctx context.Context, owner, repo string, number int) ([]string, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels", owner, repo, number)
	var labels []ghLabel
	if err := c.do(ctx, path, &labels); err != nil {
		return nil, fmt.Errorf("get labels for %s/%s#%d: %w", owner, repo, number, err)
	}
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}
	return names, nil
}

// getIssueComments returns all comment bodies on the given issue.
func (c *Client) getIssueComments(ctx context.Context, owner, repo string, number int) ([]string, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	var comments []ghComment
	if err := c.do(ctx, path, &comments); err != nil {
		return nil, fmt.Errorf("get comments for %s/%s#%d: %w", owner, repo, number, err)
	}
	bodies := make([]string, len(comments))
	for i, c := range comments {
		bodies[i] = c.Body
	}
	return bodies, nil
}

// checkDecision reads current labels and comments to determine whether a
// decision has been made. Returns DecisionPending if no signal is found yet.
func (c *Client) checkDecision(ctx context.Context, owner, repo string, number int) (Decision, error) {
	labels, err := c.GetIssueLabels(ctx, owner, repo, number)
	if err != nil {
		return DecisionPending, err
	}
	for _, l := range labels {
		switch l {
		case LabelApproved:
			return DecisionApproved, nil
		case LabelRejected:
			return DecisionRejected, nil
		}
	}

	comments, err := c.getIssueComments(ctx, owner, repo, number)
	if err != nil {
		return DecisionPending, err
	}
	for _, body := range comments {
		trimmed := strings.TrimSpace(body)
		if strings.EqualFold(trimmed, CommentApprove) {
			return DecisionApproved, nil
		}
		if strings.EqualFold(trimmed, CommentReject) {
			return DecisionRejected, nil
		}
	}

	return DecisionPending, nil
}

// WaitForDecision polls until the human approves or rejects the escalation,
// or until ctx is cancelled. pollInterval controls how often the API is queried.
func (c *Client) WaitForDecision(ctx context.Context, owner, repo string, number int, pollInterval time.Duration) (Decision, error) {
	for {
		decision, err := c.checkDecision(ctx, owner, repo, number)
		if err != nil {
			return DecisionPending, err
		}
		if decision != DecisionPending {
			return decision, nil
		}

		select {
		case <-ctx.Done():
			return DecisionPending, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
