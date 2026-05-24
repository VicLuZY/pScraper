package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/example/bc-permit-scraper/internal/model"
	sqlitestore "github.com/example/bc-permit-scraper/internal/storage/sqlite"
)

type portableImportSummary struct {
	JSONLDir string `json:"jsonl_dir"`
	SQLite   string `json:"sqlite"`
	Reset    bool   `json:"reset"`
	Current  int    `json:"current"`
	History  int    `json:"history"`
	Audit    int    `json:"audit"`
}

func runDB(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pScraper.exe db import-jsonl --jsonl data/permits-db --sqlite data/permits.sqlite")
	}
	switch args[0] {
	case "import-jsonl":
		return runImportJSONL(args[1:], stdout)
	default:
		return fmt.Errorf("unknown db command %q", args[0])
	}
}

func runImportJSONL(args []string, stdout io.Writer) error {
	var jsonlDir, sqlitePath string
	var reset bool
	fs := flag.NewFlagSet("pScraper db import-jsonl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&jsonlDir, "jsonl", "data/permits-db", "JSONL database directory")
	fs.StringVar(&sqlitePath, "sqlite", "data/permits.sqlite", "SQLite database file")
	fs.BoolVar(&reset, "reset", false, "delete existing relational rows before import")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := sqlitestore.Open(sqlitePath)
	if err != nil {
		return err
	}
	defer db.Close()
	if reset {
		if err := db.Reset(); err != nil {
			return err
		}
	}

	sum := portableImportSummary{JSONLDir: jsonlDir, SQLite: sqlitePath, Reset: reset}
	currentPath := filepath.Join(jsonlDir, "current.jsonl")
	if err := readPortableJSONL(currentPath, func(r model.PermitRecord) error {
		if err := db.PutCurrent(r); err != nil {
			return err
		}
		sum.Current++
		return nil
	}); err != nil {
		return err
	}
	historyPath := filepath.Join(jsonlDir, "history.jsonl")
	if err := readOptionalPortableJSONL(historyPath, func(evt model.StatusEvent) error {
		if err := db.AddHistory(evt); err != nil {
			return err
		}
		sum.History++
		return nil
	}); err != nil {
		return err
	}
	auditPath := filepath.Join(jsonlDir, "scrape_audit.jsonl")
	if err := readOptionalPortableJSONL(auditPath, func(a model.ScrapeAudit) error {
		if err := db.AddAudit(a); err != nil {
			return err
		}
		sum.Audit++
		return nil
	}); err != nil {
		return err
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(sum)
}

func readOptionalPortableJSONL[T any](path string, fn func(T) error) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return readPortableJSONL(path, fn)
}

func readPortableJSONL[T any](path string, fn func(T) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row T
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return fmt.Errorf("parse %s line %d: %w", path, lineNo, err)
		}
		if err := fn(row); err != nil {
			return fmt.Errorf("import %s line %d: %w", path, lineNo, err)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}
