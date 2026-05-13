package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Guardrail is a single governance rule the Control Plane enforces for a project.
// Rules are identified by ID; the description is human-readable documentation.
//
// Standard IDs:
//
//	require_pr                — all changes must go through a pull request
//	require_ci                — CI must pass before merging
//	hitl:new-dependency       — escalate new external dependencies to human review
//	hitl:architectural-change — escalate architectural changes to human review
//	hitl:security             — escalate security implications to human review
//	oss_adoption:evaluate     — Architect reviews OSS before adoption
//	docs:require-readme-update      — README must be updated when behavior changes
//	docs:require-arch-doc-update    — architecture docs must reflect structural changes
type Guardrail struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description"`
}

// StandardGuardrails is the default set applied when a config omits the
// guardrails key entirely. It enables all built-in enforcement rules.
var StandardGuardrails = []Guardrail{
	{ID: "require_pr", Description: "All changes must go through a pull request"},
	{ID: "require_ci", Description: "CI must pass before merging"},
	{ID: "hitl:new-dependency", Description: "Escalate new external dependencies to human review"},
	{ID: "hitl:architectural-change", Description: "Escalate architectural changes to human review"},
	{ID: "hitl:security", Description: "Escalate security implications to human review"},
	{ID: "oss_adoption:evaluate", Description: "Architect reviews OSS before adoption"},
	{ID: "docs:require-readme-update", Description: "README must be updated when project behavior changes"},
	{ID: "docs:require-arch-doc-update", Description: "Architecture docs must reflect structural changes"},
}

// Config holds the per-project configuration that tells the orchestrator
// how to manage a target repository's agent session.
type Config struct {
	TargetRepo        string      `yaml:"target_repo"`
	WorkerModel       string      `yaml:"worker_model"`
	ArchitectModel    string      `yaml:"architect_model"`
	TestCommand       string      `yaml:"test_command"`
	Skills            []string    `yaml:"skills"`
	DesignConstraints string      `yaml:"design_constraints"`
	Guardrails        []Guardrail `yaml:"guardrails"`
}

// GuardrailEnabled reports whether the named rule is active for this project.
func (c *Config) GuardrailEnabled(id string) bool {
	for _, g := range c.Guardrails {
		if g.ID == id {
			return true
		}
	}
	return false
}

// Load reads and validates a project config from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %q: %w", path, err)
	}

	return &cfg, nil
}

type fieldError struct {
	field   string
	problem string
	example string
	hint    string
}

func (e fieldError) Error() string {
	msg := fmt.Sprintf("%s: %s", e.field, e.problem)
	if e.example != "" {
		msg += fmt.Sprintf("\n  expected: %s", e.example)
	}
	if e.hint != "" {
		msg += fmt.Sprintf("\n  hint: %s", e.hint)
	}
	return msg
}

func (c *Config) validate() error {
	required := []fieldError{
		{
			field:   "target_repo",
			problem: "required — the GitHub URL of the repository Cypher will work on",
			example: "https://github.com/owner/repo",
			hint:    "set target_repo in your config file",
		},
		{
			field:   "worker_model",
			problem: "required — the LLM used for implementation tasks (primary worker tier)",
			example: "gemini/gemini-2.0-flash",
			hint:    "use a gemini/, openai/, or ollama/ prefixed model name",
		},
		{
			field:   "architect_model",
			problem: "required — the LLM used for architecture review and HITL classification (Architect tier)",
			example: "anthropic/claude-sonnet-4-6",
			hint:    "use an anthropic/ prefixed model; the Architect tier is reserved for Claude",
		},
		{
			field:   "test_command",
			problem: "required — the command the worker runs to verify changes",
			example: "just check",
			hint:    "use any shell command: go test ./..., make test, just check, etc.",
		},
	}

	values := []string{c.TargetRepo, c.WorkerModel, c.ArchitectModel, c.TestCommand}
	for i, fe := range required {
		if values[i] == "" {
			return &fe
		}
	}

	if len(c.Skills) == 0 {
		return &fieldError{
			field:   "skills",
			problem: "required — list at least one skill bundle to give the worker its capabilities",
			example: "[git-operations, github-pr, go-testing]",
			hint:    "run `ls skills/` to see available bundles",
		}
	}

	if len(c.Guardrails) == 0 {
		c.Guardrails = StandardGuardrails
	}
	return nil
}
