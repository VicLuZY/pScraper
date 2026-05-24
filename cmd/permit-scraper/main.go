package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/example/bc-permit-scraper/internal/config"
	"github.com/example/bc-permit-scraper/internal/dedupe"
	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
	"github.com/example/bc-permit-scraper/internal/scrapers"
	"github.com/example/bc-permit-scraper/internal/storage"
	sqlitestore "github.com/example/bc-permit-scraper/internal/storage/sqlite"
)

type summary struct {
	RunID       string            `json:"run_id"`
	StartedAt   string            `json:"started_at"`
	FinishedAt  string            `json:"finished_at"`
	Sources     int               `json:"sources"`
	RecordsSeen int               `json:"records_seen"`
	Inserted    int               `json:"inserted"`
	Updated     int               `json:"updated"`
	Unchanged   int               `json:"unchanged"`
	Skipped     int               `json:"skipped"`
	Errors      map[string]string `json:"errors,omitempty"`
	DBPath      string            `json:"db_path"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	var cfgPath, dbPath, storeKind, sourceIDs, userAgent string
	var all, tryAll, failFast, dryRun bool
	var limit, maxPages int
	var timeoutSec int
	fs := flag.NewFlagSet("permit-scraper", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfgPath, "sources", "configs/sources.json", "source config JSON")
	fs.StringVar(&dbPath, "db", "data/permits-db", "database directory for jsonl or SQLite file path")
	fs.StringVar(&storeKind, "store", "jsonl", "storage backend: jsonl or sqlite")
	fs.StringVar(&sourceIDs, "source", "", "comma-separated source ids; empty means enabled sources only")
	fs.BoolVar(&all, "all", false, "run all enabled sources")
	fs.BoolVar(&tryAll, "try-all", false, "attempt all config rows, recording skips for login-only/manual sources")
	fs.BoolVar(&failFast, "fail-fast", false, "stop on first source error")
	fs.BoolVar(&dryRun, "dry-run", false, "scrape and dedupe but do not write records")
	fs.IntVar(&limit, "limit", 0, "max records per source; 0 means scraper default/all")
	fs.IntVar(&maxPages, "max-pages", 0, "max pages per paginated source; 0 means no explicit cap")
	fs.IntVar(&timeoutSec, "timeout", 45, "HTTP timeout in seconds")
	fs.StringVar(&userAgent, "user-agent", os.Getenv("USER_AGENT"), "polite user-agent string")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dbPath = defaultDBPath(storeKind, dbPath)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	selected := selectSources(cfg.Sources, sourceIDs, all, tryAll)
	if len(selected) == 0 {
		return fmt.Errorf("no sources selected")
	}
	db, err := openStore(storeKind, dbPath)
	if err != nil {
		return err
	}
	if closer, ok := db.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	client := fetcher.New(userAgent, time.Duration(timeoutSec)*time.Second, 1500*time.Millisecond)
	runID := fmt.Sprintf("run-%d", time.Now().UTC().Unix())
	sum := summary{RunID: runID, StartedAt: model.NowUTC(), Sources: len(selected), Errors: map[string]string{}, DBPath: dbPath}

	for _, src := range selected {
		started := model.NowUTC()
		audit := model.ScrapeAudit{RunID: runID, SourceID: src.ID, SourceName: src.Name, Jurisdiction: src.Jurisdiction, Kind: src.Kind, StartedAt: started}
		scraper, err := scrapers.ForKind(src.Kind)
		if err != nil {
			audit.Status = "broken_or_changed"
			audit.Message = err.Error()
			audit.FinishedAt = model.NowUTC()
			_ = db.AddAudit(audit)
			sum.Errors[src.ID] = err.Error()
			if failFast {
				return err
			}
			continue
		}
		recs, err := scraper.Scrape(context.Background(), client, src, scrapers.Options{MaxPages: maxPages, Limit: limit, DryRun: dryRun})
		if err != nil {
			audit.FinishedAt = model.NowUTC()
			if skip, ok := scrapers.AsSkipError(err); ok {
				audit.Status = skip.Status
				audit.Skipped = true
				audit.Message = err.Error()
				sum.Skipped++
				_ = db.AddAudit(audit)
				if failFast {
					return err
				}
			} else {
				audit.Status = "broken_or_changed"
				audit.Message = err.Error()
				sum.Errors[src.ID] = err.Error()
				_ = db.AddAudit(audit)
				if failFast {
					return err
				}
			}
			continue
		}
		seenKeys := map[string]bool{}
		for _, r := range recs {
			r = dedupe.Enrich(r)
			sum.RecordsSeen++
			audit.RecordsSeen++
			if r.DedupeKey != "" {
				if seenKeys[r.DedupeKey] {
					continue
				}
				seenKeys[r.DedupeKey] = true
			}
			if dryRun {
				continue
			}
			result, err := db.Upsert(r)
			if err != nil {
				audit.Status = "broken_or_changed"
				audit.Message = err.Error()
				sum.Errors[src.ID] = err.Error()
				if failFast {
					return err
				}
				continue
			}
			switch result {
			case storage.Inserted:
				sum.Inserted++
				audit.Inserted++
			case storage.Updated:
				sum.Updated++
				audit.Updated++
			case storage.Unchanged:
				sum.Unchanged++
				audit.Unchanged++
			}
		}
		if audit.Status == "" {
			audit.Status = "ok"
		}
		audit.FinishedAt = model.NowUTC()
		if !dryRun {
			_ = db.AddAudit(audit)
		}
	}
	sum.FinishedAt = model.NowUTC()
	if len(sum.Errors) == 0 {
		sum.Errors = nil
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(sum)
}

func openStore(kind, path string) (storage.Store, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch {
	case kind == "" || kind == "jsonl":
		return storage.OpenJSONDB(path)
	case isSQLiteStore(kind):
		return sqlitestore.Open(path)
	default:
		return nil, fmt.Errorf("unknown storage backend %q", kind)
	}
}

func defaultDBPath(kind, path string) string {
	if isSQLiteStore(kind) && strings.TrimSpace(path) == "data/permits-db" {
		return "data/permits.sqlite"
	}
	return path
}

func isSQLiteStore(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "sqlite", "sqlite3":
		return true
	default:
		return false
	}
}

func selectSources(srcs []model.Source, sourceIDs string, all, tryAll bool) []model.Source {
	if sourceIDs != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(sourceIDs, ",") {
			want[strings.TrimSpace(id)] = true
		}
		out := []model.Source{}
		for _, s := range srcs {
			if want[s.ID] {
				out = append(out, s)
			}
		}
		return out
	}
	out := []model.Source{}
	for _, s := range srcs {
		if tryAll || s.Enabled {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
