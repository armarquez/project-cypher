package skills

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ToolParam describes a single parameter accepted by a tool.
type ToolParam struct {
	Type        string `yaml:"type"`
	Description string `yaml:"description,omitempty"`
}

// Tool is a capability exposed to a worker LLM. The impl field names the
// execution strategy the Control Plane uses when the worker invokes it.
type Tool struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description"`
	Parameters  map[string]ToolParam `yaml:"parameters"`
	Impl        string               `yaml:"impl"`
}

// Bundle is a named unit of worker capability: a system-prompt fragment
// paired with a set of tool definitions.
type Bundle struct {
	Name        string `yaml:"name"`
	ContextPack string `yaml:"context_pack"`
	Tools       []Tool `yaml:"tools"`
}

// Load reads a single skill bundle from path.
func Load(path string) (*Bundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bundle %q: %w", path, err)
	}

	var b Bundle
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse bundle %q: %w", path, err)
	}

	if b.Name == "" {
		return nil, fmt.Errorf("bundle %q missing required field: name", path)
	}

	return &b, nil
}

// LoadDir reads all *.yaml files in dir and returns bundles keyed by name.
// Files that fail to parse are returned as errors; the first error stops loading.
func LoadDir(dir string) (map[string]*Bundle, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", dir, err)
	}

	bundles := make(map[string]*Bundle, len(entries))
	for _, path := range entries {
		b, err := Load(path)
		if err != nil {
			return nil, err
		}
		bundles[b.Name] = b
	}
	return bundles, nil
}
