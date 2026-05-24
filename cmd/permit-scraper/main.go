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
	"sync"
	"time"

	"github.com/example/bc-permit-scraper/internal/config"
	"github.com/example/bc-permit-scraper/internal/dedupe"
	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
	progressstore "github.com/example/bc-permit-scraper/internal/progress"
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

type scrapeRunner struct {
	db       storage.Store
	client   *fetcher.Client
	opts     scrapers.Options
	progress *progressstore.Store
	failFast bool
	dryRun   bool

	dbMu  sync.Mutex
	sumMu sync.Mutex
	sum   *summary
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "scrape":
			return runScrape(args[1:], stdout)
		case "map", "serve-map":
			return runMap(args[1:], stdout)
		case "export-map", "export-static-map":
			return runMapExport(args[1:], stdout)
		case "db":
			return runDB(args[1:], stdout)
		case "import-jsonl":
			return runImportJSONL(args[1:], stdout)
		case "help", "-h", "--help":
			return printUsage(stdout)
		default:
			if !strings.HasPrefix(args[0], "-") {
				return fmt.Errorf("unknown command %q", args[0])
			}
		}
	}
	return runScrape(args, stdout)
}

func printUsage(stdout io.Writer) error {
	_, err := fmt.Fprint(stdout, `pScraper all-in-one permit scraper

Usage:
  pScraper.exe scrape [scraper flags] [--parallel N]
  pScraper.exe map [map flags]
  pScraper.exe export-map [export flags]
  pScraper.exe db import-jsonl [db flags]

For backward compatibility, scraper flags can be passed without the "scrape" subcommand.
`)
	return err
}

func runScrape(args []string, stdout io.Writer) error {
	var cfgPath, dbPath, storeKind, sourceIDs, userAgent string
	var all, tryAll, failFast, dryRun bool
	var limit, maxPages, parallel int
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
	fs.IntVar(&parallel, "parallel", 1, "number of sources to scrape concurrently")
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
	progressPath := progressPathFor(storeKind, dbPath)
	progress, err := progressstore.New(progressPath, runID, selected)
	if err != nil {
		return err
	}

	runner := &scrapeRunner{
		db:       db,
		client:   client,
		opts:     scrapers.Options{MaxPages: maxPages, Limit: limit, DryRun: dryRun},
		progress: progress,
		failFast: failFast,
		dryRun:   dryRun,
		sum:      &sum,
	}
	if err := runner.runSources(context.Background(), selected, parallel); err != nil {
		progress.CancelPending(err.Error())
		return err
	}
	progress.FinishRun()
	sum.FinishedAt = model.NowUTC()
	if len(sum.Errors) == 0 {
		sum.Errors = nil
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(sum)
}

func (r *scrapeRunner) runSources(ctx context.Context, sources []model.Source, parallel int) error {
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(sources) {
		parallel = len(sources)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan model.Source)
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for src := range jobs {
				if ctx.Err() != nil {
					continue
				}
				if err := r.runSource(ctx, src); err != nil && r.failFast {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
				}
			}
		}()
	}
	for _, src := range sources {
		if ctx.Err() != nil {
			break
		}
		jobs <- src
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

func (r *scrapeRunner) runSource(ctx context.Context, src model.Source) error {
	started := model.NowUTC()
	audit := model.ScrapeAudit{RunID: r.sum.RunID, SourceID: src.ID, SourceName: src.Name, Jurisdiction: src.Jurisdiction, Kind: src.Kind, StartedAt: started}
	r.progress.Start(src.ID)
	scraper, err := scrapers.ForKind(src.Kind)
	if err != nil {
		audit.Status = "broken_or_changed"
		audit.Message = err.Error()
		audit.FinishedAt = model.NowUTC()
		r.persistAudit(audit, true)
		r.addError(src.ID, err.Error())
		r.progress.Finish(audit)
		return err
	}
	recs, err := scraper.Scrape(ctx, r.client, src, r.opts)
	if err != nil {
		audit.FinishedAt = model.NowUTC()
		if skip, ok := scrapers.AsSkipError(err); ok {
			audit.Status = skip.Status
			audit.Skipped = true
			audit.Message = err.Error()
			r.addSkipped()
			r.persistAudit(audit, true)
		} else {
			audit.Status = "broken_or_changed"
			audit.Message = err.Error()
			r.addError(src.ID, err.Error())
			r.persistAudit(audit, true)
		}
		r.progress.Finish(audit)
		return err
	}

	seenKeys := map[string]bool{}
	for _, rec := range recs {
		rec = dedupe.Enrich(rec)
		audit.RecordsSeen++
		if rec.DedupeKey != "" {
			if seenKeys[rec.DedupeKey] {
				continue
			}
			seenKeys[rec.DedupeKey] = true
		}
		if r.dryRun {
			continue
		}
		r.dbMu.Lock()
		result, err := r.db.Upsert(rec)
		r.dbMu.Unlock()
		if err != nil {
			audit.Status = "broken_or_changed"
			audit.Message = err.Error()
			r.addError(src.ID, err.Error())
			if r.failFast {
				audit.FinishedAt = model.NowUTC()
				r.progress.Finish(audit)
				return err
			}
			continue
		}
		switch result {
		case storage.Inserted:
			audit.Inserted++
		case storage.Updated:
			audit.Updated++
		case storage.Unchanged:
			audit.Unchanged++
		}
	}
	if audit.Status == "" {
		audit.Status = "ok"
	}
	audit.FinishedAt = model.NowUTC()
	if !r.dryRun {
		r.persistAudit(audit, false)
	}
	r.addAuditToSummary(audit)
	r.progress.Finish(audit)
	return nil
}

func (r *scrapeRunner) persistAudit(audit model.ScrapeAudit, force bool) {
	if r.dryRun && !force {
		return
	}
	r.dbMu.Lock()
	defer r.dbMu.Unlock()
	_ = r.db.AddAudit(audit)
}

func (r *scrapeRunner) addAuditToSummary(audit model.ScrapeAudit) {
	r.sumMu.Lock()
	defer r.sumMu.Unlock()
	r.sum.RecordsSeen += audit.RecordsSeen
	r.sum.Inserted += audit.Inserted
	r.sum.Updated += audit.Updated
	r.sum.Unchanged += audit.Unchanged
}

func (r *scrapeRunner) addSkipped() {
	r.sumMu.Lock()
	defer r.sumMu.Unlock()
	r.sum.Skipped++
}

func (r *scrapeRunner) addError(sourceID, message string) {
	r.sumMu.Lock()
	defer r.sumMu.Unlock()
	r.sum.Errors[sourceID] = message
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

func progressPathFor(storeKind, dbPath string) string {
	if isSQLiteStore(storeKind) {
		return dbPath + ".progress.json"
	}
	return progressstore.PathForJSONLDB(dbPath)
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
