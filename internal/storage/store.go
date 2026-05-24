package storage

import "github.com/example/bc-permit-scraper/internal/model"

type Store interface {
	Upsert(model.PermitRecord) (UpsertResult, error)
	AddAudit(model.ScrapeAudit) error
}
