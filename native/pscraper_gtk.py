#!/usr/bin/env python3
"""Native GTK progress console for the Vancouver POSSE scraper."""

from __future__ import annotations

import argparse
import json
import math
import os
import queue
import sqlite3
import subprocess
import sys
import threading
import time
from collections import deque
from dataclasses import dataclass
from datetime import date
from pathlib import Path
from typing import Any

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
gi.require_version("Pango", "1.0")
from gi.repository import Gdk, Gio, GLib, Gtk, Pango  # noqa: E402


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_DB_DIR = ROOT / "data" / "permits-db"
SOURCE_ID = "vancouver_public_permit_search"
CONFIG_DIR = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config")) / "pScraper"
SETTINGS_FILE = CONFIG_DIR / "native-gui.json"
DOT_COLORS = {
    "not_processed": "#c9d1d5",
    "scraped": "#287a3e",
    "scraping": "#c89116",
    "error": "#b93636",
}


def load_settings() -> dict[str, Any]:
    try:
        return json.loads(SETTINGS_FILE.read_text())
    except (OSError, json.JSONDecodeError):
        return {}


def save_settings(settings: dict[str, Any]) -> None:
    CONFIG_DIR.mkdir(parents=True, exist_ok=True)
    SETTINGS_FILE.write_text(json.dumps(settings, indent=2) + "\n")


@dataclass
class ProgressSnapshot:
    index_total: int = 0
    current_records: int = 0
    scraped: int = 0
    scraping: int = 0
    errors: int = 0
    not_processed: int = 0
    remaining: int = 0
    percent: float = 0.0
    start_date: str = ""
    end_date: str = ""
    min_applied: str = ""
    max_applied: str = ""
    index_db: str = ""
    permit_db: str = ""
    days: list[dict[str, Any]] | None = None

    def as_dict(self) -> dict[str, Any]:
        return {
            "index_total": self.index_total,
            "current_records": self.current_records,
            "scraped": self.scraped,
            "scraping": self.scraping,
            "errors": self.errors,
            "not_processed": self.not_processed,
            "remaining": self.remaining,
            "percent": self.percent,
            "start_date": self.start_date,
            "end_date": self.end_date,
            "min_applied": self.min_applied,
            "max_applied": self.max_applied,
            "index_db": self.index_db,
            "permit_db": self.permit_db,
            "days": self.days or [],
        }


def sqlite_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def table_exists(conn: sqlite3.Connection, schema: str, table: str) -> bool:
    if schema not in {"main", "permitdb"}:
        raise ValueError(f"unsupported SQLite schema {schema!r}")
    row = conn.execute(
        f"SELECT COUNT(*) FROM {schema}.sqlite_master WHERE type = 'table' AND name = ?",
        (table,),
    ).fetchone()
    return bool(row and row[0])


def ensure_index_schema(conn: sqlite3.Connection) -> None:
    statements = [
        "ALTER TABLE vancouver_posse_index ADD COLUMN detail_status TEXT NOT NULL DEFAULT ''",
        "ALTER TABLE vancouver_posse_index ADD COLUMN detail_started_at TEXT NOT NULL DEFAULT ''",
        "ALTER TABLE vancouver_posse_index ADD COLUMN detail_finished_at TEXT NOT NULL DEFAULT ''",
        "ALTER TABLE vancouver_posse_index ADD COLUMN detail_error TEXT NOT NULL DEFAULT ''",
        "CREATE INDEX IF NOT EXISTS idx_vancouver_posse_detail_status ON vancouver_posse_index(detail_status)",
        "CREATE INDEX IF NOT EXISTS idx_vancouver_posse_detail_url ON vancouver_posse_index(detail_url)",
        "CREATE INDEX IF NOT EXISTS idx_vancouver_posse_created_status ON vancouver_posse_index(created_date, detail_status)",
    ]
    for stmt in statements:
        try:
            conn.execute(stmt)
        except sqlite3.OperationalError as exc:
            if "duplicate column" not in str(exc).lower():
                raise
    conn.commit()


def attach_permit_db(conn: sqlite3.Connection, permit_db: Path) -> bool:
    if not permit_db.exists():
        return False
    conn.execute(f"ATTACH DATABASE {sqlite_literal(str(permit_db))} AS permitdb")
    if not table_exists(conn, "permitdb", "permit_current"):
        return False
    conn.execute(
        "CREATE INDEX IF NOT EXISTS permitdb.idx_permit_current_source_url "
        "ON permit_current(source_id, url)"
    )
    conn.commit()
    return True


def reconcile_scraped_details(conn: sqlite3.Connection) -> None:
    conn.execute(
        """
        UPDATE vancouver_posse_index
        SET detail_status = 'scraped',
            detail_finished_at = CASE
                WHEN detail_finished_at IS NULL OR detail_finished_at = '' THEN datetime('now')
                ELSE detail_finished_at
            END
        WHERE detail_status = ''
          AND detail_url IN (
            SELECT url
            FROM permitdb.permit_current
            WHERE source_id = ?
              AND url IS NOT NULL
              AND url != ''
          )
        """,
        (SOURCE_ID,),
    )
    conn.commit()


def load_vancouver_progress(db_dir: Path) -> ProgressSnapshot:
    db_dir = db_dir.expanduser().resolve()
    index_db = db_dir / "vancouver_posse_index.sqlite"
    permit_db = db_dir / "permits.sqlite"
    snapshot = ProgressSnapshot(
        index_db=str(index_db),
        permit_db=str(permit_db),
        end_date=date.today().isoformat(),
        days=[],
    )
    if not index_db.exists():
        return snapshot

    conn = sqlite3.connect(index_db, timeout=8)
    conn.row_factory = sqlite3.Row
    try:
        conn.execute("PRAGMA busy_timeout = 8000")
        if not table_exists(conn, "main", "vancouver_posse_index"):
            return snapshot
        ensure_index_schema(conn)

        row = conn.execute(
            """
            SELECT
                COUNT(*) AS total,
                COALESCE(MIN(NULLIF(created_date, '')), '') AS start_date,
                COALESCE(MAX(NULLIF(created_date, '')), '') AS max_applied
            FROM vancouver_posse_index
            """
        ).fetchone()
        snapshot.index_total = int(row["total"] or 0)
        snapshot.start_date = row["start_date"] or ""
        snapshot.min_applied = snapshot.start_date
        snapshot.max_applied = row["max_applied"] or ""

        if attach_permit_db(conn, permit_db):
            reconcile_scraped_details(conn)
            row = conn.execute(
                """
                SELECT
                    COUNT(*) AS current_records,
                    COALESCE(MIN(NULLIF(applied_date, '')), '') AS min_applied,
                    COALESCE(MAX(NULLIF(applied_date, '')), '') AS max_applied
                FROM permitdb.permit_current
                WHERE source_id = ?
                """,
                (SOURCE_ID,),
            ).fetchone()
            snapshot.current_records = int(row["current_records"] or 0)
            snapshot.min_applied = row["min_applied"] or snapshot.min_applied
            snapshot.max_applied = row["max_applied"] or snapshot.max_applied

        rows = conn.execute(
            """
            SELECT
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
            ORDER BY created_date, detail_state
            """
        ).fetchall()
    finally:
        conn.close()

    days: list[dict[str, Any]] = []
    by_date: dict[str, dict[str, Any]] = {}
    for row in rows:
        day = by_date.get(row["created_date"])
        if day is None:
            day = {
                "date": row["created_date"],
                "total": 0,
                "not_processed": 0,
                "scraped": 0,
                "scraping": 0,
                "error": 0,
            }
            by_date[row["created_date"]] = day
            days.append(day)
        count = int(row["record_count"] or 0)
        day["total"] += count
        state = row["detail_state"]
        if state == "error":
            day["error"] += count
        elif state in {"scraped", "scraping", "not_processed"}:
            day[state] += count
        else:
            day["not_processed"] += count

    snapshot.days = days
    for day in days:
        snapshot.scraped += int(day["scraped"])
        snapshot.scraping += int(day["scraping"])
        snapshot.errors += int(day["error"])
        snapshot.not_processed += int(day["not_processed"])
    if snapshot.index_total and snapshot.scraped + snapshot.scraping + snapshot.errors + snapshot.not_processed < snapshot.index_total:
        snapshot.not_processed = snapshot.index_total - snapshot.scraped - snapshot.scraping - snapshot.errors
    snapshot.remaining = max(0, snapshot.index_total - snapshot.scraped)
    if snapshot.index_total:
        snapshot.percent = min(100.0, snapshot.scraped / snapshot.index_total * 100)
    return snapshot


def reset_detail_statuses(db_dir: Path, statuses: list[str], clear_error: bool = True) -> int:
    index_db = db_dir.expanduser().resolve() / "vancouver_posse_index.sqlite"
    if not index_db.exists():
        return 0
    conn = sqlite3.connect(index_db, timeout=8)
    try:
        conn.execute("PRAGMA busy_timeout = 8000")
        if not table_exists(conn, "main", "vancouver_posse_index"):
            return 0
        ensure_index_schema(conn)
        placeholders = ",".join("?" for _ in statuses)
        detail_error = "''" if clear_error else "detail_error"
        cur = conn.execute(
            f"""
            UPDATE vancouver_posse_index
            SET detail_status = '',
                detail_started_at = '',
                detail_finished_at = '',
                detail_error = {detail_error}
            WHERE detail_status IN ({placeholders})
            """,
            statuses,
        )
        conn.commit()
        return cur.rowcount if cur.rowcount is not None else 0
    finally:
        conn.close()


def reset_scraping_statuses(db_dir: Path) -> int:
    return reset_detail_statuses(db_dir, ["scraping"], clear_error=False)


def clear_error_statuses(db_dir: Path) -> int:
    return reset_detail_statuses(db_dir, ["error"], clear_error=True)


def fmt_int(value: int | float) -> str:
    return f"{int(value):,}"


def fmt_percent(value: float) -> str:
    if 0 < value < 10:
        return f"{value:.1f}%"
    return f"{value:.0f}%"


def fmt_duration(seconds: float | None) -> str:
    if seconds is None or seconds <= 0 or not math.isfinite(seconds):
        return "ETA unavailable"
    minutes = max(1, math.ceil(seconds / 60))
    days = minutes // 1440
    hours = (minutes % 1440) // 60
    mins = minutes % 60
    if days:
        return f"{days}d {hours}h remaining"
    if hours:
        return f"{hours}h {mins}m remaining"
    return f"{mins}m remaining"


def hex_to_rgb(value: str) -> tuple[float, float, float]:
    value = value.lstrip("#")
    return tuple(int(value[i : i + 2], 16) / 255 for i in (0, 2, 4))


def backend_candidates(root: Path) -> list[Path]:
    name = "pScraper.exe" if sys.platform == "win32" else "pScraper"
    return [
        root / "dist" / "native-backend" / name,
        root / "dist" / name,
    ]


def find_backend(root: Path) -> Path:
    for candidate in backend_candidates(root):
        if candidate.exists() and os.access(candidate, os.X_OK):
            return candidate
    raise FileNotFoundError("Go scraper binary not found. Run scripts/build-native-app.sh first.")


class VancouverMatrix(Gtk.DrawingArea):
    def __init__(self) -> None:
        super().__init__()
        self.snapshot = ProgressSnapshot(days=[])
        self.set_content_width(1000)
        self.set_content_height(140)
        self.set_draw_func(self.draw)

    def update_snapshot(self, snapshot: ProgressSnapshot) -> None:
        self.snapshot = snapshot
        self.update_size()
        self.queue_draw()

    def update_size(self) -> None:
        width = max(600, self.get_allocated_width() or 1000)
        total = max(0, self.snapshot.index_total)
        step = 3
        columns = max(1, (width - 1) // step)
        rows = max(1, math.ceil(total / columns))
        self.set_content_width(width)
        self.set_content_height(max(120, rows * step + 2))

    def draw(self, _area: Gtk.DrawingArea, cr: Any, width: int, _height: int) -> None:
        cr.set_source_rgb(1, 1, 1)
        cr.paint()
        total = self.snapshot.index_total
        if total <= 0:
            return

        dot = 2
        gap = 1
        step = dot + gap
        columns = max(1, (max(width, 1) - gap) // step)
        index = 0

        def draw_dots(count: int, color: str) -> None:
            nonlocal index
            if count <= 0:
                return
            cr.set_source_rgb(*hex_to_rgb(color))
            for _ in range(count):
                col = index % columns
                row = index // columns
                cr.rectangle(col * step + gap, row * step + gap, dot, dot)
                index += 1
            cr.fill()

        for day in self.snapshot.days or []:
            draw_dots(int(day.get("not_processed", 0)), DOT_COLORS["not_processed"])
            draw_dots(int(day.get("scraped", 0)), DOT_COLORS["scraped"])
            draw_dots(int(day.get("scraping", 0)), DOT_COLORS["scraping"])
            draw_dots(int(day.get("error", 0)), DOT_COLORS["error"])
        if index < total:
            draw_dots(total - index, DOT_COLORS["not_processed"])


class MainWindow(Gtk.ApplicationWindow):
    def __init__(self, app: Gtk.Application, db_dir: Path, root: Path) -> None:
        super().__init__(application=app, title="pScraper")
        self.root_dir = root
        self.settings = load_settings()
        self.db_dir = Path(self.settings.get("db_dir") or db_dir).expanduser().resolve()
        self.samples: deque[tuple[float, int]] = deque()
        self.refresh_running = False
        self.scrape_process: subprocess.Popen[str] | None = None
        self.active_command = ""
        self.log_queue: queue.Queue[str] = queue.Queue()

        self.set_default_size(1240, 820)
        self.set_size_request(980, 680)
        self.build_ui()
        self.refresh_async()
        GLib.timeout_add_seconds(2, self.refresh_async)
        GLib.timeout_add(150, self.drain_logs)

    def build_ui(self) -> None:
        shell = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
        shell.add_css_class("app-shell")
        self.set_child(shell)

        topbar = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=16)
        topbar.add_css_class("topbar")
        shell.append(topbar)

        title_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=2)
        title_box.set_hexpand(True)
        topbar.append(title_box)
        title = Gtk.Label(label="pScraper Native", xalign=0)
        title.add_css_class("title")
        title_box.append(title)
        self.range_label = Gtk.Label(label="Loading Vancouver index", xalign=0)
        self.range_label.add_css_class("muted")
        self.range_label.set_ellipsize(Pango.EllipsizeMode.END)
        title_box.append(self.range_label)

        self.refresh_button = Gtk.Button(label="Refresh")
        self.refresh_button.connect("clicked", lambda _button: self.refresh_async(force=True))
        topbar.append(self.refresh_button)

        self.link_button = Gtk.Button(label="Link Database")
        self.link_button.connect("clicked", self.on_link_database_clicked)
        topbar.append(self.link_button)

        self.open_button = Gtk.Button(label="Open Folder")
        self.open_button.connect("clicked", self.on_open_data_clicked)
        topbar.append(self.open_button)

        body = Gtk.Paned(orientation=Gtk.Orientation.VERTICAL)
        body.set_resize_start_child(True)
        shell.append(body)

        upper = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=10)
        upper.set_margin_top(14)
        upper.set_margin_bottom(12)
        upper.set_margin_start(16)
        upper.set_margin_end(16)
        body.set_start_child(upper)

        stats = Gtk.Grid(column_spacing=24, row_spacing=8)
        stats.add_css_class("stats")
        upper.append(stats)
        self.percent_value = self.stat(stats, "Progress", "0%", 0)
        self.scraped_value = self.stat(stats, "Scraped", "0 / 0", 1)
        self.remaining_value = self.stat(stats, "Remaining", "0", 2)
        self.scraping_value = self.stat(stats, "Scraping", "0", 3)
        self.error_value = self.stat(stats, "Errors", "0", 4)
        self.eta_value = self.stat(stats, "Remaining Time", "ETA unavailable", 5)
        self.pid_value = self.stat(stats, "PID", "-", 6)

        controls = Gtk.Grid(column_spacing=10, row_spacing=8)
        controls.add_css_class("controls")
        upper.append(controls)
        self.from_entry = self.entry("2000-01-01", 110)
        self.to_entry = self.entry(date.today().isoformat(), 110)
        self.index_workers_spin = Gtk.SpinButton.new_with_range(1, 64, 1)
        self.index_workers_spin.set_value(int(self.settings.get("index_workers", 8)))
        self.detail_workers_spin = Gtk.SpinButton.new_with_range(1, 64, 1)
        self.detail_workers_spin.set_value(int(self.settings.get("detail_workers", 8)))
        self.delay_spin = Gtk.SpinButton.new_with_range(0, 5000, 50)
        self.delay_spin.set_value(int(self.settings.get("delay_ms", 200)))
        self.timeout_spin = Gtk.SpinButton.new_with_range(5, 300, 5)
        self.timeout_spin.set_value(int(self.settings.get("timeout", 60)))
        self.limit_spin = Gtk.SpinButton.new_with_range(0, 10_000_000, 100)
        self.limit_spin.set_value(int(self.settings.get("limit", 0)))
        self.detail_scope_combo = Gtk.ComboBoxText()
        for value, label in [
            ("pending,error", "Resume pending/errors"),
            ("pending", "Pending only"),
            ("error", "Errors only"),
            ("all", "All indexed IDs"),
            ("scraped", "Scraped only"),
        ]:
            self.detail_scope_combo.append(value, label)
        self.detail_scope_combo.set_active_id(self.settings.get("detail_scope", "pending,error"))

        for column, (label, widget) in enumerate(
            [
                ("From", self.from_entry),
                ("To", self.to_entry),
                ("Index workers", self.index_workers_spin),
                ("Detail workers", self.detail_workers_spin),
                ("Delay ms", self.delay_spin),
                ("Timeout s", self.timeout_spin),
                ("Limit", self.limit_spin),
                ("Detail scope", self.detail_scope_combo),
            ]
        ):
            controls.attach(self.control_group(label, widget), column % 4, column // 4, 1, 1)

        command_row = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=8)
        command_row.add_css_class("command-row")
        upper.append(command_row)
        self.discover_button = Gtk.Button(label="Discover IDs")
        self.discover_button.connect("clicked", self.on_discover_clicked)
        command_row.append(self.discover_button)
        self.detail_start_button = Gtk.Button(label="Start Details")
        self.detail_start_button.add_css_class("primary")
        self.detail_start_button.connect("clicked", self.on_start_details_clicked)
        command_row.append(self.detail_start_button)
        self.resume_button = Gtk.Button(label="Resume Details")
        self.resume_button.connect("clicked", self.on_resume_clicked)
        command_row.append(self.resume_button)
        self.retry_button = Gtk.Button(label="Retry Errors")
        self.retry_button.connect("clicked", self.on_retry_errors_clicked)
        command_row.append(self.retry_button)
        self.pause_button = Gtk.Button(label="Pause")
        self.pause_button.set_sensitive(False)
        self.pause_button.connect("clicked", self.on_stop_clicked)
        command_row.append(self.pause_button)
        self.reset_scraping_button = Gtk.Button(label="Reset Yellow")
        self.reset_scraping_button.connect("clicked", self.on_reset_scraping_clicked)
        command_row.append(self.reset_scraping_button)
        self.clear_errors_button = Gtk.Button(label="Clear Errors")
        self.clear_errors_button.connect("clicked", self.on_clear_errors_clicked)
        command_row.append(self.clear_errors_button)

        self.command_status = Gtk.Label(label="Idle", xalign=0)
        self.command_status.add_css_class("muted")
        self.command_status.set_ellipsize(Pango.EllipsizeMode.END)
        upper.append(self.command_status)

        legend = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=16)
        legend.add_css_class("legend")
        upper.append(legend)
        for key, label in [
            ("not_processed", "Not processed"),
            ("scraped", "Scraped"),
            ("scraping", "Scraping"),
            ("error", "Error"),
        ]:
            legend.append(self.legend_item(key, label))

        matrix_scroller = Gtk.ScrolledWindow()
        matrix_scroller.add_css_class("matrix-frame")
        matrix_scroller.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
        matrix_scroller.set_min_content_height(260)
        self.matrix = VancouverMatrix()
        matrix_scroller.set_child(self.matrix)
        upper.append(matrix_scroller)

        lower = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        lower.set_margin_top(4)
        lower.set_margin_bottom(12)
        lower.set_margin_start(16)
        lower.set_margin_end(16)
        body.set_end_child(lower)
        body.set_position(560)

        db_row = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=10)
        lower.append(db_row)
        self.db_label = Gtk.Label(label=str(self.db_dir), xalign=0)
        self.db_label.add_css_class("muted")
        self.db_label.set_hexpand(True)
        self.db_label.set_ellipsize(Pango.EllipsizeMode.MIDDLE)
        db_row.append(self.db_label)
        open_button = Gtk.Button(label="Open Data Folder")
        open_button.connect("clicked", self.on_open_data_clicked)
        db_row.append(open_button)

        log_label = Gtk.Label(label="Scrape Log", xalign=0)
        log_label.add_css_class("section-label")
        lower.append(log_label)
        log_scroller = Gtk.ScrolledWindow()
        log_scroller.add_css_class("log-frame")
        log_scroller.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
        lower.append(log_scroller)
        self.log_view = Gtk.TextView()
        self.log_view.set_editable(False)
        self.log_view.set_monospace(True)
        self.log_view.set_wrap_mode(Gtk.WrapMode.WORD_CHAR)
        log_scroller.set_child(self.log_view)

    def stat(self, grid: Gtk.Grid, label: str, value: str, column: int) -> Gtk.Label:
        label_widget = Gtk.Label(label=label, xalign=0)
        label_widget.add_css_class("stat-label")
        value_widget = Gtk.Label(label=value, xalign=0)
        value_widget.add_css_class("stat-value")
        value_widget.set_ellipsize(Pango.EllipsizeMode.END)
        grid.attach(label_widget, column, 0, 1, 1)
        grid.attach(value_widget, column, 1, 1, 1)
        return value_widget

    def entry(self, text: str, width: int) -> Gtk.Entry:
        entry = Gtk.Entry()
        entry.set_text(text)
        entry.set_width_chars(width // 10)
        return entry

    def control_group(self, label: str, widget: Gtk.Widget) -> Gtk.Box:
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=4)
        label_widget = Gtk.Label(label=label, xalign=0)
        label_widget.add_css_class("field-label")
        box.append(label_widget)
        box.append(widget)
        return box

    def legend_item(self, key: str, label: str) -> Gtk.Box:
        item = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=6)
        dot = Gtk.DrawingArea()
        dot.set_content_width(10)
        dot.set_content_height(10)
        color = DOT_COLORS[key]
        dot.set_draw_func(lambda _area, cr, _w, _h, c=color: self.draw_legend_dot(cr, c))
        item.append(dot)
        text = Gtk.Label(label=label)
        text.add_css_class("muted")
        item.append(text)
        return item

    def draw_legend_dot(self, cr: Any, color: str) -> None:
        cr.set_source_rgb(*hex_to_rgb(color))
        cr.arc(5, 5, 4.5, 0, 2 * math.pi)
        cr.fill()

    def refresh_async(self, force: bool = False) -> bool:
        if self.refresh_running and not force:
            return True
        if force:
            self.samples.clear()
        self.refresh_running = True
        threading.Thread(target=self.load_progress_thread, daemon=True).start()
        return True

    def load_progress_thread(self) -> None:
        try:
            snapshot = load_vancouver_progress(self.db_dir)
            GLib.idle_add(self.apply_progress, snapshot, None)
        except Exception as exc:  # noqa: BLE001
            GLib.idle_add(self.apply_progress, None, exc)

    def apply_progress(self, snapshot: ProgressSnapshot | None, error: Exception | None) -> bool:
        self.refresh_running = False
        if error is not None:
            self.range_label.set_text(f"Progress unavailable: {error}")
            return False
        if snapshot is None:
            return False

        now = time.monotonic()
        self.samples.append((now, snapshot.scraped))
        while self.samples and now - self.samples[0][0] > 300:
            self.samples.popleft()
        rate = self.records_per_second()
        eta_seconds = snapshot.remaining / rate if rate > 0 and snapshot.remaining else None

        self.percent_value.set_text(fmt_percent(snapshot.percent))
        self.scraped_value.set_text(f"{fmt_int(snapshot.scraped)} / {fmt_int(snapshot.index_total)}")
        self.remaining_value.set_text(fmt_int(snapshot.remaining))
        self.scraping_value.set_text(fmt_int(snapshot.scraping))
        self.error_value.set_text(fmt_int(snapshot.errors))
        self.eta_value.set_text(fmt_duration(eta_seconds))
        self.pid_value.set_text(str(self.scrape_process.pid) if self.scrape_process else "-")
        self.update_command_controls()
        self.db_label.set_text(f"{snapshot.permit_db}  |  {snapshot.index_db}")
        if snapshot.index_total:
            self.range_label.set_text(
                f"{snapshot.start_date or 'Undated'} to {snapshot.end_date} - "
                f"{fmt_int(snapshot.scraped)} of {fmt_int(snapshot.index_total)} scraped"
            )
        else:
            self.range_label.set_text("No Vancouver index loaded")
        self.matrix.update_snapshot(snapshot)
        return False

    def records_per_second(self) -> float:
        if len(self.samples) < 2:
            return 0
        start_time, start_value = self.samples[0]
        end_time, end_value = self.samples[-1]
        elapsed = end_time - start_time
        if elapsed <= 0:
            return 0
        return max(0, (end_value - start_value) / elapsed)

    def update_command_controls(self) -> None:
        running = self.scrape_process is not None
        for button in [
            self.discover_button,
            self.detail_start_button,
            self.resume_button,
            self.retry_button,
            self.reset_scraping_button,
            self.clear_errors_button,
            self.link_button,
        ]:
            button.set_sensitive(not running)
        self.pause_button.set_sensitive(running)
        if running:
            self.command_status.set_text(f"Running {self.active_command} PID {self.scrape_process.pid}")
        else:
            self.command_status.set_text("Idle")

    def save_current_settings(self) -> None:
        self.settings.update(
            {
                "db_dir": str(self.db_dir),
                "index_workers": int(self.index_workers_spin.get_value()),
                "detail_workers": int(self.detail_workers_spin.get_value()),
                "delay_ms": int(self.delay_spin.get_value()),
                "timeout": int(self.timeout_spin.get_value()),
                "limit": int(self.limit_spin.get_value()),
                "detail_scope": self.detail_scope_combo.get_active_id() or "pending,error",
            }
        )
        save_settings(self.settings)

    def common_scrape_args(self) -> list[str]:
        args = [
            "scrape",
            "--sources",
            "configs/sources.json",
            "--store",
            "sqlite",
            "--db",
            str(self.db_dir / "permits.sqlite"),
            "--source",
            SOURCE_ID,
            "--from",
            self.from_entry.get_text().strip() or "2000-01-01",
            "--to",
            self.to_entry.get_text().strip() or date.today().isoformat(),
            "--delay-ms",
            str(int(self.delay_spin.get_value())),
            "--timeout",
            str(int(self.timeout_spin.get_value())),
        ]
        limit = int(self.limit_spin.get_value())
        if limit > 0:
            args.extend(["--limit", str(limit)])
        return args

    def on_discover_clicked(self, _button: Gtk.Button) -> None:
        args = self.common_scrape_args()
        args.extend(
            [
                "--index-only",
                "--index-workers",
                str(int(self.index_workers_spin.get_value())),
            ]
        )
        self.start_command("index discovery", args)

    def on_start_details_clicked(self, _button: Gtk.Button) -> None:
        args = self.common_scrape_args()
        args.extend(
            [
                "--detail-only",
                "--detail-workers",
                str(int(self.detail_workers_spin.get_value())),
                "--detail-status",
                self.detail_scope_combo.get_active_id() or "all",
            ]
        )
        self.start_command("detail scrape", args)

    def on_resume_clicked(self, _button: Gtk.Button) -> None:
        args = self.common_scrape_args()
        args.extend(
            [
                "--detail-only",
                "--detail-workers",
                str(int(self.detail_workers_spin.get_value())),
                "--detail-status",
                "pending,error",
            ]
        )
        self.start_command("detail resume", args)

    def on_retry_errors_clicked(self, _button: Gtk.Button) -> None:
        args = self.common_scrape_args()
        args.extend(
            [
                "--detail-only",
                "--detail-workers",
                str(int(self.detail_workers_spin.get_value())),
                "--detail-status",
                "error",
            ]
        )
        self.start_command("error retry", args)

    def start_command(self, command_name: str, scrape_args: list[str]) -> None:
        if self.scrape_process is not None:
            return
        self.save_current_settings()
        try:
            backend = find_backend(self.root_dir)
        except FileNotFoundError as exc:
            self.append_log(f"ERR {exc}\n")
            return

        args = [str(backend), *scrape_args]
        try:
            self.scrape_process = subprocess.Popen(
                args,
                cwd=self.root_dir,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                bufsize=1,
            )
        except Exception as exc:  # noqa: BLE001
            self.append_log(f"ERR failed to start scraper: {exc}\n")
            self.scrape_process = None
            return

        self.active_command = command_name
        self.samples.clear()
        self.append_log(f"SYS started {command_name} PID {self.scrape_process.pid}\n")
        self.append_log(f"SYS {' '.join(args)}\n")
        self.update_command_controls()
        threading.Thread(target=self.pipe_reader, args=(self.scrape_process.stdout, "OUT"), daemon=True).start()
        threading.Thread(target=self.pipe_reader, args=(self.scrape_process.stderr, "ERR"), daemon=True).start()
        threading.Thread(target=self.watch_process, args=(self.scrape_process,), daemon=True).start()
        self.refresh_async()

    def on_stop_clicked(self, _button: Gtk.Button) -> None:
        proc = self.scrape_process
        if proc is None:
            return
        self.append_log(f"SYS pausing PID {proc.pid}\n")
        proc.terminate()
        threading.Thread(target=self.kill_if_needed, args=(proc,), daemon=True).start()

    def on_reset_scraping_clicked(self, _button: Gtk.Button) -> None:
        try:
            count = reset_scraping_statuses(self.db_dir)
        except Exception as exc:  # noqa: BLE001
            self.append_log(f"ERR reset yellow failed: {exc}\n")
            return
        self.append_log(f"SYS reset {fmt_int(count)} yellow scraping rows to pending\n")
        self.refresh_async(force=True)

    def on_clear_errors_clicked(self, _button: Gtk.Button) -> None:
        try:
            count = clear_error_statuses(self.db_dir)
        except Exception as exc:  # noqa: BLE001
            self.append_log(f"ERR clear errors failed: {exc}\n")
            return
        self.append_log(f"SYS cleared {fmt_int(count)} error rows to pending\n")
        self.refresh_async(force=True)

    def kill_if_needed(self, proc: subprocess.Popen[str]) -> None:
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.append_log(f"SYS killing PID {proc.pid}\n")
            proc.kill()

    def pipe_reader(self, stream: Any, prefix: str) -> None:
        if stream is None:
            return
        for line in stream:
            self.log_queue.put(f"{prefix} {line}")

    def watch_process(self, proc: subprocess.Popen[str]) -> None:
        code = proc.wait()
        GLib.idle_add(self.process_finished, proc, code)

    def process_finished(self, proc: subprocess.Popen[str], code: int) -> bool:
        if self.scrape_process is proc:
            self.scrape_process = None
            self.active_command = ""
        self.append_log(f"SYS finished PID {proc.pid} with code {code}\n")
        self.update_command_controls()
        self.refresh_async()
        return False

    def drain_logs(self) -> bool:
        drained = False
        while True:
            try:
                text = self.log_queue.get_nowait()
            except queue.Empty:
                break
            self.append_log(text)
            drained = True
        return True

    def append_log(self, text: str) -> None:
        buffer = self.log_view.get_buffer()
        end_iter = buffer.get_end_iter()
        buffer.insert(end_iter, text)
        mark = buffer.create_mark(None, buffer.get_end_iter(), False)
        self.log_view.scroll_to_mark(mark, 0.0, True, 0.0, 1.0)

    def on_link_database_clicked(self, _button: Gtk.Button) -> None:
        dialog = Gtk.FileChooserNative(
            title="Link Database Folder",
            transient_for=self,
            action=Gtk.FileChooserAction.SELECT_FOLDER,
            accept_label="Link",
            cancel_label="Cancel",
        )
        dialog.set_modal(True)
        current_folder = self.db_dir if self.db_dir.exists() else DEFAULT_DB_DIR
        dialog.set_current_folder(Gio.File.new_for_path(str(current_folder)))
        dialog.connect("response", self.on_database_dialog_response)
        dialog.show()

    def on_database_dialog_response(self, dialog: Gtk.FileChooserNative, response: int) -> None:
        if response == Gtk.ResponseType.ACCEPT:
            selected = dialog.get_file()
            path = selected.get_path() if selected else None
            if path:
                self.db_dir = Path(path).expanduser().resolve()
                self.settings["db_dir"] = str(self.db_dir)
                save_settings(self.settings)
                self.samples.clear()
                self.append_log(f"SYS linked database folder {self.db_dir}\n")
                self.refresh_async(force=True)
        dialog.destroy()

    def on_open_data_clicked(self, _button: Gtk.Button) -> None:
        self.db_dir.mkdir(parents=True, exist_ok=True)
        Gio.AppInfo.launch_default_for_uri(self.db_dir.as_uri(), None)


def install_css() -> None:
    provider = Gtk.CssProvider()
    provider.load_from_data(
        b"""
        window { background: #eef2f4; color: #172026; }
        .topbar {
          padding: 14px 16px;
          border-bottom: 1px solid #d7dee2;
          background: #fbfcfc;
        }
        .title {
          font-size: 20px;
          font-weight: 800;
        }
        .muted, .field-label, .stat-label {
          color: #62717b;
          font-size: 12px;
        }
        .stats {
          padding: 8px 0;
        }
        .stat-value {
          font-size: 22px;
          font-weight: 800;
        }
        .section-label {
          font-size: 13px;
          font-weight: 800;
        }
        .controls {
          padding: 4px 0;
        }
        .controls entry, .controls spinbutton, .controls combobox {
          min-height: 32px;
        }
        .command-row {
          padding: 2px 0 4px;
        }
        button {
          min-height: 34px;
          border-radius: 6px;
          padding: 5px 12px;
        }
        button.primary {
          background: #0f7b6c;
          color: white;
        }
        .matrix-frame, .log-frame {
          border: 1px solid #d7dee2;
          border-radius: 6px;
          background: white;
        }
        textview {
          background: #172026;
          color: #d9efea;
          font-size: 11px;
          padding: 8px;
        }
        """
    )
    display = Gdk.Display.get_default()
    if display is not None:
        Gtk.StyleContext.add_provider_for_display(display, provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)


def run_gui(args: argparse.Namespace) -> int:
    db_dir = Path(args.db_dir or os.environ.get("PSCRAPER_DB_DIR") or DEFAULT_DB_DIR)
    app = Gtk.Application(application_id="com.vicluzy.pscraper", flags=Gio.ApplicationFlags.DEFAULT_FLAGS)

    def activate(application: Gtk.Application) -> None:
        install_css()
        window = MainWindow(application, db_dir, ROOT)
        window.present()

    app.connect("activate", activate)
    return app.run([])


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Native GTK GUI for pScraper")
    parser.add_argument("--db-dir", default=str(DEFAULT_DB_DIR), help="database directory containing Vancouver SQLite files")
    parser.add_argument("--check-db", action="store_true", help="print Vancouver progress JSON and exit")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    if args.check_db:
        snapshot = load_vancouver_progress(Path(args.db_dir))
        print(json.dumps(snapshot.as_dict(), indent=2))
        return 0
    return run_gui(args)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
