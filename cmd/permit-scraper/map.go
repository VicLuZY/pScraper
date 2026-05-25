package main

import (
	"database/sql"
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
	"time"

	"github.com/example/bc-permit-scraper/internal/mapdata"
	webassets "github.com/example/bc-permit-scraper/web"
	_ "modernc.org/sqlite"
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
	mux.HandleFunc("/api/vancouver-progress", a.handleVancouverProgress)
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

type vancouverProgressResponse struct {
	IndexTotal     int                  `json:"index_total"`
	CurrentRecords int                  `json:"current_records"`
	WithDetailURL  int                  `json:"with_detail_url"`
	Scraped        int                  `json:"scraped"`
	Scraping       int                  `json:"scraping"`
	Errors         int                  `json:"errors"`
	NotProcessed   int                  `json:"not_processed"`
	Remaining      int                  `json:"remaining"`
	Percent        float64              `json:"percent"`
	MinApplied     string               `json:"min_applied,omitempty"`
	MaxApplied     string               `json:"max_applied,omitempty"`
	StartDate      string               `json:"start_date,omitempty"`
	EndDate        string               `json:"end_date,omitempty"`
	IndexDB        string               `json:"index_db"`
	PermitDB       string               `json:"permit_db"`
	Days           []vancouverMatrixDay `json:"days,omitempty"`
}

type vancouverMatrixDay struct {
	Date         string `json:"date"`
	Total        int    `json:"total"`
	NotProcessed int    `json:"not_processed"`
	Scraped      int    `json:"scraped"`
	Scraping     int    `json:"scraping"`
	Error        int    `json:"error"`
}

func (a portableMapServer) handleVancouverProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := vancouverProgressResponse{
		IndexDB:  filepath.Join(a.dbDir, "vancouver_posse_index.sqlite"),
		PermitDB: filepath.Join(a.dbDir, "permits.sqlite"),
		EndDate:  vancouverProgressToday(),
	}
	if _, err := os.Stat(resp.IndexDB); err != nil {
		writeJSON(w, resp)
		return
	}

	db, err := sql.Open("sqlite", resp.IndexDB)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	defer db.Close()

	hasIndex, err := sqliteTableExists(db, "main", "vancouver_posse_index")
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if !hasIndex {
		writeJSON(w, resp)
		return
	}
	if err := ensureVancouverProgressIndexSchema(db); err != nil {
		writeAPIError(w, err)
		return
	}

	row := db.QueryRow(`SELECT
		COUNT(*),
		COALESCE(MIN(NULLIF(created_date, '')), ''),
		COALESCE(MAX(NULLIF(created_date, '')), '')
		FROM vancouver_posse_index`)
	if err := row.Scan(&resp.IndexTotal, &resp.StartDate, &resp.MaxApplied); err != nil {
		writeAPIError(w, err)
		return
	}
	resp.MinApplied = resp.StartDate

	permitAttached, err := attachVancouverPermitDB(db, resp.PermitDB)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if permitAttached {
		if err := reconcileVancouverScrapedDetails(db); err != nil {
			writeAPIError(w, err)
			return
		}
		row := db.QueryRow(`SELECT
			COUNT(*),
			COALESCE(MIN(NULLIF(applied_date, '')), ''),
			COALESCE(MAX(NULLIF(applied_date, '')), '')
			FROM permitdb.permit_current
			WHERE source_id = 'vancouver_public_permit_search'`)
		if err := row.Scan(&resp.CurrentRecords, &resp.MinApplied, &resp.MaxApplied); err != nil {
			writeAPIError(w, err)
			return
		}
	}

	days, err := queryVancouverMatrixDays(db)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	resp.Days = days
	for _, day := range days {
		resp.Scraped += day.Scraped
		resp.Scraping += day.Scraping
		resp.Errors += day.Error
		resp.NotProcessed += day.NotProcessed
	}
	resp.WithDetailURL = resp.Scraped
	if resp.IndexTotal > 0 && resp.NotProcessed+resp.Scraped+resp.Scraping+resp.Errors < resp.IndexTotal {
		resp.NotProcessed = resp.IndexTotal - resp.Scraped - resp.Scraping - resp.Errors
	}
	if resp.NotProcessed < 0 {
		resp.NotProcessed = 0
	}
	if resp.IndexTotal > 0 {
		resp.Remaining = resp.IndexTotal - resp.Scraped
		if resp.Remaining < 0 {
			resp.Remaining = 0
		}
		resp.Percent = float64(resp.Scraped) / float64(resp.IndexTotal) * 100
		if resp.Percent > 100 {
			resp.Percent = 100
		}
	}
	writeJSON(w, resp)
}

func ensureVancouverProgressIndexSchema(db *sql.DB) error {
	statements := []string{
		"ALTER TABLE vancouver_posse_index ADD COLUMN detail_status TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE vancouver_posse_index ADD COLUMN detail_started_at TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE vancouver_posse_index ADD COLUMN detail_finished_at TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE vancouver_posse_index ADD COLUMN detail_error TEXT NOT NULL DEFAULT ''",
		"CREATE INDEX IF NOT EXISTS idx_vancouver_posse_detail_status ON vancouver_posse_index(detail_status)",
		"CREATE INDEX IF NOT EXISTS idx_vancouver_posse_detail_url ON vancouver_posse_index(detail_url)",
		"CREATE INDEX IF NOT EXISTS idx_vancouver_posse_created_status ON vancouver_posse_index(created_date, detail_status)",
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	return nil
}

func attachVancouverPermitDB(db *sql.DB, permitDB string) (bool, error) {
	if _, err := os.Stat(permitDB); err != nil {
		return false, nil
	}
	if _, err := db.Exec("ATTACH DATABASE " + sqliteStringLiteral(permitDB) + " AS permitdb"); err != nil {
		return false, err
	}
	hasPermit, err := sqliteTableExists(db, "permitdb", "permit_current")
	if err != nil || !hasPermit {
		return false, err
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS permitdb.idx_permit_current_source_url ON permit_current(source_id, url)"); err != nil {
		return false, err
	}
	return true, nil
}

func reconcileVancouverScrapedDetails(db *sql.DB) error {
	_, err := db.Exec(`UPDATE vancouver_posse_index
		SET detail_status = 'scraped',
			detail_finished_at = CASE
				WHEN detail_finished_at IS NULL OR detail_finished_at = '' THEN datetime('now')
				ELSE detail_finished_at
			END
		WHERE detail_status = ''
		  AND detail_url IN (
			SELECT url
			FROM permitdb.permit_current
			WHERE source_id = 'vancouver_public_permit_search'
			  AND url IS NOT NULL
			  AND url != ''
		  )`)
	return err
}

func queryVancouverMatrixDays(db *sql.DB) ([]vancouverMatrixDay, error) {
	query := `SELECT
		COALESCE(NULLIF(i.created_date, ''), 'Undated') AS created_date,
		CASE
			WHEN i.detail_status = 'scraping' THEN 'scraping'
			WHEN i.detail_status = 'error' THEN 'error'
			WHEN i.detail_status = 'scraped' THEN 'scraped'
			ELSE 'not_processed'
		END AS detail_state,
		COUNT(*) AS record_count
		FROM vancouver_posse_index i
		GROUP BY created_date, detail_state
		ORDER BY created_date, detail_state`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	days := []vancouverMatrixDay{}
	dayByDate := map[string]*vancouverMatrixDay{}
	for rows.Next() {
		var date, status string
		var count int
		if err := rows.Scan(&date, &status, &count); err != nil {
			return nil, err
		}
		day := dayByDate[date]
		if day == nil {
			days = append(days, vancouverMatrixDay{Date: date})
			day = &days[len(days)-1]
			dayByDate[date] = day
		}
		day.Total += count
		switch status {
		case "scraped":
			day.Scraped += count
		case "scraping":
			day.Scraping += count
		case "error":
			day.Error += count
		default:
			day.NotProcessed += count
		}
	}
	return days, rows.Err()
}

func sqliteTableExists(db *sql.DB, schemaName, tableName string) (bool, error) {
	if schemaName != "main" && schemaName != "permitdb" {
		return false, fmt.Errorf("unsupported SQLite schema %q", schemaName)
	}
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM "+schemaName+".sqlite_master WHERE type = 'table' AND name = ?", tableName).Scan(&count)
	return count > 0, err
}

func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func vancouverProgressToday() string {
	loc, err := time.LoadLocation("America/Vancouver")
	if err != nil {
		loc = time.Local
	}
	return time.Now().In(loc).Format("2006-01-02")
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
