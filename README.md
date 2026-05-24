# BC Permit Scraper Starter

A Go starter package for collecting openly downloadable permit records from British Columbia public permit sources, deduplicating them, and maintaining current status plus status-change history.

This is intentionally conservative:

- It downloads only open datasets, public feature services, static public tables, and other sources that publish records without login or secret search input.
- It records skips for applicant-only, login-only, access-code, or search-input-only portals.
- It does not bypass CAPTCHA, logins, robots controls, access-code gates, or session restrictions.
- It retains raw source fields so field maps can be tuned as portals change.

## Quick start

```bash
go test ./...
go run ./cmd/permit-scraper --sources configs/sources.json --db data/permits-db --all --limit 25 --max-pages 1
```

The first run creates:

```text
data/permits-db/current.jsonl       # latest deduped current state
data/permits-db/history.jsonl       # insert/update history
data/permits-db/scrape_audit.jsonl  # per-source audit log
```

For a safe smoke run over every configured source row, including intentional skips:

```bash
go run ./cmd/permit-scraper --sources configs/sources.json --db data/permits-db --try-all --limit 10 --max-pages 1
```

Start the interactive GIS viewer after a scrape has produced `data/permits-db/current.jsonl`:

```bash
go run ./cmd/permit-map --db data/permits-db --web web --addr 127.0.0.1:8080
```

Open `http://127.0.0.1:8080/`. The viewer serves the current JSONL database through local API endpoints, renders valid latitude/longitude records on a Leaflet map, and keeps unmapped records available in the results sidebar.

Export the same viewer as static files when you do not want a local server:

```bash
go run ./cmd/permit-map-export --db data/permits-db --web web --out dist/permit-map
```

Open `dist/permit-map/index.html` directly or publish the `dist/permit-map` folder to any static host. The export embeds records, summary metrics, and audit rows in `data.js`, so it does not call `/api/*` or require localhost. Map tiles and Leaflet/Lucide assets are still loaded from their public CDNs unless you vendor those assets.

Build a portable Windows package:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/package-windows.ps1
```

The package is written to `dist/portable` and zipped as `dist/portable.zip`. It includes one executable, `pScraper.exe`, plus `configs/sources.json`, launch `.cmd` files, and the current `data/permits-db` when present. `pScraper.exe` contains the scraper, live GIS map server, static map exporter, and JSONL-to-SQLite importer. The map UI is embedded, so the live map does not need a separate `web` folder.

Portable direct commands:

```cmd
pScraper.exe scrape --sources configs\sources.json --db data\permits-db --all --limit 25 --max-pages 1
pScraper.exe map --db data\permits-db --addr 127.0.0.1:8080
pScraper.exe export-map --db data\permits-db --out map-export
pScraper.exe db import-jsonl --jsonl data\permits-db --sqlite data\permits.sqlite --reset
```

Use SQLite for relational storage after the JSONL path is working:

```bash
go run ./cmd/permit-scraper db import-jsonl --jsonl data/permits-db --sqlite data/permits.sqlite --reset
go run ./cmd/permit-scraper --sources configs/sources.json --store sqlite --db data/permits.sqlite --all --limit 25 --max-pages 1
```

## Source configuration

Edit `configs/sources.json`.

Important fields:

| Field | Meaning |
|---|---|
| `kind` | Scraper type: `opendatasoft_v2`, `arcgis_feature_service`, `html_table`, `nanaimo_whatsbuilding`, `report_download`, `report_download_needed`, `public_search_needs_input`, `applicant_login`, `application_hub`, or `authority_reference`. |
| `enabled` | Included by default runs. |
| `download_all` | Whether a bulk download is appropriate for this source. |
| `openly_searchable` | Whether the source exposes public records without applicant credentials. |
| `needs_input` | Whether a permit number/address/date/account is required. |
| `endpoint` | API endpoint. For ArcGIS this must be the actual `.../FeatureServer/<layer>/query` URL. |
| `field_map` | Maps canonical fields to source field names. Use `|` for fallbacks, e.g. `PermitNumber|permit_number|Permit No`. |

## Current included source rows

The configuration currently contains 76 source rows. Normal `--all` runs include the 23 enabled open/public rows; `--try-all` audits all rows and records why each skipped source was not bulk-scraped.

Enabled machine-readable sources include:

- OpenDataSoft: Vancouver issued building permits.
- ArcGIS FeatureServer layers: Kelowna, Maple Ridge, New Westminster, Port Moody, Columbia Shuswap Regional District, Coquitlam, Victoria permits and development applications, and BC Energy Regulator well surface hole permits.
- Public indexes and static HTML/table candidates that are safe to audit: Nanaimo What's Building, Township of Langley, North Saanich, Saanich, Richmond, City of Langley, Chilliwack, Regional District of Nanaimo, Regional District of Central Kootenay, and Regional District of Okanagan-Similkameen.

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
2. For each ArcGIS landing page, discover the exact FeatureServer layer URL and paste the `.../query` endpoint into `configs/sources.json`.
3. Add dedicated source scrapers only where an official public API exists.
4. Keep applicant-login portals out of bulk runs unless an authorised export/API exists.
5. Schedule low-frequency runs and keep the `scrape_audit.jsonl` file.
6. Review source terms, robots files, and privacy constraints before production use.

## Example ArcGIS endpoint

A valid ArcGIS endpoint usually looks like:

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
