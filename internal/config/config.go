package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the per-project configuration that tells the orchestrator
// how to manage a target repository's agent session.
type Config struct {
	TargetRepo        string   `yaml:"target_repo"`
	WorkerModel       string   `yaml:"worker_model"`
	ArchitectModel    string   `yaml:"architect_model"`
	TestCommand       string   `yaml:"test_command"`
	Skills            []string `yaml:"skills"`
	DesignConstraints string   `yaml:"design_constraints"`
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

func (c *Config) validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"target_repo", c.TargetRepo},
		{"worker_model", c.WorkerModel},
		{"architect_model", c.ArchitectModel},
		{"test_command", c.TestCommand},
	}
	for _, f := range required {
		if f.value == "" {
			return fmt.Errorf("missing required field: %s", f.name)
		}
	}
	if len(c.Skills) == 0 {
		return fmt.Errorf("missing required field: skills (must list at least one skill bundle)")
	}
	return nil
}
