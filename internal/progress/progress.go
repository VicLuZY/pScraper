package progress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/example/bc-permit-scraper/internal/model"
)

const FileName = "scrape_progress.json"

type Store struct {
	path  string
	mu    sync.Mutex
	run   model.ScrapeRunProgress
	index map[string]int
}

func PathForJSONLDB(dbDir string) string {
	return filepath.Join(dbDir, FileName)
}

func New(path, runID string, sources []model.Source) (*Store, error) {
	run := model.ScrapeRunProgress{
		RunID:     runID,
		StartedAt: model.NowUTC(),
		Total:     len(sources),
		Sources:   make([]model.SourceProgress, 0, len(sources)),
	}
	index := map[string]int{}
	for _, src := range sources {
		index[src.ID] = len(run.Sources)
		run.Sources = append(run.Sources, model.SourceProgress{
			SourceID:     src.ID,
			SourceName:   src.Name,
			Jurisdiction: src.Jurisdiction,
			Kind:         src.Kind,
			Status:       "pending",
			Progress:     0,
		})
	}
	s := &Store{path: path, run: run, index: index}
	if err := s.writeLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Start(sourceID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rowLocked(sourceID)
	if !ok {
		return
	}
	row.Status = "running"
	row.StartedAt = model.NowUTC()
	row.Progress = 35
	_ = s.writeLocked()
}

func (s *Store) Finish(a model.ScrapeAudit) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rowLocked(a.SourceID)
	if !ok {
		return
	}
	wasComplete := isComplete(row.Status)
	row.SourceName = a.SourceName
	row.Jurisdiction = a.Jurisdiction
	row.Kind = a.Kind
	row.Status = a.Status
	row.Message = a.Message
	row.StartedAt = a.StartedAt
	row.FinishedAt = a.FinishedAt
	row.RecordsSeen = a.RecordsSeen
	row.Inserted = a.Inserted
	row.Updated = a.Updated
	row.Unchanged = a.Unchanged
	row.Skipped = a.Skipped
	row.Progress = 100
	if !wasComplete {
		s.run.Completed++
	}
	_ = s.writeLocked()
}

func (s *Store) CancelPending(message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.run.Sources {
		row := &s.run.Sources[i]
		if row.Status == "pending" {
			row.Status = "canceled"
			row.Message = message
			row.FinishedAt = model.NowUTC()
			row.Progress = 100
		}
	}
	s.run.FinishedAt = model.NowUTC()
	_ = s.writeLocked()
}

func (s *Store) FinishRun() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.run.FinishedAt = model.NowUTC()
	_ = s.writeLocked()
}

func (s *Store) rowLocked(sourceID string) (*model.SourceProgress, bool) {
	i, ok := s.index[sourceID]
	if !ok || i < 0 || i >= len(s.run.Sources) {
		return nil, false
	}
	return &s.run.Sources[i], true
}

func (s *Store) writeLocked() error {
	if s.path == "" {
		return nil
	}
	if dir := filepath.Dir(s.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	b, err := json.MarshalIndent(s.run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	_ = os.Remove(s.path)
	return os.Rename(tmp, s.path)
}

func isComplete(status string) bool {
	switch status {
	case "ok", "endpoint_needed", "requires_search_input", "login_or_authorized_only", "not_public_bulk", "broken_or_changed", "canceled":
		return true
	default:
		return false
	}
}
