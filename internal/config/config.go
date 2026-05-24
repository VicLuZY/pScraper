package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/example/bc-permit-scraper/internal/model"
)

type File struct {
	Version string         `json:"version"`
	Sources []model.Source `json:"sources"`
}

func Load(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	ids := map[string]bool{}
	for _, s := range f.Sources {
		if s.ID == "" {
			return File{}, fmt.Errorf("source with empty id: %s", s.Name)
		}
		if ids[s.ID] {
			return File{}, fmt.Errorf("duplicate source id: %s", s.ID)
		}
		ids[s.ID] = true
	}
	return f, nil
}
