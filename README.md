# BC Permit Scraper Starter

A Go starter package for collecting openly downloadable permit records from British Columbia public permit sources, deduplicating them, and maintaining current status plus status-change history.

The scraper collects permit information. The map is a presentation layer over the scraped permit database; it is not a generic GIS-layer ingestion tool.

This is intentionally conservative:

- It downloads only open permit datasets, permit-record APIs, static public permit tables, and other sources that publish permit records without login or secret search input.
- It records skips for applicant-only, login-only, access-code, or search-input-only portals.
- It does not bypass CAPTCHA, logins, robots controls, access-code gates, or session restrictions.
- It retains raw source fields so field maps can be tuned as portals change.

## Quick start

```bash
go test ./...
go run ./cmd/permit-scraper --sources configs/sources.json --db data/permits-db --all --limit 25 --max-pages 1 --parallel 4
```

The first run creates:

```text
data/permits-db/current.jsonl       # latest deduped current state
data/permits-db/history.jsonl       # insert/update history
data/permits-db/scrape_audit.jsonl  # per-source audit log
data/permits-db/scrape_progress.json # latest per-source progress snapshot
```

`--parallel` controls source-level concurrency. The default is `1`; use a small value such as `4` for faster runs while still keeping database writes serialized.

For a safe smoke run over every configured source row, including intentional skips:

```bash
go run ./cmd/permit-scraper --sources configs/sources.json --db data/permits-db --try-all --limit 10 --max-pages 1 --parallel 4
```

Start the interactive permit map after a scrape has produced `data/permits-db/current.jsonl`:

```bash
go run ./cmd/permit-map --db data/permits-db --web web --addr 127.0.0.1:8080
```

Open `http://127.0.0.1:8080/`. The viewer serves the current JSONL database through local API endpoints, renders valid latitude/longitude records on a Leaflet map, and keeps unmapped records available in the results sidebar.

Run the native Ubuntu GTK GUI:

```bash
scripts/build-native-app.sh
scripts/launch-native-gui.sh
```

The native app does not use Electron or a localhost browser shell. It reads `data/permits-db/vancouver_posse_index.sqlite` and `data/permits-db/permits.sqlite` directly, draws the Vancouver POSSE dot-matrix progress view, and acts as the command centre for this source: link a database folder, refresh counts, discover or refresh POSSE IDs, start a detail scrape, pause it, resume pending/error IDs, retry errors, reset stuck yellow rows, clear errors, open the data folder, and tune date range, workers, delay, timeout, limit, and detail status scope.

Build a local launchable app directory:

```bash
make native
```

Install a user-level Ubuntu application launcher:

```bash
scripts/install-native-desktop.sh
```

This writes `~/.local/share/applications/pscraper-native.desktop`, pointing at `dist/native/pScraper-native`.

Export the same viewer as static files when you do not want a local server:

```bash
go run ./cmd/permit-map-export --db data/permits-db --web web --out dist/permit-map
```

Open `dist/permit-map/index.html` directly or publish the `dist/permit-map` folder to any static host. The export embeds records, summary metrics, and audit rows in `data.js`, so it does not call `/api/*` or require localhost. Map tiles and Leaflet/Lucide assets are still loaded from their public CDNs unless you vendor those assets.

Build a portable Windows package:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/package-windows.ps1
```

The package is written as a single file: `dist/pScraper.exe`. No config folder, data folder, launcher scripts, or zip file are required. `pScraper.exe` embeds the default source config, the current permit database snapshot, and the map UI. When a scrape is run, the embedded snapshot is loaded as the seed and the updated OS-agnostic JSONL database is saved separately in `data/permits-db` unless another `--db` path is supplied.

Portable direct commands:

```cmd
pScraper.exe scrape --all --limit 25 --max-pages 1 --parallel 4
pScraper.exe map --addr 127.0.0.1:8080
pScraper.exe export-map --out map-export
pScraper.exe db import-jsonl --sqlite data\permits.sqlite --reset
```

If `configs/sources.json` or `data/permits-db` exist beside the executable or in the app runtime directory, those external files take precedence over the embedded defaults.

Use SQLite for relational storage after the JSONL path is working:

```bash
go run ./cmd/permit-scraper db import-jsonl --jsonl data/permits-db --sqlite data/permits.sqlite --reset
go run ./cmd/permit-scraper --sources configs/sources.json --store sqlite --db data/permits.sqlite --all --limit 25 --max-pages 1 --parallel 4
```

## Source configuration

Edit `configs/sources.json`.

Important fields:

| Field | Meaning |
|---|---|
| `kind` | Scraper type: `opendatasoft_v2`, `arcgis_feature_service`, `html_table`, `nanaimo_whatsbuilding`, `vancouver_posse_date_search`, `report_download`, `report_download_needed`, `public_search_needs_input`, `applicant_login`, `application_hub`, or `authority_reference`. |
| `enabled` | Included by default runs. |
| `download_all` | Whether a bulk download is appropriate for this source. |
| `openly_searchable` | Whether the source exposes public records without applicant credentials. |
| `needs_input` | Whether a permit number/address/date/account is required. |
| `endpoint` | Permit-record API endpoint. If a municipality publishes permit records through ArcGIS, this must be the actual permit layer `.../FeatureServer/<layer>/query` URL. |
| `field_map` | Maps canonical fields to source field names. Use `|` for fallbacks, e.g. `PermitNumber|permit_number|Permit No`. |

## Current included source rows

The configuration currently contains 76 source rows. Normal `--all` runs include the 23 enabled open/public rows; `--try-all` audits all rows and records why each skipped source was not bulk-scraped.

Enabled machine-readable sources include:

- OpenDataSoft: Vancouver issued building permits.
- Permit-record APIs exposed through ArcGIS/FeatureServer: Kelowna, Maple Ridge, New Westminster, Port Moody, Columbia Shuswap Regional District, Coquitlam, Victoria permits and development applications, and BC Energy Regulator well surface hole permits.
- Public indexes and static HTML/table candidates that are safe to audit: Nanaimo What's Building, Township of Langley, North Saanich, Saanich, Richmond, City of Langley, Chilliwack, Regional District of Nanaimo, Regional District of Central Kootenay, and Regional District of Okanagan-Similkameen.

Targeted jurisdiction scrapers can be run with `--source`; for example, `vancouver_posse_date_search` uses the City of Vancouver public POSSE date search in one-week windows, with `--max-pages` controlling how many weekly windows are searched backward from today. Each Vancouver result is followed to its public detail page, and the cleaned page text plus structured detail fields are retained in `raw`.

For large targeted Vancouver backfills, use explicit dates and bounded workers:

```bash
go run ./cmd/permit-scraper --sources configs/sources.json --db data/permits-db --source vancouver_public_permit_search --from 2025-01-01 --to 2026-05-25 --index-workers 4 --detail-workers 4 --delay-ms 500
```

The Vancouver date-search stage saves discovered object IDs and search-result row fields to the compact SQLite index `data/permits-db/vancouver_posse_index.sqlite`. If the legacy JSONL index exists and SQLite is empty, it is migrated automatically. If a search window returns the POSSE cap of 1000 rows, it is recursively split into smaller date windows before saving. Use `--index-only` to refresh only that index, or `--detail-only` to populate records from an existing index.

For the overnight Vancouver detail scrape, keep the permit DB in the same directory as the POSSE index and use detail workers:

```bash
go run ./cmd/permit-scraper --sources configs/sources.json --store sqlite --db data/permits-db/permits.sqlite --source vancouver_public_permit_search --from 2000-01-01 --to 2026-05-25 --detail-only --detail-status pending,error --detail-workers 8 --delay-ms 200 --timeout 60
```

The Vancouver detail path streams worker results into storage in batches, so records are saved throughout the run instead of waiting for every detail page to finish. `--detail-status` can be `all`, `pending`, `error`, `scraping`, `scraped`, or comma-separated values such as `pending,error`; the native GUI uses that to pause and resume without re-scraping completed IDs.

The native desktop app exposes the full Vancouver workflow. Use `Discover IDs` for the POSSE date-search index, `Start Details` for the selected detail scope, `Resume Details` for pending/error rows, `Retry Errors` for red rows, and `Pause` to stop the current child process while preserving saved progress. The panel reads `vancouver_posse_index.sqlite` and `permits.sqlite` to show completed detail records, remaining IDs, PID, current rate, and estimated time remaining. The Vancouver matrix is linked to those same database files and draws every indexed permit as a dot: gray for not processed, green for scraped, yellow for currently scraping, and red for errors. Its timeline runs from the earliest indexed Vancouver permit date through the app's current local date.

The remaining configured rows are deliberately classified as `endpoint_needed`, `requires_search_input`, `login_or_authorized_only`, or `not_public_bulk` when they are not ready or appropriate for open bulk collection. CSV/TSV report downloads can use `report_download`; PDF-only report sources remain auditable as `report_download_needed` until a reliable parser is configured.

## Dedupe strategy

The dedupe key is deterministic and jurisdiction-aware:

1. Prefer permit number.
2. Fall back to application ID.
3. Fall back to PID or roll number plus permit type/date.
4. Fall back to normalized address plus permit type/date/description.
5. Last resort: source ID plus record description hash.

The content hash is independent from the dedupe key. If the same dedupe key appears with a changed status or changed canonical content, the database updates `current.jsonl` and appends an event to `history.jsonl`.

## Database note

The default database remains a file-backed JSONL store because it is simple to inspect and works without a service. The optional SQLite backend uses `modernc.org/sqlite`, preserves the same upsert/history semantics as JSONL, and can be selected with `--store sqlite --db data/permits.sqlite`. `cmd/permit-db import-jsonl` migrates existing JSONL current/history/audit files into SQLite.

## Recommended production workflow

1. Run `--try-all --limit 10 --max-pages 1` to build a skip/error audit.
2. For each source landing page, confirm that the endpoint publishes permit records, then paste the exact API URL into `configs/sources.json`.
3. Add dedicated source scrapers only where an official public permit-record API or index exists.
4. Keep applicant-login portals out of bulk runs unless an authorised export/API exists.
5. Schedule low-frequency runs and keep the `scrape_audit.jsonl` file.
6. Review source terms, robots files, and privacy constraints before production use.

## Example Permit API Endpoint

Some municipalities publish permit records through ArcGIS FeatureServer APIs. Use those only when the layer itself is a permit-record dataset:

```text
https://services.arcgis.com/<org>/arcgis/rest/services/<layer>/FeatureServer/0/query
```

Once configured, enable it:

```json
{
  "kind": "arcgis_feature_service",
  "endpoint": "https://services.arcgis.com/.../FeatureServer/0/query",
  "enabled": true
}
```

## Extending

Add a scraper by implementing:

```go
type Scraper interface {
    Scrape(ctx context.Context, client *fetcher.Client, source model.Source, opts Options) ([]model.PermitRecord, error)
}
```

Then register it in `internal/scrapers/registry.go`.
