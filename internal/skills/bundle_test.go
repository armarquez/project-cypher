package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const gitBundle = `
name: git-operations
context_pack: |
  You have access to git tools. Never force-push. Never commit to main.
tools:
  - name: git_status
    description: "Show working tree status"
    parameters: {}
    impl: sandbox_exec
  - name: git_diff
    description: "Show changes"
    parameters:
      staged:
        type: boolean
        description: "Show staged changes only"
    impl: sandbox_exec
`

const githubBundle = `
name: github-pr
context_pack: |
  You can open and manage GitHub pull requests.
tools:
  - name: pr_create
    description: "Open a pull request"
    parameters:
      title:
        type: string
        description: "PR title"
    impl: github_api
`

func writeBundleFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- Load ---

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	path := writeBundleFile(t, dir, "git.yaml", gitBundle)

	b, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Name != "git-operations" {
		t.Errorf("Name = %q, want %q", b.Name, "git-operations")
	}
	if len(b.Tools) != 2 {
		t.Errorf("Tools len = %d, want 2", len(b.Tools))
	}
}

func TestLoad_MissingName(t *testing.T) {
	dir := t.TempDir()
	path := writeBundleFile(t, dir, "bad.yaml", "context_pack: hello\ntools: []")
	if _, err := Load(path); err == nil {
		t.Error("expected error for missing name, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeBundleFile(t, dir, "bad.yaml", ":::not yaml:::")
	if _, err := Load(path); err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	writeBundleFile(t, dir, "git.yaml", gitBundle)
	writeBundleFile(t, dir, "github.yaml", githubBundle)

	bundles, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bundles) != 2 {
		t.Errorf("len = %d, want 2", len(bundles))
	}
	if _, ok := bundles["git-operations"]; !ok {
		t.Error("missing git-operations bundle")
	}
}

// --- Assemble ---

func loadTestBundles(t *testing.T) map[string]*Bundle {
	t.Helper()
	dir := t.TempDir()
	writeBundleFile(t, dir, "git.yaml", gitBundle)
	writeBundleFile(t, dir, "github.yaml", githubBundle)
	bundles, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return bundles
}

func TestAssemblePrompt(t *testing.T) {
	bundles := loadTestBundles(t)

	prompt, err := AssemblePrompt(bundles, []string{"git-operations", "github-pr"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "Never force-push") {
		t.Error("prompt missing git-operations context")
	}
	if !strings.Contains(prompt, "pull requests") {
		t.Error("prompt missing github-pr context")
	}
}

func TestAssemblePrompt_MissingBundle(t *testing.T) {
	bundles := loadTestBundles(t)
	if _, err := AssemblePrompt(bundles, []string{"nonexistent"}); err == nil {
		t.Error("expected error for unknown bundle, got nil")
	}
}

func TestAssembleTools(t *testing.T) {
	bundles := loadTestBundles(t)

	tools, err := AssembleTools(bundles, []string{"git-operations", "github-pr"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 3 { // git_status + git_diff + pr_create
		t.Errorf("tools len = %d, want 3", len(tools))
	}
}

// --- Vendor conversions ---

func getTools(t *testing.T) []Tool {
	t.Helper()
	bundles := loadTestBundles(t)
	tools, err := AssembleTools(bundles, []string{"git-operations"})
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

func TestToGemini(t *testing.T) {
	tools := getTools(t)
	gemini := ToGemini(tools)

	if len(gemini) != len(tools) {
		t.Errorf("len = %d, want %d", len(gemini), len(tools))
	}

	out, err := json.Marshal(gemini)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"name":"git_status"`) {
		t.Error("missing git_status in Gemini output")
	}
	if !strings.Contains(s, `"parameters"`) {
		t.Error("missing parameters key in Gemini output")
	}
	// Gemini must NOT have "input_schema" or "function" wrapper
	if strings.Contains(s, "input_schema") || strings.Contains(s, `"type":"function"`) {
		t.Error("Gemini output contains unexpected Anthropic/OpenAI keys")
	}
}

func TestToAnthropic(t *testing.T) {
	tools := getTools(t)
	anthropic := ToAnthropic(tools)

	out, err := json.Marshal(anthropic)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"input_schema"`) {
		t.Error("missing input_schema in Anthropic output")
	}
	if !strings.Contains(s, `"required"`) {
		t.Error("missing required array in Anthropic output")
	}
}

func TestToOpenAI(t *testing.T) {
	tools := getTools(t)
	openai := ToOpenAI(tools)

	out, err := json.Marshal(openai)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"type":"function"`) {
		t.Error("missing function wrapper in OpenAI output")
	}
	if !strings.Contains(s, `"function"`) {
		t.Error("missing function key in OpenAI output")
	}
}
