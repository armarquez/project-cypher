package agents

import (
	"context"
	"fmt"

	"github.com/armarquez/project-cypher/internal/architect"
)

const ossEvalSystem = `You are the OSS Adoption Evaluator for Project Cypher. When a worker proposes adding a new external dependency, assess whether it meets the project's security and quality bar.

Evaluate the proposed package and cover:
1. Security posture (maintenance activity, CVE history, dependency pinning)
2. Transitive dependency risk
3. Abstraction fit (would we work with it or against it?)
4. Recommendation: Adopt / Use as reference or fork / Build ourselves

Keep your response to 3-5 sentences. The human makes the final call — your role is to provide a pre-populated evaluation they can act on immediately.`

// OSSEvaluator calls the Architect LLM to assess a proposed new dependency
// before the HITL escalation comment is posted to the human.
type OSSEvaluator struct {
	llm *architect.Client
}

// NewOSSEvaluator creates an OSSEvaluator backed by llm.
func NewOSSEvaluator(llm *architect.Client) *OSSEvaluator {
	return &OSSEvaluator{llm: llm}
}

// Run evaluates a proposed OSS package and returns a markdown-formatted
// assessment for inclusion in the HITL escalation comment.
// packageDetail is the free-text from the HITL marker (package name + reason).
func (e *OSSEvaluator) Run(ctx context.Context, packageDetail string) (string, error) {
	msg := fmt.Sprintf("Evaluate this proposed new dependency:\n\n%s", packageDetail)
	result, err := e.llm.Complete(ctx, ossEvalSystem, msg)
	if err != nil {
		return "", fmt.Errorf("oss eval: %w", err)
	}
	return result, nil
}
