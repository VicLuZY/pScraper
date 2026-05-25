package storage

import "github.com/example/bc-permit-scraper/internal/model"

type Store interface {
	Upsert(model.PermitRecord) (UpsertResult, error)
	AddAudit(model.ScrapeAudit) error
}

type UpsertCounts struct {
	Inserted  int
	Updated   int
	Unchanged int
}

type BulkStore interface {
	UpsertMany([]model.PermitRecord) (UpsertCounts, error)
}
