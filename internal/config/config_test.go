package config

import (
	"path/filepath"
	"testing"
)

func TestLoadUsesEmbeddedDefaultSourcesWhenFileMissing(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg, err := Load(filepath.Join("configs", "sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) == 0 {
		t.Fatal("expected embedded sources")
	}
}
