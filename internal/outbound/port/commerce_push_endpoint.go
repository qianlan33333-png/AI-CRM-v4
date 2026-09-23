package port

import (
	"context"
	"errors"
)

var ErrCommercePushEndpointInvalid = errors.New("invalid commerce push endpoint configuration")

// CommercePushEndpointManager participates in the caller's PostgreSQL unit of work.
// URLs are protected configuration and must not be included in logs or audit payloads.
type CommercePushEndpointManager interface {
	ReadCommercePushEndpointWithin(context.Context, string, int64, string) (string, error)
	SaveCommercePushEndpointWithin(context.Context, string, int64, string, string) (string, error)
}
