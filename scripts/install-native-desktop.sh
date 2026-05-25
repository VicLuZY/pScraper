#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
"$ROOT/scripts/build-native-app.sh"

APP_DIR="$HOME/.local/share/applications"
mkdir -p "$APP_DIR"
cp "$ROOT/dist/native/pScraper.desktop" "$APP_DIR/pscraper-native.desktop"

if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "$APP_DIR" >/dev/null 2>&1 || true
fi

echo "Installed native launcher: $APP_DIR/pscraper-native.desktop"
