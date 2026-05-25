import { spawnSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, "..", "..");
const outDir = path.join(root, "dist", "electron-backend");
const exeName = process.platform === "win32" ? "pScraper.exe" : "pScraper";
const outPath = path.join(outDir, exeName);

mkdirSync(outDir, { recursive: true });

const result = spawnSync("go", ["build", "-o", outPath, "./cmd/permit-scraper"], {
  cwd: root,
  stdio: "inherit",
});

if (result.status !== 0) {
  process.exit(result.status ?? 1);
}

console.log(`Built Go backend: ${path.relative(root, outPath)}`);
