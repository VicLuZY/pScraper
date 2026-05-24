param(
    [string]$OutDir = "dist\portable",
    [string]$Go = "C:\Program Files\Go\bin\go.exe"
)

$ErrorActionPreference = "Stop"

$root = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $root

if (!(Test-Path $Go)) {
    throw "Go executable not found at $Go"
}

if (Test-Path $OutDir) {
    Remove-Item -LiteralPath $OutDir -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $OutDir "configs") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $OutDir "data") | Out-Null

$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

& $Go build -trimpath -ldflags="-s -w" -o (Join-Path $OutDir "pScraper.exe") .\cmd\permit-scraper

Copy-Item -LiteralPath "configs\sources.json" -Destination (Join-Path $OutDir "configs\sources.json")
Copy-Item -LiteralPath "README.md" -Destination (Join-Path $OutDir "README.md")

@'
@echo off
setlocal
cd /d "%~dp0"
pScraper.exe scrape --sources configs\sources.json --db data\permits-db --all --limit 25 --max-pages 1 --parallel 4
pause
'@ | Set-Content -Path (Join-Path $OutDir "run-scraper.cmd") -Encoding ASCII

@'
@echo off
setlocal
cd /d "%~dp0"
pScraper.exe map --db data\permits-db --addr 127.0.0.1:8080
pause
'@ | Set-Content -Path (Join-Path $OutDir "run-map.cmd") -Encoding ASCII

@'
@echo off
setlocal
cd /d "%~dp0"
pScraper.exe export-map --db data\permits-db --out map-export
echo.
echo Static map exported to map-export\index.html
pause
'@ | Set-Content -Path (Join-Path $OutDir "export-static-map.cmd") -Encoding ASCII

@'
# BC Permit Scraper Portable Package

This folder contains one Windows executable. Go is not required on the target machine.

## First run

1. Run `run-scraper.cmd` to collect current records into `data\permits-db`.
2. Run `run-map.cmd` and open `http://127.0.0.1:8080/`.

The scraper, map server, static map export, and JSONL-to-SQLite import are all included in `pScraper.exe`. The map UI is embedded; there is no separate web folder to copy.

## No localhost map

Run `export-static-map.cmd`, then open `map-export\index.html` directly or publish the `map-export` folder to a static host.

## Direct commands

```cmd
pScraper.exe scrape --sources configs\sources.json --db data\permits-db --all --limit 25 --max-pages 1 --parallel 4
pScraper.exe map --db data\permits-db --addr 127.0.0.1:8080
pScraper.exe export-map --db data\permits-db --out map-export
pScraper.exe db import-jsonl --jsonl data\permits-db --sqlite data\permits.sqlite --reset
```

Login-only, access-code, CAPTCHA, and applicant-only sources remain audited skips unless an authorised export/API is added.
'@ | Set-Content -Path (Join-Path $OutDir "README-portable.md") -Encoding ASCII

if (Test-Path "data\permits-db") {
    Copy-Item -LiteralPath "data\permits-db" -Destination (Join-Path $OutDir "data\permits-db") -Recurse
}

$zip = "$OutDir.zip"
if (Test-Path $zip) {
    Remove-Item -LiteralPath $zip -Force
}
Compress-Archive -Path (Join-Path $OutDir "*") -DestinationPath $zip -Force

Get-ChildItem -LiteralPath $OutDir | Select-Object Name, Length, LastWriteTime
Write-Host "Portable package: $OutDir"
Write-Host "Zip package: $zip"
