package web

import "embed"

// FS contains the browser UI used by the portable permit-map executable.
//
//go:embed index.html app.js styles.css
var FS embed.FS
