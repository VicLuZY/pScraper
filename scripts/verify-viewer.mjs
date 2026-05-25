import fs from "node:fs";
import path from "node:path";

const root = process.cwd();
const required = [
  "web/index.html",
  "web/app.js",
  "web/styles.css",
  "web/.nojekyll",
  ".github/workflows/pages.yml",
  "LICENSE",
  "README.md",
];

for (const file of required) {
  if (!fs.existsSync(path.join(root, file))) {
    throw new Error(`Missing required viewer file: ${file}`);
  }
}

const forbidden = [
  "cmd",
  "internal",
  "configs",
  "native",
  "db",
  "examples",
  "go.mod",
  "go.sum",
  "dist/pScraper.exe",
];

for (const file of forbidden) {
  if (fs.existsSync(path.join(root, file))) {
    throw new Error(`Repository still contains non-viewer artifact: ${file}`);
  }
}

const html = fs.readFileSync(path.join(root, "web/index.html"), "utf8");
for (const asset of ["styles.css", "app.js"]) {
  if (!html.includes(asset)) {
    throw new Error(`web/index.html does not reference ${asset}`);
  }
}

const app = fs.readFileSync(path.join(root, "web/app.js"), "utf8");
for (const expected of ["permit_current", "vancouver_posse_index", "arrayBuffer", "initSqlJs"]) {
  if (!app.includes(expected)) {
    throw new Error(`web/app.js is missing expected viewer capability: ${expected}`);
  }
}

console.log("viewer verification ok");
