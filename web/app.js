const state = {
  records: [],
  filtered: [],
  markers: new Map(),
  selectedKey: "",
  firstFit: true,
  permitFileName: "",
  matrix: null,
  sqlPromise: null,
};

const els = {};
let map;
let markerLayer;

const statusColors = {
  active: "#0f7b6c",
  issued: "#2d6fbb",
  complete: "#287a3e",
  pending: "#a35a00",
  closed: "#5d6970",
  other: "#7b4fa3",
  none: "#7d858b",
};

document.addEventListener("DOMContentLoaded", () => {
  cacheElements();
  initMap();
  bindControls();
  renderEmpty();
});

function cacheElements() {
  [
    "dataSubtitle",
    "permitFileInput",
    "indexFileInput",
    "uploadStatus",
    "metricRecords",
    "metricMapped",
    "metricSources",
    "metricFile",
    "filterCount",
    "searchInput",
    "jurisdictionFilter",
    "sourceFilter",
    "statusFilter",
    "typeFilter",
    "fromDate",
    "toDate",
    "mappedOnly",
    "unmappedCount",
    "statusGroupCount",
    "recordsList",
    "resultCount",
    "detailPanel",
    "statusText",
    "mapLegend",
    "fitMapBtn",
    "resetBtn",
    "exportBtn",
    "matrixRange",
    "matrixPercent",
    "matrixCounts",
    "matrixCanvas",
  ].forEach((id) => {
    els[id] = document.getElementById(id);
  });
}

function initMap() {
  map = L.map("map", { zoomControl: false }).setView([53.7, -124.8], 5);
  L.control.zoom({ position: "bottomright" }).addTo(map);
  L.tileLayer("https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png", {
    maxZoom: 19,
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors &copy; <a href="https://carto.com/attributions">CARTO</a>',
  }).addTo(map);
  markerLayer = L.layerGroup().addTo(map);
}

function bindControls() {
  [
    "searchInput",
    "jurisdictionFilter",
    "sourceFilter",
    "statusFilter",
    "typeFilter",
    "fromDate",
    "toDate",
    "mappedOnly",
  ].forEach((id) => {
    els[id].addEventListener("input", applyFilters);
    els[id].addEventListener("change", applyFilters);
  });

  els.permitFileInput.addEventListener("change", loadPermitFile);
  els.indexFileInput.addEventListener("change", loadIndexFile);
  els.fitMapBtn.addEventListener("click", fitVisibleRecords);
  els.resetBtn.addEventListener("click", resetFilters);
  els.exportBtn.addEventListener("click", exportFilteredCSV);
  window.addEventListener("resize", () => renderMatrix(state.matrix));
}

async function loadPermitFile(event) {
  const file = event.target.files && event.target.files[0];
  if (!file) return;
  setStatus(`Loading ${file.name}`);
  try {
    const records = await readPermitRecords(file);
    state.records = records.map(enrichRecord);
    state.filtered = state.records.slice();
    state.selectedKey = "";
    state.firstFit = true;
    state.permitFileName = file.name;
    populateFilters(state.records);
    renderSummary();
    applyFilters();
    els.detailPanel.replaceChildren(emptyState("Select a record"));
    setStatus(`Loaded ${number(state.records.length)} records`);
    els.uploadStatus.textContent = `${file.name} loaded. Files stay in this browser session.`;
  } catch (err) {
    setStatus(`Load failed: ${err.message}`);
    els.uploadStatus.textContent = err.message;
  }
}

async function loadIndexFile(event) {
  const file = event.target.files && event.target.files[0];
  if (!file) return;
  setStatus(`Loading ${file.name}`);
  try {
    const matrix = await readVancouverIndex(file);
    state.matrix = matrix;
    renderMatrix(matrix);
    setStatus(`Loaded ${file.name}`);
  } catch (err) {
    setStatus(`Index load failed: ${err.message}`);
  }
}

async function readPermitRecords(file) {
  const lower = file.name.toLowerCase();
  if (lower.endsWith(".sqlite") || lower.endsWith(".sqlite3") || lower.endsWith(".db")) {
    return readPermitSQLite(file);
  }
  const text = await file.text();
  if (lower.endsWith(".jsonl")) {
    return text
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
      .map((line) => JSON.parse(line));
  }
  const parsed = JSON.parse(text);
  if (Array.isArray(parsed)) return parsed;
  if (Array.isArray(parsed.records)) return parsed.records;
  throw new Error("JSON upload must be an array of records or an object with a records array.");
}

async function readPermitSQLite(file) {
  const db = await openSQLite(file);
  try {
    if (!sqliteTableExists(db, "permit_current")) {
      throw new Error("SQLite database does not contain permit_current.");
    }
    return sqliteObjects(
      db,
      `SELECT
        dedupe_key, source_id, source_name, jurisdiction, jurisdiction_type, region,
        permit_number, application_id, permit_type, permit_family, status, address,
        pid, roll_number, applicant, contractor, description, applied_date, issued_date,
        final_date, completed_date, value, latitude, longitude, url, raw_json,
        first_seen_at, last_seen_at, last_changed_at, scraped_at
      FROM permit_current
      ORDER BY COALESCE(applied_date, issued_date, completed_date, final_date, ''), dedupe_key`
    ).map((row) => {
      if (typeof row.raw_json === "string" && row.raw_json.trim()) {
        try {
          row.raw = JSON.parse(row.raw_json);
        } catch {
          row.raw = { raw_json: row.raw_json };
        }
      }
      delete row.raw_json;
      return row;
    });
  } finally {
    db.close();
  }
}

async function readVancouverIndex(file) {
  const db = await openSQLite(file);
  try {
    if (!sqliteTableExists(db, "vancouver_posse_index")) {
      throw new Error("SQLite database does not contain vancouver_posse_index.");
    }
    const rows = sqliteObjects(
      db,
      `SELECT
        COALESCE(NULLIF(created_date, ''), 'Undated') AS created_date,
        CASE
          WHEN detail_status = 'scraping' THEN 'scraping'
          WHEN detail_status = 'error' THEN 'error'
          WHEN detail_status = 'scraped' THEN 'scraped'
          ELSE 'not_processed'
        END AS detail_state,
        COUNT(*) AS record_count
      FROM vancouver_posse_index
      GROUP BY created_date, detail_state
      ORDER BY created_date, detail_state`
    );
    const days = [];
    const byDate = new Map();
    const matrix = {
      fileName: file.name,
      index_total: 0,
      scraped: 0,
      scraping: 0,
      errors: 0,
      not_processed: 0,
      percent: 0,
      start_date: "",
      end_date: todayISODate(),
      days,
    };
    rows.forEach((row) => {
      let day = byDate.get(row.created_date);
      if (!day) {
        day = { date: row.created_date, total: 0, not_processed: 0, scraped: 0, scraping: 0, error: 0 };
        byDate.set(row.created_date, day);
        days.push(day);
      }
      const count = Number(row.record_count || 0);
      day.total += count;
      if (row.detail_state === "scraped") day.scraped += count;
      else if (row.detail_state === "scraping") day.scraping += count;
      else if (row.detail_state === "error") day.error += count;
      else day.not_processed += count;
    });
    days.forEach((day) => {
      matrix.index_total += day.total;
      matrix.scraped += day.scraped;
      matrix.scraping += day.scraping;
      matrix.errors += day.error;
      matrix.not_processed += day.not_processed;
    });
    matrix.start_date = days.find((day) => day.date !== "Undated")?.date || "";
    if (matrix.index_total > 0) {
      matrix.percent = Math.min(100, (matrix.scraped / matrix.index_total) * 100);
    }
    return matrix;
  } finally {
    db.close();
  }
}

async function openSQLite(file) {
  if (!window.initSqlJs) {
    throw new Error("SQLite reader failed to load.");
  }
  if (!state.sqlPromise) {
    state.sqlPromise = window.initSqlJs({
      locateFile: (name) => `https://cdnjs.cloudflare.com/ajax/libs/sql.js/1.10.3/${name}`,
    });
  }
  const SQL = await state.sqlPromise;
  return new SQL.Database(new Uint8Array(await file.arrayBuffer()));
}

function sqliteTableExists(db, tableName) {
  const rows = sqliteObjects(db, "SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", [tableName]);
  return rows.length > 0;
}

function sqliteObjects(db, sql, params = []) {
  const result = db.exec(sql, params);
  if (result.length === 0) return [];
  return result[0].values.map((values) => Object.fromEntries(result[0].columns.map((column, index) => [column, values[index]])));
}

function enrichRecord(record, index) {
  const coords = coordinateFor(record);
  const key = record.dedupe_key || `${record.source_id || "source"}-${record.permit_number || record.application_id || index}`;
  const dateValue = first(record.issued_date, record.applied_date, record.completed_date, record.final_date);
  const displayId = first(record.permit_number, record.application_id, record.dedupe_key, "Unknown permit");
  const status = first(record.status, "Unspecified");
  const type = first(record.permit_type, record.permit_family, "Unspecified");
  return {
    ...record,
    _key: key,
    _displayId: displayId,
    _date: normalizeDate(dateValue),
    _dateLabel: first(dateValue, "No date"),
    _lat: coords.lat,
    _lon: coords.lon,
    _mapped: Number.isFinite(coords.lat) && Number.isFinite(coords.lon),
    _statusGroup: statusGroup(status),
    _search: [
      displayId,
      record.application_id,
      status,
      type,
      record.address,
      record.description,
      record.jurisdiction,
      record.source_name,
      record.applicant,
      record.contractor,
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase(),
  };
}

function coordinateFor(record) {
  const lat = Number.parseFloat(first(record.latitude, record.lat));
  const lon = Number.parseFloat(first(record.longitude, record.lon, record.lng));
  return { lat, lon };
}

function populateFilters(records) {
  fillSelect(els.jurisdictionFilter, "All jurisdictions", countValues(records, "jurisdiction"));
  fillSelect(els.sourceFilter, "All sources", countValues(records, "source_name"));
  fillSelect(els.statusFilter, "All statuses", countValues(records, "status"));
  fillSelect(els.typeFilter, "All permit types", countValues(records, "permit_type"));
}

function fillSelect(select, label, counts) {
  const previous = select.value;
  select.replaceChildren();
  select.append(option("", label));
  [...counts.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .forEach(([value, count]) => select.append(option(value, `${value} (${count})`)));
  if ([...select.options].some((opt) => opt.value === previous)) {
    select.value = previous;
  }
}

function option(value, label) {
  const opt = document.createElement("option");
  opt.value = value;
  opt.textContent = label;
  return opt;
}

function countValues(records, field) {
  const counts = new Map();
  records.forEach((record) => {
    const value = first(record[field], "Unspecified");
    counts.set(value, (counts.get(value) || 0) + 1);
  });
  return counts;
}

function applyFilters() {
  const q = els.searchInput.value.trim().toLowerCase();
  const jurisdiction = els.jurisdictionFilter.value;
  const source = els.sourceFilter.value;
  const status = els.statusFilter.value;
  const type = els.typeFilter.value;
  const from = els.fromDate.value;
  const to = els.toDate.value;
  const mappedOnly = els.mappedOnly.checked;

  state.filtered = state.records.filter((record) => {
    if (q && !record._search.includes(q)) return false;
    if (jurisdiction && record.jurisdiction !== jurisdiction) return false;
    if (source && first(record.source_name, "Unspecified") !== source) return false;
    if (status && first(record.status, "Unspecified") !== status) return false;
    if (type && first(record.permit_type, "Unspecified") !== type) return false;
    if (mappedOnly && !record._mapped) return false;
    if (from && (!record._date || record._date < from)) return false;
    if (to && (!record._date || record._date > to)) return false;
    return true;
  });

  renderMarkers();
  renderResults();
  renderFilterStats();
  renderLegend();
  if (state.firstFit) {
    fitVisibleRecords();
    state.firstFit = false;
  }
}

function renderSummary() {
  const sourceCount = new Set(state.records.map((record) => first(record.source_id, record.source_name)).filter(Boolean)).size;
  const mapped = state.records.filter((record) => record._mapped).length;
  els.metricRecords.textContent = number(state.records.length);
  els.metricMapped.textContent = number(mapped);
  els.metricSources.textContent = number(sourceCount);
  els.metricFile.textContent = state.permitFileName || "None";
  els.dataSubtitle.textContent = state.permitFileName
    ? `${state.permitFileName} - ${number(state.records.length - mapped)} unmapped records`
    : "Upload a prepopulated permit database to begin";
}

function renderEmpty() {
  populateFilters([]);
  renderSummary();
  renderMarkers();
  renderResults();
  renderFilterStats();
  renderLegend();
  renderMatrix(null);
}

function renderMarkers() {
  markerLayer.clearLayers();
  state.markers.clear();
  state.filtered
    .filter((record) => record._mapped)
    .forEach((record) => {
      const color = colorFor(record);
      const marker = L.circleMarker([record._lat, record._lon], {
        radius: 7,
        color: "#ffffff",
        weight: 2,
        fillColor: color,
        fillOpacity: 0.92,
      });
      marker.bindPopup(popupHTML(record));
      marker.on("click", () => selectRecord(record._key, false));
      marker.addTo(markerLayer);
      state.markers.set(record._key, marker);
    });
}

function renderResults() {
  els.recordsList.replaceChildren();
  els.resultCount.textContent = `${number(state.filtered.length)} shown`;
  if (state.records.length === 0) {
    els.recordsList.append(emptyState("Upload a database"));
    return;
  }
  if (state.filtered.length === 0) {
    els.recordsList.append(emptyState("No records match"));
    return;
  }
  const fragment = document.createDocumentFragment();
  state.filtered.slice(0, 500).forEach((record) => {
    const row = document.createElement("button");
    row.type = "button";
    row.className = `record-row${record._key === state.selectedKey ? " selected" : ""}`;
    row.addEventListener("click", () => selectRecord(record._key, true));

    const rail = document.createElement("span");
    rail.className = "rail";
    rail.style.background = colorFor(record);
    row.append(rail);

    const body = document.createElement("span");
    const title = document.createElement("span");
    title.className = "record-title";
    title.textContent = record._displayId;
    body.append(title);

    const meta = document.createElement("span");
    meta.className = "record-meta";
    meta.textContent = [first(record.status, "No status"), first(record.permit_type, "No type"), first(record._dateLabel, "No date")]
      .filter(Boolean)
      .join(" | ");
    body.append(meta);

    const address = document.createElement("span");
    address.className = "record-address";
    address.textContent = first(record.address, record.jurisdiction, record.source_name);
    body.append(address);
    row.append(body);
    fragment.append(row);
  });
  if (state.filtered.length > 500) {
    fragment.append(emptyState(`Showing first 500 of ${number(state.filtered.length)} records`));
  }
  els.recordsList.append(fragment);
}

function renderFilterStats() {
  const mapped = state.filtered.filter((record) => record._mapped).length;
  const statuses = new Set(state.filtered.map((record) => first(record.status, "No status")));
  els.filterCount.textContent = `${number(state.filtered.length)} visible`;
  els.unmappedCount.textContent = number(state.filtered.length - mapped);
  els.statusGroupCount.textContent = number(statuses.size);
  setStatus(`${number(state.filtered.length)} visible - ${number(mapped)} mapped`);
}

function renderLegend() {
  els.mapLegend.replaceChildren();
  const counts = new Map();
  state.filtered
    .filter((record) => record._mapped)
    .forEach((record) => counts.set(record._statusGroup, (counts.get(record._statusGroup) || 0) + 1));
  if (counts.size === 0) {
    els.mapLegend.append(legendRow(statusColors.none, "No mapped records"));
    return;
  }
  [...counts.entries()]
    .sort((a, b) => b[1] - a[1])
    .forEach(([group, count]) => els.mapLegend.append(legendRow(statusColors[group] || statusColors.other, `${labelCase(group)} (${count})`)));
}

function renderMatrix(matrix) {
  const canvas = els.matrixCanvas;
  const wrap = canvas.parentElement;
  const width = Math.max(1, Math.floor(wrap?.clientWidth || canvas.clientWidth || 1));
  const total = Number(matrix?.index_total || 0);
  const pct = total > 0 ? Math.min(100, (Number(matrix.scraped || 0) / total) * 100) : 0;
  els.matrixPercent.textContent = `${pct.toFixed(total > 0 && pct < 10 ? 1 : 0)}%`;
  els.matrixCounts.textContent = `${number(matrix?.scraped || 0)} / ${number(total)}`;
  els.matrixRange.textContent = matrix && total > 0
    ? `${matrix.start_date || "Undated"} to ${matrix.end_date || todayISODate()} - ${matrix.fileName}`
    : "Upload an index database to view detail progress";

  const dot = 2;
  const gap = 1;
  const step = dot + gap;
  const columns = Math.max(1, Math.floor((width - gap) / step));
  const rows = Math.max(1, Math.ceil(total / columns));
  const height = Math.max(step, rows * step + gap);
  canvas.width = width;
  canvas.height = height;
  canvas.style.width = `${width}px`;
  canvas.style.height = `${height}px`;

  const ctx = canvas.getContext("2d");
  ctx.clearRect(0, 0, width, height);
  if (!matrix || total <= 0) return;

  let index = 0;
  const drawDots = (count, color) => {
    ctx.fillStyle = color;
    for (let i = 0; i < count; i += 1) {
      const col = index % columns;
      const row = Math.floor(index / columns);
      ctx.fillRect(col * step + gap, row * step + gap, dot, dot);
      index += 1;
    }
  };
  matrix.days.forEach((day) => {
    drawDots(Number(day.not_processed || 0), "#c9d1d5");
    drawDots(Number(day.scraped || 0), "#287a3e");
    drawDots(Number(day.scraping || 0), "#c89116");
    drawDots(Number(day.error || 0), "#b93636");
  });
}

function legendRow(color, label) {
  const row = document.createElement("div");
  row.className = "legend-row";
  const dot = document.createElement("span");
  dot.className = "dot";
  dot.style.background = color;
  const text = document.createElement("span");
  text.textContent = label;
  row.append(dot, text);
  return row;
}

function selectRecord(key, moveMap) {
  const record = state.records.find((item) => item._key === key);
  if (!record) return;
  state.selectedKey = key;
  renderResults();
  renderDetail(record);
  const marker = state.markers.get(key);
  if (moveMap && marker) {
    map.setView(marker.getLatLng(), Math.max(map.getZoom(), 13), { animate: true });
    marker.openPopup();
  }
}

function renderDetail(record) {
  els.detailPanel.replaceChildren();
  const title = document.createElement("div");
  title.className = "detail-title";
  title.textContent = record._displayId;
  els.detailPanel.append(title);

  const dl = document.createElement("dl");
  dl.className = "detail-grid";
  [
    ["Status", first(record.status, "No status")],
    ["Type", first(record.permit_type, "No type")],
    ["Jurisdiction", record.jurisdiction],
    ["Source", record.source_name],
    ["Address", record.address],
    ["Applied", record.applied_date],
    ["Issued", record.issued_date],
    ["Completed", record.completed_date],
    ["Final", record.final_date],
    ["Applicant", record.applicant],
    ["Contractor", record.contractor],
    ["Value", record.value],
    ["Description", record.description],
    ["URL", record.url],
    ["Mapped", record._mapped ? `${record._lat.toFixed(5)}, ${record._lon.toFixed(5)}` : "No valid coordinates"],
  ].forEach(([label, value]) => appendDef(dl, label, first(value, "")));
  els.detailPanel.append(dl);

  if (record.raw && Object.keys(record.raw).length > 0) {
    const details = document.createElement("details");
    details.className = "raw-block";
    const summary = document.createElement("summary");
    summary.textContent = "Raw source fields";
    details.append(summary);
    const table = document.createElement("table");
    table.className = "raw-table";
    Object.keys(record.raw)
      .sort((a, b) => a.localeCompare(b))
      .forEach((key) => {
        const tr = document.createElement("tr");
        const k = document.createElement("td");
        const v = document.createElement("td");
        k.textContent = key;
        v.textContent = record.raw[key];
        tr.append(k, v);
        table.append(tr);
      });
    details.append(table);
    els.detailPanel.append(details);
  }
}

function appendDef(dl, label, value) {
  if (!value) return;
  const dt = document.createElement("dt");
  const dd = document.createElement("dd");
  dt.textContent = label;
  dd.textContent = value;
  dl.append(dt, dd);
}

function fitVisibleRecords() {
  const mappedRecords = state.filtered.filter((record) => record._mapped);
  if (mappedRecords.length === 0) {
    map.setView([53.7, -124.8], 5);
    return;
  }
  const bounds = L.latLngBounds(mappedRecords.map((record) => [record._lat, record._lon]));
  map.fitBounds(bounds.pad(0.18), { maxZoom: 13 });
}

function resetFilters() {
  ["searchInput", "jurisdictionFilter", "sourceFilter", "statusFilter", "typeFilter", "fromDate", "toDate"].forEach((id) => {
    els[id].value = "";
  });
  els.mappedOnly.checked = false;
  state.selectedKey = "";
  els.detailPanel.replaceChildren(emptyState(state.records.length ? "Select a record" : "Upload a database"));
  applyFilters();
  fitVisibleRecords();
}

function exportFilteredCSV() {
  const columns = [
    "source_name",
    "jurisdiction",
    "permit_number",
    "application_id",
    "permit_type",
    "status",
    "address",
    "issued_date",
    "applied_date",
    "completed_date",
    "value",
    "latitude",
    "longitude",
    "description",
    "url",
  ];
  const rows = [columns.join(",")].concat(
    state.filtered.map((record) => columns.map((column) => csvCell(record[column] || "")).join(","))
  );
  const blob = new Blob([rows.join("\n")], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = "filtered-permits.csv";
  link.click();
  URL.revokeObjectURL(url);
}

function csvCell(value) {
  const text = String(value).replaceAll('"', '""');
  return `"${text}"`;
}

function popupHTML(record) {
  return `
    <div class="popup-title">${escapeHTML(record._displayId)}</div>
    <div class="popup-line">${escapeHTML(first(record.status, "No status"))}</div>
    <div class="popup-line">${escapeHTML(first(record.permit_type, "No type"))}</div>
    <div class="popup-line">${escapeHTML(first(record.address, record.jurisdiction, ""))}</div>
  `;
}

function statusGroup(status) {
  const s = (status || "").toLowerCase();
  if (!s) return "none";
  if (s.includes("active")) return "active";
  if (s.includes("issued")) return "issued";
  if (s.includes("complete") || s.includes("completed") || s.includes("final")) return "complete";
  if (s.includes("closed")) return "closed";
  if (s.includes("pending") || s.includes("progress") || s.includes("submitted")) return "pending";
  return "other";
}

function colorFor(record) {
  return statusColors[record._statusGroup] || statusColors.other;
}

function normalizeDate(value) {
  const s = first(value, "");
  if (!s) return "";
  if (/^\d{4}-\d{2}-\d{2}$/.test(s)) return s;
  if (/^\d{8}$/.test(s)) return `${s.slice(0, 4)}-${s.slice(4, 6)}-${s.slice(6, 8)}`;
  const dmy = s.match(/^(\d{2})\/(\d{2})\/(\d{4})$/);
  if (dmy) return `${dmy[3]}-${dmy[2]}-${dmy[1]}`;
  const date = new Date(s);
  if (!Number.isNaN(date.valueOf())) return date.toISOString().slice(0, 10);
  return "";
}

function emptyState(text) {
  const div = document.createElement("div");
  div.className = "empty-state";
  div.textContent = text;
  return div;
}

function setStatus(text) {
  els.statusText.textContent = text;
}

function first(...values) {
  for (const value of values) {
    if (value !== null && value !== undefined && String(value).trim() !== "") {
      return String(value).trim();
    }
  }
  return "";
}

function number(value) {
  return Number(value || 0).toLocaleString();
}

function todayISODate() {
  return new Date().toISOString().slice(0, 10);
}

function labelCase(value) {
  return String(value || "")
    .split(/[\s_-]+/)
    .filter(Boolean)
    .map((part) => part.slice(0, 1).toUpperCase() + part.slice(1))
    .join(" ");
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}
