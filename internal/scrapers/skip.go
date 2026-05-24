package scrapers

import (
	"errors"
	"fmt"
)

const (
	StatusEndpointNeeded        = "endpoint_needed"
	StatusRequiresSearchInput   = "requires_search_input"
	StatusLoginOrAuthorizedOnly = "login_or_authorized_only"
	StatusNotPublicBulk         = "not_public_bulk"
)

type SkipError struct {
	Status   string
	SourceID string
	Reason   string
}

func (e *SkipError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("skip %s", e.SourceID)
	}
	return fmt.Sprintf("skip %s: %s", e.SourceID, e.Reason)
}

func NewSkipError(status, sourceID, reason string) error {
	return &SkipError{Status: status, SourceID: sourceID, Reason: reason}
}

func AsSkipError(err error) (*SkipError, bool) {
	var skip *SkipError
	if errors.As(err, &skip) {
		return skip, true
	}
	return nil, false
}
