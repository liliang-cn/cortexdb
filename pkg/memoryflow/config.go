package memoryflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultConfigFilename is the default project-local memoryflow config file.
const DefaultConfigFilename = ".cortexdb-memoryflow.json"

// ProjectConfig stores project-local workflow defaults.
type ProjectConfig struct {
	Version     int           `json:"version"`
	Project     string        `json:"project,omitempty"`
	Conventions ConventionSet `json:"conventions"`
}

// DefaultProjectConfig returns a ready-to-save config for a project.
func DefaultProjectConfig(project string) ProjectConfig {
	project = strings.TrimSpace(project)
	return ProjectConfig{
		Version:     1,
		Project:     project,
		Conventions: DefaultConventionSet(project),
	}
}

// DefaultConfigPath returns the default config path for a project root.
func DefaultConfigPath(projectRoot string) string {
	return filepath.Join(projectRoot, DefaultConfigFilename)
}

// LoadConfig loads a project config from disk.
func LoadConfig(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode memoryflow config: %w", err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Conventions.Project == "" {
		cfg.Conventions.Project = cfg.Project
	}
	if cfg.Conventions.DefaultWing == "" && cfg.Project != "" {
		cfg.Conventions = DefaultConventionSet(cfg.Project)
	}
	return &cfg, nil
}

// SaveConfig writes a project config to disk.
func SaveConfig(path string, cfg ProjectConfig) error {
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Project != "" && cfg.Conventions.Project == "" {
		cfg.Conventions.Project = cfg.Project
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode memoryflow config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
