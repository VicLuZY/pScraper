const { app, BrowserWindow, ipcMain, shell } = require("electron");
const { spawn } = require("child_process");
const fs = require("fs");
const http = require("http");
const net = require("net");
const path = require("path");

let mainWindow;
let backendProcess;
let scrapeProcess;
let backendUrl;
let runtimeDir;

const isSmoke = process.env.PSCRAPER_ELECTRON_SMOKE === "1";

if (process.platform === "linux") {
  app.commandLine.appendSwitch("no-sandbox");
}

function appRoot() {
  return app.isPackaged ? process.resourcesPath : path.resolve(__dirname, "..");
}

function bundledAssetPath(...parts) {
  return path.join(app.isPackaged ? app.getAppPath() : appRoot(), ...parts);
}

function backendExecutableName() {
  return process.platform === "win32" ? "pScraper.exe" : "pScraper";
}

function backendExecutablePath() {
  const name = backendExecutableName();
  const candidates = app.isPackaged
    ? [path.join(process.resourcesPath, "backend", name)]
    : [
        path.join(appRoot(), "dist", "electron-backend", name),
        path.join(appRoot(), "dist", "pScraper.exe"),
      ];

  const found = candidates.find((candidate) => fs.existsSync(candidate));
  if (!found) {
    throw new Error(`Go backend executable not found. Run "npm run go:build" first.`);
  }
  return found;
}

function backendWorkingDirectory() {
  if (runtimeDir) {
    return runtimeDir;
  }
  runtimeDir = app.isPackaged ? app.getPath("userData") : appRoot();
  fs.mkdirSync(runtimeDir, { recursive: true });
  return runtimeDir;
}

async function startBackend() {
  const port = await findAvailablePort();
  backendUrl = `http://127.0.0.1:${port}`;
  const args = ["map", "--addr", `127.0.0.1:${port}`, "--db", "data/permits-db"];

  backendProcess = spawn(backendExecutablePath(), args, {
    cwd: backendWorkingDirectory(),
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });

  backendProcess.stdout.on("data", (chunk) => logBackend(chunk));
  backendProcess.stderr.on("data", (chunk) => logBackend(chunk));
  backendProcess.once("exit", (code, signal) => {
    backendProcess = null;
    if (!app.isQuitting) {
      const reason = signal ? `signal ${signal}` : `code ${code}`;
      broadcast("desktop:backend-exit", { reason });
    }
  });

  await waitForServer(backendUrl);
  return backendUrl;
}

function logBackend(chunk) {
  const text = chunk.toString().trim();
  if (text) {
    console.log(`[backend] ${text}`);
  }
}

function findAvailablePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      server.close(() => resolve(address.port));
    });
  });
}

function waitForServer(url) {
  const deadline = Date.now() + 15000;

  return new Promise((resolve, reject) => {
    const tryRequest = () => {
      const req = http.get(url, (res) => {
        res.resume();
        resolve();
      });
      req.on("error", (err) => {
        if (Date.now() > deadline) {
          reject(new Error(`Map server did not start: ${err.message}`));
          return;
        }
        setTimeout(tryRequest, 150);
      });
      req.setTimeout(1000, () => {
        req.destroy(new Error("server start timed out"));
      });
    };
    tryRequest();
  });
}

async function createWindow() {
  const url = await startBackend();
  mainWindow = new BrowserWindow({
    width: 1360,
    height: 900,
    minWidth: 1040,
    minHeight: 720,
    title: "pScraper",
    icon: bundledAssetPath("assets", "app-icon.png"),
    backgroundColor: "#eef2f4",
    show: false,
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  mainWindow.once("ready-to-show", () => {
    mainWindow.show();
  });

  mainWindow.webContents.setWindowOpenHandler(({ url: target }) => {
    shell.openExternal(target);
    return { action: "deny" };
  });
  mainWindow.webContents.on("will-navigate", (event, target) => {
    if (!target.startsWith(url)) {
      event.preventDefault();
      shell.openExternal(target);
    }
  });

  if (isSmoke) {
    mainWindow.webContents.once("did-finish-load", async () => {
      const ok = await mainWindow.webContents.executeJavaScript(
        "Boolean(document.querySelector('#map') && document.body.innerText.includes('BC Permit Map'))"
      );
      console.log(`[electron-smoke] renderer loaded: ${ok}`);
      app.quit();
    });
  }

  await mainWindow.loadURL(url);
}

function broadcast(channel, payload) {
  BrowserWindow.getAllWindows().forEach((win) => {
    win.webContents.send(channel, payload);
  });
}

function normalizedScrapeOptions(input = {}) {
  const mode = input.mode === "try-all" ? "try-all" : "all";
  return {
    mode,
    limit: positiveInt(input.limit, mode === "try-all" ? 10 : 25),
    maxPages: positiveInt(input.maxPages, 1),
    parallel: positiveInt(input.parallel, 4),
  };
}

function positiveInt(value, fallback) {
  const n = Number.parseInt(value, 10);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}

ipcMain.handle("scrape:start", async (_event, input) => {
  if (scrapeProcess) {
    return { ok: false, message: "A scrape is already running." };
  }

  const options = normalizedScrapeOptions(input);
  const args = [
    "scrape",
    options.mode === "try-all" ? "--try-all" : "--all",
    "--sources",
    "configs/sources.json",
    "--db",
    "data/permits-db",
    "--limit",
    String(options.limit),
    "--max-pages",
    String(options.maxPages),
    "--parallel",
    String(options.parallel),
  ];

  scrapeProcess = spawn(backendExecutablePath(), args, {
    cwd: backendWorkingDirectory(),
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });

  const startedAt = new Date().toISOString();
  broadcast("scrape:log", { stream: "system", text: `Started ${options.mode} scrape at ${startedAt}` });
  scrapeProcess.stdout.on("data", (chunk) => {
    broadcast("scrape:log", { stream: "stdout", text: chunk.toString() });
  });
  scrapeProcess.stderr.on("data", (chunk) => {
    broadcast("scrape:log", { stream: "stderr", text: chunk.toString() });
  });
  scrapeProcess.once("exit", (code, signal) => {
    const finished = { code, signal, stopped: Boolean(signal), finishedAt: new Date().toISOString() };
    scrapeProcess = null;
    broadcast("scrape:finished", finished);
  });

  return { ok: true, pid: scrapeProcess.pid, options };
});

ipcMain.handle("scrape:stop", async () => {
  if (!scrapeProcess) {
    return { ok: false, message: "No scrape is running." };
  }
  scrapeProcess.kill();
  return { ok: true };
});

ipcMain.handle("scrape:status", async () => {
  return { running: Boolean(scrapeProcess), backendUrl, runtimeDir: backendWorkingDirectory() };
});

ipcMain.handle("app:open-runtime-dir", async () => {
  await shell.openPath(backendWorkingDirectory());
  return { ok: true };
});

app.whenReady().then(createWindow).catch((err) => {
  console.error(err);
  app.exit(1);
});

app.on("activate", () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow().catch((err) => {
      console.error(err);
      app.exit(1);
    });
  }
});

app.on("before-quit", () => {
  app.isQuitting = true;
  if (scrapeProcess) {
    scrapeProcess.kill();
    scrapeProcess = null;
  }
  if (backendProcess) {
    backendProcess.kill();
    backendProcess = null;
  }
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
