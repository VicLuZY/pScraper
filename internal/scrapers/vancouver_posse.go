package scrapers

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "time/tzdata"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

const (
	vancouverDateSearchURL        = "https://plposweb.vancouver.ca/Public/Default.aspx?PossePresentation=PermitSearchByDate"
	vancouverDefaultWeeks         = 10
	vancouverServerResultLimit    = 1000
	vancouverCreatedFromColumn    = "972146"
	vancouverCreatedToColumn      = "984849"
	vancouverDefaultCurrentPane   = "1018439"
	vancouverDefaultResultPane    = "987904"
	vancouverDefaultFunctionDef   = "5"
	vancouverIndexFileName        = "vancouver_posse_index.jsonl"
	vancouverIndexDBFileName      = "vancouver_posse_index.sqlite"
	vancouverDetailSinkBatchSize  = 100
	vancouverDetailStatusScraping = "scraping"
	vancouverDetailStatusScraped  = "scraped"
	vancouverDetailStatusError    = "error"
)

type VancouverPOSSEDateSearch struct{}

func (VancouverPOSSEDateSearch) Scrape(ctx context.Context, client *fetcher.Client, source model.Source, opts Options) ([]model.PermitRecord, error) {
	sink := &collectingRecordSink{}
	err := (VancouverPOSSEDateSearch{}).ScrapeToSink(ctx, client, source, opts, sink)
	return sink.records, err
}

func (VancouverPOSSEDateSearch) ScrapeToSink(ctx context.Context, client *fetcher.Client, source model.Source, opts Options, sink RecordSink) error {
	base := first(source.Endpoint, source.URL, vancouverDateSearchURL)
	windows, rangeFrom, rangeTo, err := vancouverWindows(opts)
	if err != nil {
		return err
	}
	gate := &vancouverRequestGate{delay: client.MinDelay}
	index, err := openVancouverIndexStore(opts.DataDir)
	if err != nil {
		return err
	}
	defer index.Close()

	discoveredIDs := map[string]bool{}
	if !opts.DetailOnly {
		discovered, err := discoverVancouverIndex(ctx, client, source, base, windows, opts.IndexWorkers, gate)
		if err != nil {
			return err
		}
		now := model.NowUTC()
		for _, entry := range discovered {
			discoveredIDs[entry.ObjectID] = true
		}
		if err := index.UpsertEntries(discovered, now); err != nil {
			return err
		}
	}
	if opts.IndexOnly {
		return nil
	}
	return scrapeVancouverDetailsFromIndexToSink(ctx, client, source, base, index, rangeFrom, rangeTo, discoveredIDs, opts.DetailOnly, opts.DetailStatus, opts.Limit, opts.DetailWorkers, gate, sink)
}

type collectingRecordSink struct {
	records []model.PermitRecord
}

func (s *collectingRecordSink) PutRecords(records []model.PermitRecord) (BatchCounts, error) {
	s.records = append(s.records, records...)
	return BatchCounts{RecordsSeen: len(records)}, nil
}

type vancouverDateWindow struct {
	From time.Time
	To   time.Time
}

func vancouverWindows(opts Options) ([]vancouverDateWindow, string, string, error) {
	if strings.TrimSpace(opts.FromDate) != "" || strings.TrimSpace(opts.ToDate) != "" {
		from, err := parseISODate(opts.FromDate)
		if err != nil {
			return nil, "", "", fmt.Errorf("--from: %w", err)
		}
		to, err := parseISODate(first(opts.ToDate, vancouverToday().Format("2006-01-02")))
		if err != nil {
			return nil, "", "", fmt.Errorf("--to: %w", err)
		}
		if to.Before(from) {
			return nil, "", "", fmt.Errorf("--to must be on or after --from")
		}
		windows := []vancouverDateWindow{}
		for start := from; !start.After(to); start = start.AddDate(0, 0, 7) {
			end := start.AddDate(0, 0, 6)
			if end.After(to) {
				end = to
			}
			windows = append(windows, vancouverDateWindow{From: start, To: end})
		}
		return windows, from.Format("2006-01-02"), to.Format("2006-01-02"), nil
	}
	weeks := opts.MaxPages
	if weeks <= 0 {
		weeks = vancouverDefaultWeeks
	}
	today := vancouverToday()
	windows := make([]vancouverDateWindow, 0, weeks)
	for i := 0; i < weeks; i++ {
		to := today.AddDate(0, 0, -7*i)
		from := to.AddDate(0, 0, -6)
		windows = append(windows, vancouverDateWindow{From: from, To: to})
	}
	oldest := windows[len(windows)-1].From.Format("2006-01-02")
	newest := windows[0].To.Format("2006-01-02")
	return windows, oldest, newest, nil
}

func parseISODate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("date is required")
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected YYYY-MM-DD: %w", err)
	}
	return t, nil
}

type vancouverIndexEntry struct {
	ObjectID           string            `json:"object_id"`
	DetailURL          string            `json:"detail_url"`
	PermitNumber       string            `json:"permit_number,omitempty"`
	PermitType         string            `json:"permit_type,omitempty"`
	Status             string            `json:"status,omitempty"`
	Address            string            `json:"address,omitempty"`
	CreatedDate        string            `json:"created_date,omitempty"`
	IssueDate          string            `json:"issue_date,omitempty"`
	CompletedDate      string            `json:"completed_date,omitempty"`
	SearchWindowFrom   string            `json:"search_window_from,omitempty"`
	SearchWindowTo     string            `json:"search_window_to,omitempty"`
	SearchWindowCount  int               `json:"search_window_count,omitempty"`
	SearchWindowCapped bool              `json:"search_window_capped,omitempty"`
	SearchSplitDepth   int               `json:"search_split_depth,omitempty"`
	DetailStatus       string            `json:"detail_status,omitempty"`
	DetailStartedAt    string            `json:"detail_started_at,omitempty"`
	DetailFinishedAt   string            `json:"detail_finished_at,omitempty"`
	DetailError        string            `json:"detail_error,omitempty"`
	FirstIndexedAt     string            `json:"first_indexed_at,omitempty"`
	LastIndexedAt      string            `json:"last_indexed_at,omitempty"`
	Raw                map[string]string `json:"raw,omitempty"`
}

func discoverVancouverIndex(ctx context.Context, client *fetcher.Client, source model.Source, base string, windows []vancouverDateWindow, workers int, gate *vancouverRequestGate) ([]vancouverIndexEntry, error) {
	if workers < 1 {
		workers = 1
	}
	if workers > len(windows) {
		workers = len(windows)
	}
	if workers < 1 {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan vancouverDateWindow)
	results := make(chan []vancouverIndexEntry, len(windows))
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	setErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}
	getErr := func() error {
		errMu.Lock()
		defer errMu.Unlock()
		return firstErr
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for window := range jobs {
				entries, err := scrapeVancouverDateWindowIndexAdaptive(ctx, client, source, base, window, gate, 0)
				if err != nil {
					setErr(err)
					return
				}
				results <- entries
			}
		}()
	}
	for _, window := range windows {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			if err := getErr(); err != nil {
				return nil, err
			}
			return nil, ctx.Err()
		case jobs <- window:
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	if err := getErr(); err != nil {
		return nil, err
	}
	out := []vancouverIndexEntry{}
	for entries := range results {
		out = append(out, entries...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedDate == out[j].CreatedDate {
			return out[i].ObjectID < out[j].ObjectID
		}
		return out[i].CreatedDate < out[j].CreatedDate
	})
	return out, nil
}

func scrapeVancouverDateWindowIndexAdaptive(ctx context.Context, client *fetcher.Client, source model.Source, base string, window vancouverDateWindow, gate *vancouverRequestGate, splitDepth int) ([]vancouverIndexEntry, error) {
	entries, err := scrapeVancouverDateWindowIndex(ctx, client, source, base, window, gate, splitDepth)
	if err != nil {
		return nil, err
	}
	if len(entries) < vancouverServerResultLimit || !window.canSplit() {
		return entries, nil
	}
	left, right := window.split()
	leftEntries, err := scrapeVancouverDateWindowIndexAdaptive(ctx, client, source, base, left, gate, splitDepth+1)
	if err != nil {
		return nil, err
	}
	rightEntries, err := scrapeVancouverDateWindowIndexAdaptive(ctx, client, source, base, right, gate, splitDepth+1)
	if err != nil {
		return nil, err
	}
	return append(leftEntries, rightEntries...), nil
}

func (w vancouverDateWindow) canSplit() bool {
	return w.To.After(w.From)
}

func (w vancouverDateWindow) split() (vancouverDateWindow, vancouverDateWindow) {
	days := int(w.To.Sub(w.From).Hours()/24) + 1
	leftDays := days / 2
	if leftDays < 1 {
		leftDays = 1
	}
	leftTo := w.From.AddDate(0, 0, leftDays-1)
	rightFrom := leftTo.AddDate(0, 0, 1)
	return vancouverDateWindow{From: w.From, To: leftTo}, vancouverDateWindow{From: rightFrom, To: w.To}
}

func scrapeVancouverDateWindowIndex(ctx context.Context, client *fetcher.Client, source model.Source, base string, window vancouverDateWindow, gate *vancouverRequestGate, splitDepth int) ([]vancouverIndexEntry, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: client.HTTP.Timeout, Jar: jar}
	if err := gate.wait(ctx); err != nil {
		return nil, err
	}
	body, err := doVancouverRequest(ctx, httpClient, client.UserAgent, http.MethodGet, base, nil, source.Headers, "")
	if err != nil {
		return nil, err
	}
	form, err := parseVancouverSearchForm(string(body))
	if err != nil {
		return nil, err
	}
	fromDate := window.From.Format("2006-01-02")
	toDate := window.To.Format("2006-01-02")
	changes := fmt.Sprintf("%s,('C','S0',%s,'%s 00:00:00'),('C','S0',%s,'%s 00:00:00'),('F','CreatedDateFrom_1018439_S0',0,0)",
		form.DataChanges,
		vancouverCreatedFromColumn,
		fromDate,
		vancouverCreatedToColumn,
		toDate,
	)
	values := url.Values{
		"currentpaneid":   {form.CurrentPaneID},
		"paneid":          {form.ResultPaneID},
		"functiondef":     {form.FunctionDef},
		"sortcolumns":     {form.SortColumns},
		"datachanges":     {changes},
		"comesfrom":       {"posse"},
		"changesxml":      {""},
		"changespending":  {"T"},
		"changesonobject": {""},
	}
	if err := gate.wait(ctx); err != nil {
		return nil, err
	}
	result, err := doVancouverRequest(ctx, httpClient, client.UserAgent, http.MethodPost, base, strings.NewReader(values.Encode()), source.Headers, "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	recs := parseVancouverRows(source, string(result), fromDate, toDate)
	entries := make([]vancouverIndexEntry, 0, len(recs))
	for _, rec := range recs {
		entry, err := vancouverIndexEntryFromRecord(base, rec)
		if err != nil {
			return nil, err
		}
		entry.SearchWindowCount = len(recs)
		entry.SearchWindowCapped = len(recs) >= vancouverServerResultLimit
		entry.SearchSplitDepth = splitDepth
		entry.Raw["Search Window Count"] = fmt.Sprint(len(recs))
		entry.Raw["Search Window Capped"] = fmt.Sprint(entry.SearchWindowCapped)
		entry.Raw["Search Split Depth"] = fmt.Sprint(splitDepth)
		entries = append(entries, entry)
	}
	return entries, nil
}

func doVancouverRequest(ctx context.Context, httpClient *http.Client, userAgent, method, rawURL string, body io.Reader, headers map[string]string, contentType string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Origin", "https://plposweb.vancouver.ca")
		req.Header.Set("Referer", rawURL)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(b))
		if len(msg) > 512 {
			msg = msg[:512]
		}
		return nil, fmt.Errorf("%s %s returned %s: %s", method, rawURL, resp.Status, msg)
	}
	return b, readErr
}

type vancouverSearchForm struct {
	CurrentPaneID string
	ResultPaneID  string
	FunctionDef   string
	SortColumns   string
	DataChanges   string
}

var posseSubmitLinkRE = regexp.MustCompile(`(?is)PosseSubmitLink\([^,]+,\s*(\d+),\s*(\d+)`)

func parseVancouverSearchForm(page string) (vancouverSearchForm, error) {
	form := vancouverSearchForm{
		CurrentPaneID: first(inputValue(page, "currentpaneid"), vancouverDefaultCurrentPane),
		ResultPaneID:  vancouverDefaultResultPane,
		FunctionDef:   vancouverDefaultFunctionDef,
		SortColumns:   first(inputValue(page, "sortcolumns"), "{}"),
		DataChanges:   inputValue(page, "datachanges"),
	}
	if matches := posseSubmitLinkRE.FindStringSubmatch(page); len(matches) == 3 {
		form.FunctionDef = matches[1]
		form.ResultPaneID = matches[2]
	}
	if form.DataChanges == "" {
		return form, fmt.Errorf("Vancouver POSSE search form did not include datachanges token")
	}
	return form, nil
}

func inputValue(page, name string) string {
	tag := inputTag(page, name)
	if tag == "" {
		return ""
	}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?is)\bvalue\s*=\s*"([^"]*)"`),
		regexp.MustCompile(`(?is)\bvalue\s*=\s*'([^']*)'`),
		regexp.MustCompile(`(?is)\bvalue\s*=\s*([^\s>]+)`),
	} {
		if matches := re.FindStringSubmatch(tag); len(matches) == 2 {
			return html.UnescapeString(matches[1])
		}
	}
	return ""
}

func inputTag(page, name string) string {
	for _, tag := range regexp.MustCompile(`(?is)<input\b[^>]*>`).FindAllString(page, -1) {
		if attrEquals(tag, "id", name) || attrEquals(tag, "name", name) {
			return tag
		}
	}
	return ""
}

func attrEquals(tag, attr, want string) bool {
	re := regexp.MustCompile(`(?is)\b` + regexp.QuoteMeta(attr) + `\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
	matches := re.FindStringSubmatch(tag)
	if len(matches) == 0 {
		return false
	}
	for _, value := range matches[2:] {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

func parseVancouverRows(source model.Source, page, fromDate, toDate string) []model.PermitRecord {
	out := []model.PermitRecord{}
	rows := rowRE.FindAllStringSubmatch(page, -1)
	for _, row := range rows {
		cells := cellRE.FindAllStringSubmatch(row[1], -1)
		if len(cells) < 8 {
			continue
		}
		vals := make([]string, 0, len(cells))
		for _, cell := range cells {
			vals = append(vals, cleanCell(cell[1]))
		}
		if strings.EqualFold(vals[1], "Type") || strings.TrimSpace(vals[2]) == "" {
			continue
		}
		raw := map[string]string{
			"Type":                 vals[1],
			"Number":               vals[2],
			"Location":             vals[3],
			"Status":               vals[4],
			"Created Date":         vals[5],
			"Issue Date":           vals[6],
			"Completed Date":       vals[7],
			"Search Window From":   fromDate,
			"Search Window To":     toDate,
			"Server Result Limit":  fmt.Sprint(vancouverServerResultLimit),
			"Vancouver Object URL": extractHref(cells[0][1]),
		}
		rec := applyFieldMap(source, raw)
		if rec.URL == source.URL {
			rec.URL = raw["Vancouver Object URL"]
		}
		if !hasPermitSignal(rec) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func vancouverIndexEntryFromRecord(base string, rec model.PermitRecord) (vancouverIndexEntry, error) {
	detailURL, err := resolveVancouverURL(base, rec.URL)
	if err != nil {
		return vancouverIndexEntry{}, err
	}
	objectID := vancouverObjectID(detailURL)
	if objectID == "" {
		return vancouverIndexEntry{}, fmt.Errorf("missing PosseObjectId in detail URL %s", detailURL)
	}
	return vancouverIndexEntry{
		ObjectID:         objectID,
		DetailURL:        detailURL,
		PermitNumber:     rec.PermitNumber,
		PermitType:       rec.PermitType,
		Status:           rec.Status,
		Address:          rec.Address,
		CreatedDate:      rec.AppliedDate,
		IssueDate:        rec.IssuedDate,
		CompletedDate:    rec.CompletedDate,
		SearchWindowFrom: rec.Raw["Search Window From"],
		SearchWindowTo:   rec.Raw["Search Window To"],
		Raw:              rec.Raw,
	}, nil
}

func vancouverObjectID(detailURL string) string {
	u, err := url.Parse(detailURL)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("PosseObjectId"))
}

func dateInRange(date, fromDate, toDate string) bool {
	date = strings.TrimSpace(date)
	if date == "" {
		return true
	}
	return (fromDate == "" || date >= fromDate) && (toDate == "" || date <= toDate)
}

func scrapeVancouverDetailsFromIndex(ctx context.Context, client *fetcher.Client, source model.Source, base string, entries []vancouverIndexEntry, workers int, gate *vancouverRequestGate) ([]model.PermitRecord, error) {
	if workers < 1 {
		workers = 1
	}
	if workers > len(entries) {
		workers = len(entries)
	}
	if workers < 1 {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan vancouverIndexEntry)
	results := make(chan model.PermitRecord, len(entries))
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	setErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}
	getErr := func() error {
		errMu.Lock()
		defer errMu.Unlock()
		return firstErr
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			httpClient := &http.Client{Timeout: client.HTTP.Timeout}
			for entry := range jobs {
				rec, err := scrapeVancouverDetailEntry(ctx, httpClient, client, source, base, entry, gate)
				if err != nil {
					setErr(err)
					return
				}
				results <- rec
			}
		}()
	}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			if err := getErr(); err != nil {
				return nil, err
			}
			return nil, ctx.Err()
		case jobs <- entry:
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	if err := getErr(); err != nil {
		return nil, err
	}
	out := make([]model.PermitRecord, 0, len(results))
	for rec := range results {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppliedDate == out[j].AppliedDate {
			return out[i].PermitNumber < out[j].PermitNumber
		}
		return out[i].AppliedDate < out[j].AppliedDate
	})
	return out, nil
}

func scrapeVancouverDetailsFromIndexToSink(ctx context.Context, client *fetcher.Client, source model.Source, base string, index *vancouverIndexStore, fromDate, toDate string, discoveredIDs map[string]bool, detailOnly bool, detailStatus string, limit int, workers int, gate *vancouverRequestGate, sink RecordSink) error {
	if sink == nil {
		return fmt.Errorf("record sink is required")
	}
	if workers < 1 {
		workers = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := index.ResetDetailStatus(vancouverDetailStatusScraping); err != nil {
		return err
	}

	jobs := make(chan vancouverIndexEntry, workers*2)
	results := make(chan model.PermitRecord, workers*2)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	setErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}
	getErr := func() error {
		errMu.Lock()
		defer errMu.Unlock()
		return firstErr
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			httpClient := &http.Client{Timeout: client.HTTP.Timeout}
			for entry := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := index.MarkDetailStatus(entry.ObjectID, vancouverDetailStatusScraping, "", model.NowUTC()); err != nil {
					setErr(err)
					return
				}
				rec, err := scrapeVancouverDetailEntry(ctx, httpClient, client, source, base, entry, gate)
				if err != nil {
					if markErr := index.MarkDetailStatus(entry.ObjectID, vancouverDetailStatusError, err.Error(), model.NowUTC()); markErr != nil {
						setErr(markErr)
						return
					}
					continue
				}
				select {
				case results <- rec:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	go func() {
		defer close(jobs)
		err := index.ForEachEntry(ctx, fromDate, toDate, discoveredIDs, detailOnly, detailStatus, limit, func(entry vancouverIndexEntry) error {
			select {
			case jobs <- entry:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err != nil && getErr() == nil {
			setErr(err)
		}
	}()

	batch := make([]model.PermitRecord, 0, vancouverDetailSinkBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		records := append([]model.PermitRecord(nil), batch...)
		objectIDs := vancouverRecordObjectIDs(records)
		_, err := sink.PutRecords(records)
		batch = batch[:0]
		if err != nil {
			_ = index.MarkDetailStatusBatch(objectIDs, vancouverDetailStatusError, err.Error(), model.NowUTC())
			setErr(err)
			return err
		}
		if err := index.MarkDetailStatusBatch(objectIDs, vancouverDetailStatusScraped, "", model.NowUTC()); err != nil {
			setErr(err)
			return err
		}
		return nil
	}
	var sinkErr error
	for rec := range results {
		if sinkErr != nil {
			continue
		}
		batch = append(batch, rec)
		if len(batch) >= vancouverDetailSinkBatchSize {
			if err := flush(); err != nil {
				sinkErr = err
			}
		}
	}
	if sinkErr != nil {
		return sinkErr
	}
	if err := flush(); err != nil {
		return err
	}
	return getErr()
}

func vancouverRecordObjectIDs(records []model.PermitRecord) []string {
	ids := make([]string, 0, len(records))
	for _, rec := range records {
		if rec.Raw == nil {
			continue
		}
		if objectID := strings.TrimSpace(rec.Raw["Object ID"]); objectID != "" {
			ids = append(ids, objectID)
		}
	}
	return ids
}

func scrapeVancouverDetailEntry(ctx context.Context, httpClient *http.Client, client *fetcher.Client, source model.Source, base string, entry vancouverIndexEntry, gate *vancouverRequestGate) (model.PermitRecord, error) {
	rec := vancouverRecordFromIndexEntry(source, entry)
	detailURL, err := resolveVancouverURL(base, entry.DetailURL)
	if err != nil {
		return rec, fmt.Errorf("Vancouver record %s detail URL: %w", entry.PermitNumber, err)
	}
	if err := gate.wait(ctx); err != nil {
		return rec, err
	}
	body, err := doVancouverRequest(ctx, httpClient, client.UserAgent, http.MethodGet, detailURL, nil, source.Headers, "")
	if err != nil {
		return rec, fmt.Errorf("fetch Vancouver detail %s %s: %w", entry.PermitNumber, detailURL, err)
	}
	mergeVancouverDetail(&rec, detailURL, string(body))
	return rec, nil
}

func vancouverRecordFromIndexEntry(source model.Source, entry vancouverIndexEntry) model.PermitRecord {
	raw := map[string]string{}
	for k, v := range entry.Raw {
		raw[k] = v
	}
	raw["Object ID"] = entry.ObjectID
	raw["Vancouver Object URL"] = entry.DetailURL
	raw["Search Window From"] = first(raw["Search Window From"], entry.SearchWindowFrom)
	raw["Search Window To"] = first(raw["Search Window To"], entry.SearchWindowTo)
	if entry.SearchWindowCount > 0 {
		raw["Search Window Count"] = fmt.Sprint(entry.SearchWindowCount)
	}
	raw["Search Window Capped"] = fmt.Sprint(entry.SearchWindowCapped)
	raw["Search Split Depth"] = fmt.Sprint(entry.SearchSplitDepth)
	raw["Type"] = first(raw["Type"], entry.PermitType)
	raw["Number"] = first(raw["Number"], entry.PermitNumber)
	raw["Location"] = first(raw["Location"], entry.Address)
	raw["Status"] = first(raw["Status"], entry.Status)
	raw["Created Date"] = first(raw["Created Date"], entry.CreatedDate)
	raw["Issue Date"] = first(raw["Issue Date"], entry.IssueDate)
	raw["Completed Date"] = first(raw["Completed Date"], entry.CompletedDate)
	return applyFieldMap(source, raw)
}

func resolveVancouverURL(base, href string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		return u.String(), nil
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(u).String(), nil
}

type vancouverRequestGate struct {
	delay time.Duration
	mu    sync.Mutex
	last  time.Time
}

func (g *vancouverRequestGate) wait(ctx context.Context) error {
	if g == nil || g.delay <= 0 {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	wait := g.delay - time.Since(g.last)
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	g.last = time.Now()
	return nil
}

func mergeVancouverDetail(rec *model.PermitRecord, detailURL, page string) {
	if rec.Raw == nil {
		rec.Raw = map[string]string{}
	}
	rec.URL = detailURL
	rec.Raw["Detail URL"] = detailURL
	rec.Raw["Detail Page Title"] = parseHTMLTitle(page)
	rec.Raw["Detail Page Text"] = cleanDetailPageText(page)
	for k, v := range parseVancouverDetailSpans(page) {
		rec.Raw[k] = v
	}
	rec.Address = first(rawFirst(rec.Raw, "Detail PermitLocation", "Detail Address"), rec.Address)
	rec.Description = first(rawFirst(rec.Raw, "Detail WorkDescription"), rec.Description)
	rec.AppliedDate = normalizeDate(first(rawFirst(rec.Raw, "Detail ApplicationDate"), rec.AppliedDate))
	rec.IssuedDate = normalizeDate(first(rawFirst(rec.Raw, "Detail IssueDate"), rec.IssuedDate))
	rec.CompletedDate = normalizeDate(first(rawFirst(rec.Raw, "Detail CompletedDate"), rec.CompletedDate))
}

func parseHTMLTitle(page string) string {
	re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	if matches := re.FindStringSubmatch(page); len(matches) == 2 {
		return cleanCell(matches[1])
	}
	return ""
}

func cleanDetailPageText(page string) string {
	re := regexp.MustCompile(`(?is)<body\b[^>]*>(.*?)</body>`)
	if matches := re.FindStringSubmatch(page); len(matches) == 2 {
		return cleanCell(matches[1])
	}
	return cleanCell(page)
}

var spanRE = regexp.MustCompile(`(?is)<span\b[^>]*\bid\s*=\s*"([^"]+)"[^>]*>(.*?)</span>`)

func parseVancouverDetailSpans(page string) map[string]string {
	out := map[string]string{}
	for _, matches := range spanRE.FindAllStringSubmatch(page, -1) {
		id := strings.TrimSpace(strings.TrimSuffix(matches[1], "_sp"))
		value := cleanCell(matches[2])
		if id == "" || value == "" || strings.HasSuffix(value, ":") {
			continue
		}
		field := vancouverSpanField(id)
		if field == "" {
			continue
		}
		appendRawValue(out, "Detail "+field, value)
	}
	return out
}

func vancouverSpanField(id string) string {
	field := id
	if idx := strings.Index(field, "_"); idx > 0 {
		field = field[:idx]
	}
	field = strings.TrimSpace(field)
	if field == "" || strings.HasPrefix(field, "Text") || strings.HasPrefix(field, "Link") || strings.HasPrefix(field, "Instr") {
		return ""
	}
	if strings.HasSuffix(field, "Label") {
		return ""
	}
	return field
}

func appendRawValue(raw map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if existing := strings.TrimSpace(raw[key]); existing != "" {
		for _, part := range strings.Split(existing, " | ") {
			if part == value {
				return
			}
		}
		raw[key] = existing + " | " + value
		return
	}
	raw[key] = value
}

func extractHref(cellHTML string) string {
	re := regexp.MustCompile(`(?is)\bhref\s*=\s*"([^"]+)"`)
	if matches := re.FindStringSubmatch(cellHTML); len(matches) == 2 {
		return html.UnescapeString(matches[1])
	}
	re = regexp.MustCompile(`(?is)\bhref\s*=\s*'([^']+)'`)
	if matches := re.FindStringSubmatch(cellHTML); len(matches) == 2 {
		return html.UnescapeString(matches[1])
	}
	return ""
}

func vancouverToday() time.Time {
	loc, err := time.LoadLocation("America/Vancouver")
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}
