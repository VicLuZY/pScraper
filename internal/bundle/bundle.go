package bundle

import (
	"embed"
	"path/filepath"
	"strings"
)

const (
	DefaultSourcesPath  = "configs/sources.json"
	DefaultDBDir        = "data/permits-db"
	DefaultCurrentPath  = DefaultDBDir + "/current.jsonl"
	DefaultHistoryPath  = DefaultDBDir + "/history.jsonl"
	DefaultAuditPath    = DefaultDBDir + "/scrape_audit.jsonl"
	DefaultProgressPath = DefaultDBDir + "/scrape_progress.json"
)

// FS contains the self-contained config and current permit snapshot used by the
// single-file Windows build when no external files have been created yet.
//
//go:embed assets/configs/sources.json assets/data/permits-db/current.jsonl assets/data/permits-db/history.jsonl assets/data/permits-db/scrape_audit.jsonl assets/data/permits-db/scrape_progress.json
var FS embed.FS

func ReadDefault(path string) ([]byte, bool, error) {
	rel, ok := defaultRelPath(path)
	if !ok {
		return nil, false, nil
	}
	b, err := FS.ReadFile("assets/" + rel)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func IsDefault(path string) bool {
	_, ok := defaultRelPath(path)
	return ok
}

func defaultRelPath(path string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	clean = strings.TrimPrefix(clean, "./")
	switch clean {
	case DefaultSourcesPath, DefaultCurrentPath, DefaultHistoryPath, DefaultAuditPath, DefaultProgressPath:
		return clean, true
	default:
		return "", false
	}
}
