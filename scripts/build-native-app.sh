#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$ROOT/dist/native-backend"
APP_DIR="$ROOT/dist/native"
EXE_NAME="pScraper"

mkdir -p "$OUT_DIR" "$APP_DIR"
go build -o "$OUT_DIR/$EXE_NAME" ./cmd/permit-scraper

cat > "$APP_DIR/pScraper-native" <<EOF
#!/usr/bin/env bash
exec python3 "$ROOT/native/pscraper_gtk.py" "\$@"
EOF
chmod +x "$APP_DIR/pScraper-native"

cat > "$APP_DIR/pScraper.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=pScraper
Comment=Native Ubuntu progress console for Vancouver permit scraping
Exec=$APP_DIR/pScraper-native
Icon=$ROOT/assets/app-icon.png
Terminal=false
Categories=Utility;
StartupNotify=true
EOF
chmod +x "$APP_DIR/pScraper.desktop"

echo "Built native Ubuntu app launcher: dist/native/pScraper-native"
echo "Desktop entry: dist/native/pScraper.desktop"
