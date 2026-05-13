package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/armarquez/project-cypher/internal/config"
)

func baseCfg() *config.Config {
	return &config.Config{
		TargetRepo:     "https://github.com/org/repo",
		WorkerModel:    "gemini/gemini-2.0-flash",
		ArchitectModel: "anthropic/claude-sonnet-4-6",
		TestCommand:    "go test ./...",
		Skills:         []string{},
		Guardrails:     config.StandardGuardrails,
	}
}

func makeSkillsDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte("name: "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func hasError(issues []Issue, field string) bool {
	for _, i := range issues {
		if i.Field == field && i.Severity == Error {
			return true
		}
	}
	return false
}

func hasWarning(issues []Issue, field string) bool {
	for _, i := range issues {
		if i.Field == field && i.Severity == Warning {
			return true
		}
	}
	return false
}

// --- target_repo ---

func TestCheckTargetRepo_Valid(t *testing.T) {
	for _, v := range []string{
		"https://github.com/owner/repo",
		"owner/repo",
		"https://github.com/owner/repo.git",
	} {
		cfg := baseCfg()
		cfg.TargetRepo = v
		if issues := Check(cfg, t.TempDir()); hasError(issues, "target_repo") {
			t.Errorf("unexpected error for %q", v)
		}
	}
}

func TestCheckTargetRepo_Invalid(t *testing.T) {
	cfg := baseCfg()
	cfg.TargetRepo = "not-a-repo"
	if !hasError(Check(cfg, t.TempDir()), "target_repo") {
		t.Error("expected error for unparseable target_repo")
	}
}

// --- worker_model / architect_model ---

func TestCheckModel_KnownPrefixes(t *testing.T) {
	for _, model := range []string{
		"gemini/gemini-2.0-flash",
		"anthropic/claude-sonnet-4-6",
		"openai/gpt-4o",
		"ollama/llama3",
	} {
		cfg := baseCfg()
		cfg.WorkerModel = model
		if issues := Check(cfg, t.TempDir()); hasError(issues, "worker_model") {
			t.Errorf("unexpected error for worker_model %q", model)
		}
	}
}

func TestCheckModel_UnknownPrefix_Error(t *testing.T) {
	cfg := baseCfg()
	cfg.WorkerModel = "unknown/model"
	if !hasError(Check(cfg, t.TempDir()), "worker_model") {
		t.Error("expected error for unknown worker_model prefix")
	}
}

func TestCheckArchitectModel_NonAnthropic_Warning(t *testing.T) {
	cfg := baseCfg()
	cfg.ArchitectModel = "gemini/gemini-1.5-pro"
	if !hasWarning(Check(cfg, t.TempDir()), "architect_model") {
		t.Error("expected warning when architect_model is not anthropic/")
	}
}

func TestCheckArchitectModel_Anthropic_NoWarning(t *testing.T) {
	cfg := baseCfg()
	cfg.ArchitectModel = "anthropic/claude-opus-4-7"
	if hasWarning(Check(cfg, t.TempDir()), "architect_model") {
		t.Error("unexpected warning for anthropic architect_model")
	}
}

// --- skills ---

func TestCheckSkills_AllExist(t *testing.T) {
	dir := makeSkillsDir(t, "git-operations", "github-pr")
	cfg := baseCfg()
	cfg.Skills = []string{"git-operations", "github-pr"}
	if issues := Check(cfg, dir); len(issues) > 0 {
		t.Errorf("unexpected issues: %+v", issues)
	}
}

func TestCheckSkills_Missing(t *testing.T) {
	dir := makeSkillsDir(t, "git-operations")
	cfg := baseCfg()
	cfg.Skills = []string{"git-operations", "missing-bundle"}
	if !hasError(Check(cfg, dir), "skills[1]") {
		t.Error("expected error for missing skill bundle")
	}
}

// --- guardrails ---

func TestCheckGuardrails_StandardIDs_NoWarning(t *testing.T) {
	cfg := baseCfg()
	cfg.Guardrails = config.StandardGuardrails
	if issues := Check(cfg, t.TempDir()); len(issues) > 0 {
		t.Errorf("unexpected issues for standard guardrails: %+v", issues)
	}
}

func TestCheckGuardrails_UnknownID_Warning(t *testing.T) {
	cfg := baseCfg()
	cfg.Guardrails = []config.Guardrail{{ID: "custom:unknown-rule", Description: "custom"}}
	if !hasWarning(Check(cfg, t.TempDir()), "guardrails[0].id") {
		t.Error("expected warning for unrecognized guardrail ID")
	}
}
