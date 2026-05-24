.PHONY: test run try-all map map-export package-windows sqlite-import sqlite-run fmt clean

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go')

run:
	go run ./cmd/permit-scraper --sources configs/sources.json --db data/permits-db --all --limit 25 --max-pages 1

try-all:
	go run ./cmd/permit-scraper --sources configs/sources.json --db data/permits-db --try-all --limit 10 --max-pages 1

map:
	go run ./cmd/permit-map --db data/permits-db --web web --addr 127.0.0.1:8080

map-export:
	go run ./cmd/permit-map-export --db data/permits-db --web web --out dist/permit-map

package-windows:
	powershell -ExecutionPolicy Bypass -File scripts/package-windows.ps1

sqlite-import:
	go run ./cmd/permit-db import-jsonl --jsonl data/permits-db --sqlite data/permits.sqlite --reset

sqlite-run:
	go run ./cmd/permit-scraper --sources configs/sources.json --store sqlite --db data/permits.sqlite --all --limit 25 --max-pages 1

clean:
	rm -rf data/permits-db data/permits.sqlite dist/permit-map dist/portable dist/portable.zip
