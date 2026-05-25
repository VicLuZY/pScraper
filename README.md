# Permit Viewer

Permit Viewer is a static GitHub Pages application for reviewing a prepopulated permit database in the browser.

The repository contains only the viewer. It does not include permit collection code, source configurations, local databases, generated datasets, credentials, or runtime output.

## Use

Open the GitHub Pages site, then upload one of these local files:

- `permits.sqlite`, `*.sqlite3`, or `*.db` containing a `permit_current` table
- `current.jsonl`
- JSON containing either an array of permit records or an object with a `records` array

An optional `vancouver_posse_index.sqlite` file can also be uploaded to render the Vancouver detail progress matrix.

Files are read in the browser for the current session. The viewer does not upload the selected database to this repository.

## Features

- Record counts, mapped counts, source counts, and loaded-file status
- Search and filters for jurisdiction, source, status, permit type, date range, and mapped-only records
- Leaflet map for records with valid latitude and longitude
- Record list, selected-record detail panel, raw source fields, and CSV export of the filtered view
- Optional Vancouver progress matrix using the uploaded index database

## Development

The static site lives in `web/`.

```bash
npm test
```

The test checks that the viewer files are self-contained enough for GitHub Pages and that no scraper/runtime source files are present in the repository.

## License

AGPL-3.0-only. See `LICENSE`.
