#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND="$ROOT/dist/native-backend/pScraper"

if [[ ! -x "$BACKEND" ]]; then
  "$ROOT/scripts/build-native-app.sh"
fi

exec python3 "$ROOT/native/pscraper_gtk.py" "$@"
