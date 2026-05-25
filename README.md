# pScraper Viewer

pScraper Viewer is a static GitHub Pages application for reviewing a prepopulated records database in the browser.

This repository contains only the viewer site. It does not include collection code, source configurations, local databases, generated datasets, credentials, packaged desktop apps, or runtime output.

## Use

Open the GitHub Pages site, then upload one of these local files:

- `*.sqlite`, `*.sqlite3`, or `*.db` containing a `permit_current` table
- `current.jsonl`
- JSON containing either an array of records or an object with a `records` array

An optional SQLite progress database can also be uploaded. The viewer detects a compatible progress table by its columns and renders the detail progress matrix without assuming a specific place or source.

Files are read in the browser for the current session. The viewer does not upload the selected database to this repository or to a server.

## Features

- Record counts, mapped counts, source counts, and loaded-file status
- Search and filters for jurisdiction, source, status, record type, date range, and mapped-only records
- Map for records with valid latitude and longitude
- Record list, selected-record detail panel, raw fields, and CSV export of the filtered view
- Optional progress matrix from a compatible SQLite progress database

## Development

The static site lives in `web/`.

```bash
npm test
```

The test checks that the viewer files are self-contained enough for GitHub Pages and that no scraper, database, desktop app, or release artifacts are present in the repository.

## License

AGPL-3.0-only. See `LICENSE`.
