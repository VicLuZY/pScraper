import { _electron as electron } from "playwright";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, "..", "..");
const sources = JSON.parse(fs.readFileSync(path.join(root, "configs", "sources.json"), "utf8")).sources;
const sourceCount = sources.length;
const enabledSourceCount = sources.filter((source) => source.enabled).length;

const executable = process.platform === "win32"
  ? path.join(root, "release", "win-unpacked", "pScraper.exe")
  : process.platform === "darwin"
    ? path.join(root, "release", "mac", "pScraper.app", "Contents", "MacOS", "pScraper")
    : path.join(root, "release", "linux-unpacked", "pscraper");

if (!fs.existsSync(executable)) {
  throw new Error(`Built Electron app not found at ${executable}. Run "npm run dist:dir" first.`);
}

if (process.platform === "linux" && !process.env.DISPLAY) {
  throw new Error("DISPLAY is not set. Run this under Xvfb or a desktop session.");
}

const scenarios = [
  {
    mode: "all",
    expectedSources: enabledSourceCount,
    requireAllOK: true,
  },
  {
    mode: "try-all",
    expectedSources: sourceCount,
    requireAllOK: false,
  },
];

for (const scenario of scenarios) {
  await runScenario(scenario);
}

async function runScenario(scenario) {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), `pscraper-electron-${scenario.mode}-`));
  const env = {
    ...process.env,
    XDG_CONFIG_HOME: path.join(tempRoot, "config"),
    HOME: path.join(tempRoot, "home"),
  };
  fs.mkdirSync(env.XDG_CONFIG_HOME, { recursive: true });
  fs.mkdirSync(env.HOME, { recursive: true });

  const app = await electron.launch({
    executablePath: executable,
    args: ["--no-sandbox"],
    env,
  });

  try {
    const win = await app.firstWindow();
    await win.waitForLoadState("domcontentloaded");
    await win.locator("#desktopPanel").waitFor({ state: "visible", timeout: 15000 });
    await win.selectOption("#scrapeMode", scenario.mode);
    await win.fill("#scrapeLimit", "3");
    await win.fill("#scrapeMaxPages", "1");
    await win.fill("#scrapeParallel", "6");
    await win.click("#runScrapeBtn");
    await win.locator("#desktopState", { hasText: "Running" }).waitFor({ timeout: 5000 });
    await win.locator("#desktopState", { hasText: "Idle" }).waitFor({ timeout: 120000 });

    const auditPath = findAuditPath(env);
    const latest = latestRunRows(auditPath);
    const counts = statusCounts(latest);
    const broken = latest.filter((row) => row.status === "broken_or_changed");
    const nonOK = latest.filter((row) => row.status !== "ok");

    if (latest.length !== scenario.expectedSources) {
      throw new Error(`${scenario.mode}: expected ${scenario.expectedSources} audit rows, got ${latest.length}`);
    }
    if (broken.length) {
      throw new Error(`${scenario.mode}: broken sources: ${broken.map((row) => row.source_id).join(", ")}`);
    }
    if (scenario.requireAllOK && nonOK.length) {
      throw new Error(`${scenario.mode}: non-ok sources: ${nonOK.map((row) => `${row.source_id}:${row.status}`).join(", ")}`);
    }

    console.log(`${scenario.mode}: ${latest.length} sources ${JSON.stringify(counts)}`);
  } finally {
    await app.close();
  }
}

function findAuditPath(env) {
  const candidates = [
    path.join(env.XDG_CONFIG_HOME, "pscraper", "data", "permits-db", "scrape_audit.jsonl"),
    path.join(env.XDG_CONFIG_HOME, "pScraper", "data", "permits-db", "scrape_audit.jsonl"),
    path.join(env.HOME, ".config", "pscraper", "data", "permits-db", "scrape_audit.jsonl"),
    path.join(env.HOME, ".config", "pScraper", "data", "permits-db", "scrape_audit.jsonl"),
  ];
  const found = candidates.find((candidate) => fs.existsSync(candidate));
  if (!found) {
    throw new Error(`scrape audit not found in: ${candidates.join(", ")}`);
  }
  return found;
}

function latestRunRows(auditPath) {
  const rows = fs.readFileSync(auditPath, "utf8")
    .trim()
    .split(/\n/)
    .filter(Boolean)
    .map((line) => JSON.parse(line));
  const runID = rows.at(-1)?.run_id;
  return rows.filter((row) => row.run_id === runID);
}

function statusCounts(rows) {
  return rows.reduce((counts, row) => {
    counts[row.status] = (counts[row.status] || 0) + 1;
    return counts;
  }, {});
}
