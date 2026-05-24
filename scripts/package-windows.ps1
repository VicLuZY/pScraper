param(
    [string]$OutFile = "dist\pScraper.exe",
    [string]$Go = "C:\Program Files\Go\bin\go.exe"
)

$ErrorActionPreference = "Stop"

$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $root

if (!(Test-Path $Go)) {
    throw "Go executable not found at $Go"
}

$distDir = [System.IO.Path]::GetFullPath((Join-Path $root "dist"))
$outPath = [System.IO.Path]::GetFullPath($OutFile)
if (-not $outPath.StartsWith($distDir, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to write outside dist: $outPath"
}

if (!(Test-Path $distDir)) {
    New-Item -ItemType Directory -Force -Path $distDir | Out-Null
}

foreach ($child in Get-ChildItem -LiteralPath $distDir -Force) {
    $childPath = [System.IO.Path]::GetFullPath($child.FullName)
    if (-not $childPath.StartsWith($distDir, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove outside dist: $childPath"
    }
    Remove-Item -LiteralPath $childPath -Recurse -Force
}

$bundleConfig = Join-Path $root "internal\bundle\assets\configs"
$bundleDB = Join-Path $root "internal\bundle\assets\data\permits-db"
New-Item -ItemType Directory -Force -Path $bundleConfig | Out-Null
New-Item -ItemType Directory -Force -Path $bundleDB | Out-Null
Copy-Item -LiteralPath "configs\sources.json" -Destination (Join-Path $bundleConfig "sources.json") -Force
foreach ($name in @("current.jsonl", "history.jsonl", "scrape_audit.jsonl", "scrape_progress.json")) {
    $src = Join-Path "data\permits-db" $name
    if (Test-Path $src) {
        Copy-Item -LiteralPath $src -Destination (Join-Path $bundleDB $name) -Force
    }
}

$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

& $Go build -trimpath -ldflags="-s -w" -o $outPath .\cmd\permit-scraper

Get-Item -LiteralPath $outPath | Select-Object Name, Length, LastWriteTime
Write-Host "Portable executable: $outPath"
