package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/armarquez/project-cypher/internal/architect"
	"github.com/armarquez/project-cypher/internal/github"
)

// docReviewSystem is adapted from skills/documentation-agent.yaml.
// The full context_pack is the source of truth; this is a condensed version
// for the custom agent path until the migration from skill bundle is complete.
const docReviewSystem = `You are the Documentation Agent for Project Cypher. Your sole responsibility is reviewing pull requests for documentation completeness. You do not write code, suggest implementation changes, or merge PRs. You identify gaps and post a single structured review comment.

## What to check

### 1. README.md
A README update is required when the PR changes any of:
- How the project is set up or installed
- Commands a developer or operator runs (justfile recipes, env vars, CLI flags)
- Project status, capabilities, or entry points

### 2. docs/architecture.md
An update is required when the PR introduces or changes:
- How components communicate with each other
- A new internal package, service, or layer
- A new external actor or system dependency
- A security boundary or trust model
- The rationale for a design choice over an alternative

## Review output format

Post exactly one comment using this structure:

### Documentation Review

**Status**: PASS | NEEDS WORK

| Check | Result | Notes |
|---|---|---|
| README.md | ✅ not required / ✅ updated / ❌ missing update | |
| docs/architecture.md | ✅ not required / ✅ updated / ❌ missing update | |

If status is NEEDS WORK, list each required action as a checkbox.

## Hard rules
- Do not comment on code quality, test coverage, or implementation choices.
- Do not request changes beyond documentation gaps.
- Do not flag a doc as missing if it genuinely does not require updating.`

// ghClient is the subset of github.Client DocumentationAgent needs.
type ghClient interface {
	GetPR(ctx context.Context, owner, repo string, number int) (*github.PRDetail, error)
	GetPRFiles(ctx context.Context, owner, repo string, number int) ([]github.PRFile, error)
	PostComment(ctx context.Context, owner, repo string, number int, body string) error
}

// DocumentationAgent reviews a PR for documentation completeness using the
// Architect LLM, then posts the result as a PR comment.
type DocumentationAgent struct {
	llm *architect.Client
	gh  ghClient
}

// NewDocumentationAgent creates a DocumentationAgent.
func NewDocumentationAgent(llm *architect.Client, gh ghClient) *DocumentationAgent {
	return &DocumentationAgent{llm: llm, gh: gh}
}

// Run reviews PR number for documentation completeness.
// activeChecks is the subset of docs:* guardrail IDs that are active for this
// project (e.g. ["docs:require-readme-update", "docs:require-arch-doc-update"]).
func (a *DocumentationAgent) Run(ctx context.Context, owner, repo string, prNumber int, activeChecks []string) error {
	pr, err := a.gh.GetPR(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("doc review: get PR: %w", err)
	}
	files, err := a.gh.GetPRFiles(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("doc review: get PR files: %w", err)
	}

	fileLines := make([]string, len(files))
	for i, f := range files {
		fileLines[i] = fmt.Sprintf("  %s (%s)", f.Filename, f.Status)
	}

	msg := fmt.Sprintf(
		"PR #%d: %s\n\nDescription:\n%s\n\nChanged files:\n%s\n\nActive doc guardrails: %s",
		pr.Number,
		pr.Title,
		pr.Body,
		strings.Join(fileLines, "\n"),
		strings.Join(activeChecks, ", "),
	)

	review, err := a.llm.Complete(ctx, docReviewSystem, msg)
	if err != nil {
		return fmt.Errorf("doc review: llm: %w", err)
	}

	if err := a.gh.PostComment(ctx, owner, repo, prNumber, review); err != nil {
		return fmt.Errorf("doc review: post comment: %w", err)
	}
	return nil
}
