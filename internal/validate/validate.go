package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/armarquez/project-cypher/internal/config"
)

// Severity classifies how serious a validation issue is.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Issue is a single validation finding. Errors block usage; warnings are advisory.
type Issue struct {
	Field    string
	Severity Severity
	Message  string
	Got      string // the offending value, shown in output header
	Hint     string // actionable fix suggestion
}

var knownVendorPrefixes = []string{"gemini/", "anthropic/", "openai/", "ollama/"}

var knownGuardrailIDs = func() map[string]bool {
	m := make(map[string]bool, len(config.StandardGuardrails))
	for _, g := range config.StandardGuardrails {
		m[g.ID] = true
	}
	return m
}()

// Check runs deep static validation on a loaded config beyond the required-field
// checks already performed by config.Load. skillsDir is the directory containing
// skill bundle YAML files (typically "skills/").
//
// Returns all issues found. Errors must be fixed before Cypher can run correctly;
// warnings indicate likely misconfiguration but do not block execution.
func Check(cfg *config.Config, skillsDir string) []Issue {
	var issues []Issue
	issues = append(issues, checkTargetRepo(cfg.TargetRepo)...)
	issues = append(issues, checkModel("worker_model", cfg.WorkerModel, false)...)
	issues = append(issues, checkModel("architect_model", cfg.ArchitectModel, true)...)
	issues = append(issues, checkSkills(cfg.Skills, skillsDir)...)
	issues = append(issues, checkGuardrails(cfg.Guardrails)...)
	return issues
}

func checkTargetRepo(value string) []Issue {
	if _, _, err := config.ParseRepo(value); err != nil {
		return []Issue{{
			Field:    "target_repo",
			Severity: Error,
			Message:  "cannot parse as owner/repo",
			Got:      value,
			Hint:     "use https://github.com/owner/repo or owner/repo",
		}}
	}
	return nil
}

func checkModel(field, value string, warnIfNotAnthropic bool) []Issue {
	for _, prefix := range knownVendorPrefixes {
		if strings.HasPrefix(value, prefix) {
			if warnIfNotAnthropic && prefix != "anthropic/" {
				return []Issue{{
					Field:    field,
					Severity: Warning,
					Message:  "Architect tier expects a Claude model",
					Got:      value,
					Hint:     "use anthropic/claude-sonnet-4-6; routing implementation work to Claude exhausts your limited account",
				}}
			}
			return nil
		}
	}
	return []Issue{{
		Field:    field,
		Severity: Error,
		Message:  fmt.Sprintf("unknown vendor prefix — expected one of: %s", strings.Join(knownVendorPrefixes, ", ")),
		Got:      value,
		Hint:     "use a supported prefix, e.g. gemini/gemini-2.0-flash or anthropic/claude-sonnet-4-6",
	}}
}

func checkSkills(skills []string, skillsDir string) []Issue {
	var issues []Issue
	for i, name := range skills {
		path := filepath.Join(skillsDir, name+".yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			issues = append(issues, Issue{
				Field:    fmt.Sprintf("skills[%d]", i),
				Severity: Error,
				Message:  fmt.Sprintf("no skill bundle found at %s", path),
				Got:      name,
				Hint:     fmt.Sprintf("create %s or remove %q from the skills list", path, name),
			})
		}
	}
	return issues
}

func checkGuardrails(guardrails []config.Guardrail) []Issue {
	var issues []Issue
	for i, g := range guardrails {
		if !knownGuardrailIDs[g.ID] {
			issues = append(issues, Issue{
				Field:    fmt.Sprintf("guardrails[%d].id", i),
				Severity: Warning,
				Message:  "unrecognized guardrail ID — the Control Plane will not enforce this rule",
				Got:      g.ID,
				Hint:     "check standard IDs in docs/architecture.md or add enforcement in internal/orchestrator",
			})
		}
	}
	return issues
}
