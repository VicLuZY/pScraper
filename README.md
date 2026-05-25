# pScraper Viewer

pScraper Viewer is a static GitHub Pages application for reviewing a prepopulated records database in the browser.

Live site: <https://vicluzy.github.io/pScraper/>

This repository contains only the viewer site. It does not include collection code, source configurations, local databases, generated datasets, credentials, packaged desktop apps, or runtime output.

All data loading happens locally in the browser. Uploaded files are not committed to this repository, sent to GitHub, or sent to an application server.

## Use

Open the GitHub Pages site and upload a local records file:

- `*.sqlite`, `*.sqlite3`, or `*.db` containing a `permit_current` table
- `current.jsonl`
- JSON containing either an array of records or an object with a `records` array

An optional SQLite progress database can also be uploaded. The viewer detects a compatible progress table by column names and renders a detail progress matrix without assuming a specific place or source.

The primary SQLite table is expected to expose record fields such as identifiers, status, type, address, dates, latitude, longitude, URL, and optional raw JSON. Missing optional fields are left blank in the interface.

Compatible progress tables are detected from date and status columns such as `created_date`, `applied_date`, `detail_status`, `progress_status`, or similar fields. If no status column is present but the table looks like completed records, the matrix treats rows as complete.

## Features

- Record counts, mapped counts, source counts, and loaded-file status
- Search and filters for jurisdiction, source, status, record type, date range, and mapped-only records
- Map for records with valid latitude and longitude
- Record list, selected-record detail panel, raw fields, and CSV export of the filtered view
- Optional progress matrix from a compatible SQLite progress database

## Repository Boundary

This public repository is intentionally limited to the static viewer and GitHub Pages workflow. It should not contain:

- local databases or generated exports
- desktop app packages or release output
- collection tools, source configurations, or runtime logs
- credentials, tokens, or environment-specific files

## Development

The static site lives in `web/`.

```bash
npm test
```

The test checks that the viewer files are self-contained enough for GitHub Pages and that no database, desktop app, release, runtime, or non-viewer artifacts are present in the repository.

Deployment is handled by `.github/workflows/pages.yml` on pushes to `main`.

## License

AGPL-3.0-only. See `LICENSE`.
