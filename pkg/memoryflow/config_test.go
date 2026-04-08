package memoryflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFilename)

	cfg := DefaultProjectConfig("apollo")
	cfg.Conventions.Rules = append(cfg.Conventions.Rules, ConventionRule{
		MatchSource: "manual",
		Wing:        "ops",
		Room:        "logbook",
	})

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.Project != "apollo" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
	if len(loaded.Conventions.Rules) == 0 {
		t.Fatalf("expected convention rules, got %+v", loaded)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	got := DefaultConfigPath("/tmp/project")
	if got != "/tmp/project/"+DefaultConfigFilename {
		t.Fatalf("unexpected config path: %s", got)
	}
	if _, err := os.Stat("/tmp/project"); err == nil {
		// no-op: path existence is irrelevant, this just keeps the import used.
	}
}
