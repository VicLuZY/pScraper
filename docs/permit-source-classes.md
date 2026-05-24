# Permit source classes

The package deliberately separates sources by automation safety.

## `opendatasoft_v2`

OpenDataSoft v2 records API. This is the best case for bulk collection because the source is explicitly an API. The Vancouver issued-building-permits dataset is configured this way.

## `arcgis_feature_service`

Permit-record query endpoints that happen to be published through ArcGIS FeatureServer or MapServer. These can usually be downloaded page-by-page with `where=1=1&outFields=*&f=json`. Only use this kind when the layer itself contains permit or development-application records; do not ingest generic GIS layers. Polygon permit records are mapped to a representative coordinate using their geometry bounds so they can appear on the portable map.

## `html_table`

Static HTML tables. The included parser is intentionally simple and safe. It does not execute JavaScript. If a tracker renders data client-side, discover the public API endpoint and add a dedicated scraper rather than trying to bypass the page.

## `nanaimo_whatsbuilding`

City of Nanaimo's public "What's Building" all-active-applications listing. This is treated as a published public index, not as a generic keyword search against a lookup form.

## `report_download`

Public CSV/TSV report downloads, or report index pages that link to supported CSV/TSV files. The scraper fetches the report, maps rows through `field_map`, and preserves the source report URL in each record's raw fields.

## `report_download_needed`

Public report indexes or PDF/spreadsheet report links where records may be published, but a dedicated parser still has to be added before bulk permit-record collection is reliable. These sources are audited as `endpoint_needed`; reachable PDF links are discovered and recorded in the audit message without attempting to parse the PDF.

## `public_search_needs_input`

Public lookup forms that require a permit number, address, date, or parcel. These are openly searchable but usually not safely or lawfully downloadable as “all permits” unless the site publishes an index. Use these for targeted monitoring lists only.

## `applicant_login`

Applicant, contractor, owner, or permit-holder portals. These are not open public scrape targets. Add an authorised export/API connector only when the user has the right to access those records.

## `application_hub`

Application intake hubs, not public permit databases.

## `authority_reference`

Routing/reference sources such as delegated electrical/gas authority lists.
