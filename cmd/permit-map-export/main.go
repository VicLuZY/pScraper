package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/example/bc-permit-scraper/internal/mapdata"
	"github.com/example/bc-permit-scraper/internal/model"
	webassets "github.com/example/bc-permit-scraper/web"
)

type exportData struct {
	GeneratedAt string               `json:"generated_at"`
	Records     []model.PermitRecord `json:"records"`
	Summary     mapdata.Summary      `json:"summary"`
	Audit       []model.ScrapeAudit  `json:"audit"`
}

type exportSummary struct {
	Out         string `json:"out"`
	DBPath      string `json:"db_path"`
	Records     int    `json:"records"`
	AuditRows   int    `json:"audit_rows"`
	GeneratedAt string `json:"generated_at"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	var dbDir, webDir, outDir string
	var auditLimit int
	fs := flag.NewFlagSet("permit-map-export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&dbDir, "db", "data/permits-db", "JSONL scraper database directory")
	fs.StringVar(&webDir, "web", "web", "web app source directory")
	fs.StringVar(&outDir, "out", "dist/permit-map", "static export output directory")
	fs.IntVar(&auditLimit, "audit-limit", 1000, "latest audit rows to embed; 0 embeds all rows")
	if err := fs.Parse(args); err != nil {
		return err
	}

	records, err := mapdata.LoadRecords(filepath.Join(dbDir, "current.jsonl"))
	if err != nil {
		return err
	}
	audit, err := mapdata.LoadAudit(filepath.Join(dbDir, "scrape_audit.jsonl"), auditLimit)
	if err != nil {
		return err
	}
	generatedAt := model.NowUTC()
	payload := exportData{
		GeneratedAt: generatedAt,
		Records:     records,
		Summary:     mapdata.Summarize(dbDir, records),
		Audit:       audit,
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	webFS := openWebFS(webDir)
	if err := writeStaticIndex(webFS, filepath.Join(outDir, "index.html")); err != nil {
		return err
	}
	for _, name := range []string{"app.js", "styles.css"} {
		if err := copyFile(webFS, name, filepath.Join(outDir, name)); err != nil {
			return err
		}
	}
	if err := writeDataJS(filepath.Join(outDir, "data.js"), payload); err != nil {
		return err
	}

	sum := exportSummary{
		Out:         outDir,
		DBPath:      dbDir,
		Records:     len(records),
		AuditRows:   len(audit),
		GeneratedAt: generatedAt,
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(sum)
}

func openWebFS(webDir string) fs.FS {
	if webDir != "" {
		if info, err := os.Stat(webDir); err == nil && info.IsDir() {
			return os.DirFS(webDir)
		}
	}
	return webassets.FS
}

func writeStaticIndex(webFS fs.FS, dst string) error {
	b, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		return err
	}
	html := string(b)
	html = strings.ReplaceAll(html, `href="/styles.css"`, `href="styles.css"`)
	html = strings.ReplaceAll(html, `src="/app.js"`, `src="app.js"`)
	if !strings.Contains(html, `src="data.js"`) {
		for _, appScript := range []string{
			`    <script src="app.js" defer></script>`,
			`<script src="app.js" defer></script>`,
		} {
			if strings.Contains(html, appScript) {
				dataScript := strings.Repeat(" ", leadingSpaces(appScript)) + `<script src="data.js"></script>` + "\n"
				html = strings.Replace(html, appScript, dataScript+appScript, 1)
				break
			}
		}
	}
	return os.WriteFile(dst, []byte(html), 0o644)
}

func leadingSpaces(s string) int {
	count := 0
	for _, r := range s {
		if r != ' ' {
			break
		}
		count++
	}
	return count
}

func writeDataJS(path string, payload exportData) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	content := "window.PERMIT_STATIC_DATA = " + string(b) + ";\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func copyFile(webFS fs.FS, name, dst string) error {
	b, err := fs.ReadFile(webFS, name)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
