package port

import (
	"context"
	"time"
)

// ReleaseObservation is a local observation of the artifact currently being
// served. It never means that deployment, cutover, or an external effect was
// executed.
type ReleaseObservation struct {
	ReleaseSHA string
	Status     string
	ObservedAt time.Time
}

// ObservationWriter is the narrow seam used by the release app to persist a
// local release fact. The owning persistence adapter is selected at the
// Composition Root; the release plane never imports another domain's store.
type ObservationWriter interface {
	RecordReleaseObservation(context.Context, ReleaseObservation) error
}
