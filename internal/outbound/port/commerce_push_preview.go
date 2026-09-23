package port

import (
	"context"
	"encoding/json"
)

// CommercePushLegacyPreviewer renders the existing protocol with synthetic
// data; it never submits an effect or reads a real customer's identity.
type CommercePushLegacyPreviewer interface {
	PreviewLegacyCommercePushWithin(context.Context, int64) (json.RawMessage, error)
}
