# /goal — Codex instructions

You are working on `bc-permit-scraper`, a Go package that collects openly downloadable British Columbia permit records and maintains a deduplicated current-status database.

## Non-negotiable constraints

1. Scrape only records that are openly published without login, CAPTCHA bypass, access codes, hidden credentials, or private session state.
2. Do not brute-force permit numbers, address ranges, parcel ranges, or date ranges against public lookup forms unless the source explicitly publishes a bulk index/API.
3. Applicant-only portals such as Cloudpermit workspaces, MyCity/MyDistrict portals, access-code workflows, and permit-holder dashboards must remain skipped unless an authorised export/API connector is added.
4. Respect source terms, robots controls, rate limits, and privacy requirements.
5. Keep all new source behaviour auditable through `scrape_audit.jsonl`.

## Current goal

Turn the starter into a production-grade BC permit ingestion system.

The system should:

- Download all permits from sources that publish public bulk datasets, public ArcGIS layers, public OpenDataSoft datasets, or static public tables.
- Maintain `current` status for each permit.
- Append history only when a newly scraped record is new or materially changed.
- Use robust dedupe that handles duplicate records from overlapping municipal sources.
- Record skip reasons for public-search-only, applicant-login, and hub/intake sources.
- Keep raw source fields so field maps can be refined without losing data.

## First tasks

1. Run the baseline tests:

   ```bash
   go test ./...
   ```

2. Run a limited smoke test:

   ```bash
   go run ./cmd/permit-scraper --sources configs/sources.json --db data/permits-db --try-all --limit 10 --max-pages 1
   ```

3. Inspect `data/permits-db/scrape_audit.jsonl` and classify every source as:

   - `ok`
   - `endpoint_needed`
   - `requires_search_input`
   - `login_or_authorized_only`
   - `not_public_bulk`
   - `broken_or_changed`

4. For each ArcGIS open-data landing page, discover the official FeatureServer layer query URL and update `endpoint` in `configs/sources.json`. Do not enable the source until the endpoint is verified.

5. Improve field maps by looking at raw records in `current.jsonl`.

## Required acceptance tests

Add or preserve tests that prove:

- Same jurisdiction + same permit number dedupes even when formatting differs.
- Same address fallback dedupes after street abbreviation normalization.
- Changed status produces one current record and a new history event.
- OpenDataSoft pagination works against an `httptest.Server`.
- ArcGIS pagination works against an `httptest.Server`.
- Unsupported/login/search-input-only sources produce audit skips, not hard failures, unless `--fail-fast` is set.

## Database work

The starter uses a standard-library JSONL database. Add a relational adapter only after tests pass.

Suggested path:

1. Keep the `storage` interface stable.
2. Add a `storage/sqlite` package behind a build tag or with a documented dependency.
3. Apply `db/schema.sql`.
4. Preserve the same upsert semantics as `JSONDB`.
5. Add migration/import command from JSONL to SQL.

## Scraper work

Prefer official APIs and open datasets. Build source-specific scrapers only when the public endpoint is clear.

Priority source classes:

1. OpenDataSoft datasets.
2. ArcGIS FeatureServer layers.
3. Static HTML tables.
4. CSV/PDF report downloads.
5. Public lookup forms only when monitoring a supplied list of permit numbers or addresses.

Do not add headless-browser scraping unless the source is public, allowed by terms, and there is no API alternative. Browser scraping should still be targeted and rate-limited.

## Output expectations

Every run should print JSON summary to stdout and append audit rows. Production runs should be schedulable with cron or a container job.
