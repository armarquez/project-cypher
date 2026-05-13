package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_Valid(t *testing.T) {
	path := writeTemp(t, `
target_repo: https://github.com/org/repo
worker_model: gemini/gemini-2.0-flash
architect_model: anthropic/claude-sonnet-4-5
test_command: go test ./...
skills:
  - git-operations
  - github-pr
design_constraints: "No global state."
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TargetRepo != "https://github.com/org/repo" {
		t.Errorf("TargetRepo = %q", cfg.TargetRepo)
	}
	if len(cfg.Skills) != 2 {
		t.Errorf("Skills len = %d, want 2", len(cfg.Skills))
	}
}

func TestLoad_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{
			name: "missing target_repo",
			content: `
worker_model: gemini/gemini-2.0-flash
architect_model: anthropic/claude-sonnet-4-5
test_command: go test ./...
skills: [git-operations]
`,
		},
		{
			name: "missing worker_model",
			content: `
target_repo: https://github.com/org/repo
architect_model: anthropic/claude-sonnet-4-5
test_command: go test ./...
skills: [git-operations]
`,
		},
		{
			name: "empty skills list",
			content: `
target_repo: https://github.com/org/repo
worker_model: gemini/gemini-2.0-flash
architect_model: anthropic/claude-sonnet-4-5
test_command: go test ./...
skills: []
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, tc.content)
			if _, err := Load(path); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTemp(t, ":::not valid yaml:::")
	if _, err := Load(path); err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml")); err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoad_GuardrailsDefaultWhenAbsent(t *testing.T) {
	path := writeTemp(t, `
target_repo: https://github.com/org/repo
worker_model: gemini/gemini-2.0-flash
architect_model: anthropic/claude-sonnet-4-5
test_command: go test ./...
skills: [git-operations]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Guardrails) == 0 {
		t.Fatal("expected standard guardrails to be applied when key is absent")
	}
	for _, id := range []string{
		"require_pr",
		"require_ci",
		"hitl:new-dependency",
		"hitl:architectural-change",
		"hitl:security",
		"oss_adoption:evaluate",
		"docs:require-readme-update",
		"docs:require-arch-doc-update",
	} {
		if !cfg.GuardrailEnabled(id) {
			t.Errorf("expected guardrail %q to be enabled by default", id)
		}
	}
}

func TestLoad_GuardrailsExplicit(t *testing.T) {
	path := writeTemp(t, `
target_repo: https://github.com/org/repo
worker_model: gemini/gemini-2.0-flash
architect_model: anthropic/claude-sonnet-4-5
test_command: go test ./...
skills: [git-operations]
guardrails:
  - id: require_pr
    description: "PRs only"
  - id: hitl:security
    description: "Security escalation"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.GuardrailEnabled("require_pr") {
		t.Error("require_pr should be enabled")
	}
	if !cfg.GuardrailEnabled("hitl:security") {
		t.Error("hitl:security should be enabled")
	}
	if cfg.GuardrailEnabled("hitl:new-dependency") {
		t.Error("hitl:new-dependency should not be enabled — not in explicit list")
	}
}

func TestGuardrailEnabled_EmptyList(t *testing.T) {
	cfg := &Config{}
	if cfg.GuardrailEnabled("anything") {
		t.Error("empty guardrails list should return false")
	}
}
