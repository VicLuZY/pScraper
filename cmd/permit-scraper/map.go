package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/example/bc-permit-scraper/internal/mapdata"
	webassets "github.com/example/bc-permit-scraper/web"
)

type portableMapServer struct {
	dbDir  string
	webDir string
}

func runMap(args []string, out io.Writer) error {
	var dbDir, webDir, addr string
	fs := flag.NewFlagSet("pScraper map", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&dbDir, "db", "data/permits-db", "file-backed scraper database directory")
	fs.StringVar(&webDir, "web", "web", "static web app directory")
	fs.StringVar(&addr, "addr", "127.0.0.1:8080", "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	app := portableMapServer{dbDir: dbDir, webDir: webDir}
	fmt.Fprintf(out, "permit map listening at http://%s\n", addr)
	return http.ListenAndServe(addr, app.routes())
}

func (a portableMapServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/records", a.handleRecords)
	mux.HandleFunc("/api/summary", a.handleSummary)
	mux.HandleFunc("/api/audit", a.handleAudit)
	mux.HandleFunc("/api/progress", a.handleProgress)
	mux.Handle("/", http.FileServer(a.webFS()))
	return mux
}

func (a portableMapServer) webFS() http.FileSystem {
	if a.webDir != "" {
		if info, err := os.Stat(a.webDir); err == nil && info.IsDir() {
			return http.Dir(a.webDir)
		}
	}
	sub, err := fs.Sub(webassets.FS, ".")
	if err != nil {
		return http.FS(webassets.FS)
	}
	return http.FS(sub)
}

func (a portableMapServer) handleRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	records, err := mapdata.LoadRecords(filepath.Join(a.dbDir, "current.jsonl"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, records)
}

func (a portableMapServer) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	records, err := mapdata.LoadRecords(filepath.Join(a.dbDir, "current.jsonl"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, mapdata.Summarize(a.dbDir, records))
}

func (a portableMapServer) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}
	rows, err := mapdata.LoadAudit(filepath.Join(a.dbDir, "scrape_audit.jsonl"), limit)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, rows)
}

func (a portableMapServer) handleProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	progress, err := mapdata.LoadProgress(filepath.Join(a.dbDir, "scrape_progress.json"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, progress)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if strings.Contains(err.Error(), "does not exist") {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}
