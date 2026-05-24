package scrapers

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

type ReportDownload struct{}

type reportLink struct {
	URL  string
	Text string
}

var anchorRE = regexp.MustCompile(`(?is)<a\s+[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)

func (ReportDownload) Scrape(ctx context.Context, client *fetcher.Client, source model.Source, opts Options) ([]model.PermitRecord, error) {
	target, err := requireEndpoint(source)
	if err != nil {
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, first(source.SkipReason, "report URL or endpoint is not configured"))
	}

	if meta, err := client.Head(ctx, target, source.Headers); err == nil {
		switch classifyReport(target, meta.ContentType, nil) {
		case "pdf":
			return nil, NewSkipError(StatusEndpointNeeded, source.ID, pdfUnsupportedReason("public PDF report is reachable", target, meta))
		case "excel":
			return nil, NewSkipError(StatusEndpointNeeded, source.ID, fmt.Sprintf("public spreadsheet report is reachable at %s; spreadsheet parser is not configured", target))
		}
	}

	body, contentType, err := client.Get(ctx, target, source.Headers)
	if err != nil {
		return nil, err
	}
	switch classifyReport(target, contentType, body) {
	case "csv":
		return parseCSVReport(source, target, contentType, body, opts.Limit)
	case "pdf":
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, pdfUnsupportedReason("public PDF report was downloaded", target, fetcher.Metadata{ContentType: contentType, ContentLength: int64(len(body))}))
	case "excel":
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, fmt.Sprintf("public spreadsheet report was downloaded from %s; spreadsheet parser is not configured", target))
	case "html":
		return scrapeReportIndex(ctx, client, source, target, string(body), opts)
	default:
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, fmt.Sprintf("report endpoint %s returned %q; add a parser or exact machine-readable CSV endpoint", target, contentType))
	}
}

func scrapeReportIndex(ctx context.Context, client *fetcher.Client, source model.Source, pageURL, htmlText string, opts Options) ([]model.PermitRecord, error) {
	links := discoverReportLinks(pageURL, htmlText)
	if len(links) == 0 {
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, fmt.Sprintf("report page is reachable at %s, but no CSV/PDF report links were discovered", pageURL))
	}
	if opts.MaxPages > 0 && len(links) > opts.MaxPages {
		links = links[:opts.MaxPages]
	}

	var out []model.PermitRecord
	var parsedCSV, pdfCount, spreadsheetCount int
	var firstPDF string
	for _, link := range links {
		kind := classifyReport(link.URL, "", nil)
		meta, headErr := client.Head(ctx, link.URL, source.Headers)
		if headErr == nil {
			kind = classifyReport(link.URL, meta.ContentType, nil)
		}
		switch kind {
		case "csv":
			body, contentType, err := client.Get(ctx, link.URL, source.Headers)
			if err != nil {
				return nil, err
			}
			recs, err := parseCSVReport(source, link.URL, contentType, body, remainingLimit(opts.Limit, len(out)))
			if err != nil {
				return nil, err
			}
			parsedCSV++
			out = append(out, recs...)
			if opts.Limit > 0 && len(out) >= opts.Limit {
				return out, nil
			}
		case "pdf":
			pdfCount++
			if firstPDF == "" {
				firstPDF = link.URL
			}
		case "excel":
			spreadsheetCount++
		}
	}
	if len(out) > 0 || parsedCSV > 0 {
		return out, nil
	}
	if pdfCount > 0 {
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, fmt.Sprintf("found %d public PDF report link(s), first %s; PDF parser is not configured", pdfCount, firstPDF))
	}
	if spreadsheetCount > 0 {
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, fmt.Sprintf("found %d public spreadsheet report link(s); spreadsheet parser is not configured", spreadsheetCount))
	}
	return nil, NewSkipError(StatusEndpointNeeded, source.ID, fmt.Sprintf("found %d report-like link(s) at %s, but none resolved to supported CSV reports", len(links), pageURL))
}

func parseCSVReport(source model.Source, reportURL, contentType string, body []byte, limit int) ([]model.PermitRecord, error) {
	reader := csv.NewReader(bytes.NewReader(body))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	reader.Comma = detectDelimiter(body)

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV report %s: %w", reportURL, err)
	}
	headerIndex := -1
	var headers []string
	for i, row := range rows {
		if !emptyCSVRow(row) {
			headerIndex = i
			headers = normalizeHeaders(row)
			break
		}
	}
	if headerIndex < 0 {
		return []model.PermitRecord{}, nil
	}

	out := []model.PermitRecord{}
	for i := headerIndex + 1; i < len(rows); i++ {
		row := rows[i]
		if emptyCSVRow(row) {
			continue
		}
		raw := map[string]string{
			"report_url":          reportURL,
			"report_content_type": contentType,
			"report_row":          strconv.Itoa(i + 1),
		}
		for c, v := range row {
			key := "col_" + strconv.Itoa(c+1)
			if c < len(headers) && headers[c] != "" {
				key = headers[c]
			}
			raw[key] = strings.TrimSpace(v)
		}
		rec := applyFieldMap(source, raw)
		if !hasPermitSignal(rec) {
			continue
		}
		out = append(out, rec)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func discoverReportLinks(pageURL, htmlText string) []reportLink {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []reportLink
	for _, m := range anchorRE.FindAllStringSubmatch(htmlText, -1) {
		href := strings.TrimSpace(html.UnescapeString(m[1]))
		if href == "" || strings.HasPrefix(strings.ToLower(href), "javascript:") || strings.HasPrefix(strings.ToLower(href), "mailto:") {
			continue
		}
		u, err := url.Parse(href)
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(u).String()
		text := cleanCell(m[2])
		if !looksLikeReportLink(resolved, text) || seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, reportLink{URL: resolved, Text: text})
	}
	return out
}

func looksLikeReportLink(rawURL, text string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	ext := strings.ToLower(path.Ext(p))
	if strings.Contains(p, "/media/file/") || strings.Contains(p, "/download") {
		return true
	}
	switch ext {
	case ".csv", ".tsv", ".txt", ".pdf", ".xls", ".xlsx":
		return true
	}
	label := strings.ToLower(text)
	return strings.Contains(label, "csv") || strings.Contains(label, "pdf") || strings.Contains(label, "spreadsheet")
}

func classifyReport(rawURL, contentType string, body []byte) string {
	ct := strings.ToLower(contentType)
	ext := strings.ToLower(path.Ext(pathOnly(rawURL)))
	switch {
	case strings.Contains(ct, "text/csv") || strings.Contains(ct, "application/csv") || ext == ".csv":
		return "csv"
	case strings.Contains(ct, "application/pdf") || ext == ".pdf" || strings.HasSuffix(strings.ToLower(pathOnly(rawURL)), "pdf"):
		return "pdf"
	case strings.Contains(ct, "spreadsheet") || strings.Contains(ct, "excel") || ext == ".xls" || ext == ".xlsx":
		return "excel"
	case strings.Contains(ct, "text/html") || bytes.Contains(bytes.ToLower(bytes.TrimSpace(body[:min(len(body), 512)])), []byte("<html")):
		return "html"
	case ext == ".tsv" || ext == ".txt":
		return "csv"
	default:
		return ""
	}
}

func pathOnly(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Path
}

func detectDelimiter(body []byte) rune {
	firstLine := string(body)
	if i := strings.IndexAny(firstLine, "\r\n"); i >= 0 {
		firstLine = firstLine[:i]
	}
	switch {
	case strings.Count(firstLine, "\t") > strings.Count(firstLine, ","):
		return '\t'
	case strings.Count(firstLine, ";") > strings.Count(firstLine, ","):
		return ';'
	default:
		return ','
	}
}

func normalizeHeaders(row []string) []string {
	headers := make([]string, len(row))
	for i, h := range row {
		h = strings.TrimPrefix(h, "\ufeff")
		headers[i] = strings.TrimSpace(h)
	}
	return headers
}

func emptyCSVRow(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func remainingLimit(limit, have int) int {
	if limit <= 0 {
		return 0
	}
	if have >= limit {
		return 1
	}
	return limit - have
}

func pdfUnsupportedReason(prefix, reportURL string, meta fetcher.Metadata) string {
	size := "unknown size"
	if meta.ContentLength >= 0 {
		size = fmt.Sprintf("%d bytes", meta.ContentLength)
	}
	ct := strings.TrimSpace(meta.ContentType)
	if ct == "" {
		ct = "unknown content type"
	}
	return fmt.Sprintf("%s at %s (%s, %s); PDF parser is not configured", prefix, reportURL, ct, size)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
