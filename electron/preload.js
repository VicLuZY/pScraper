const { contextBridge, ipcRenderer } = require("electron");

const listeners = new Map();

function subscribe(channel, callback) {
  if (typeof callback !== "function") {
    return () => {};
  }
  const wrapped = (_event, payload) => callback(payload);
  listeners.set(callback, wrapped);
  ipcRenderer.on(channel, wrapped);
  return () => {
    ipcRenderer.removeListener(channel, wrapped);
    listeners.delete(callback);
  };
}

contextBridge.exposeInMainWorld("pScraperDesktop", {
  runScrape: (options) => ipcRenderer.invoke("scrape:start", options),
  stopScrape: () => ipcRenderer.invoke("scrape:stop"),
  scrapeStatus: () => ipcRenderer.invoke("scrape:status"),
  openRuntimeDirectory: () => ipcRenderer.invoke("app:open-runtime-dir"),
  onScrapeLog: (callback) => subscribe("scrape:log", callback),
  onScrapeFinished: (callback) => subscribe("scrape:finished", callback),
  onBackendExit: (callback) => subscribe("desktop:backend-exit", callback),
});
