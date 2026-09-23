// Package domain defines channel-acquisition values shared with callback
// orchestration. It deliberately does not contain a database representation.
package domain

import "errors"

var ErrInvalidStateResolution = errors.New("invalid channel state resolution")

// StateResolutionStatus is the only cardinality information callers receive
// after looking up a registered (corp_id, state_digest) binding. In
// particular, a caller must never turn ambiguous into attributed by selecting
// a latest asset.
type StateResolutionStatus string

const (
	StateAttributed StateResolutionStatus = "attributed"
	StateUnmatched  StateResolutionStatus = "unmatched"
	StateAmbiguous  StateResolutionStatus = "ambiguous"
)

// AcquisitionAsset is the immutable asset tuple recorded with a successful
// attribution. It is intentionally absent from unmatched and ambiguous
// resolutions so those outcomes cannot accidentally acquire a customer link.
type AcquisitionAsset struct {
	ChannelID    int64
	Kind         string
	AssetVersion int64
}

func (asset AcquisitionAsset) Valid() bool {
	return asset.ChannelID > 0 && asset.AssetVersion > 0 && (asset.Kind == "qrcode" || asset.Kind == "link")
}

type StateResolution struct {
	Status StateResolutionStatus
	Asset  AcquisitionAsset
}

func (resolution StateResolution) Valid() bool {
	switch resolution.Status {
	case StateAttributed:
		return resolution.Asset.Valid()
	case StateUnmatched, StateAmbiguous:
		return resolution.Asset == (AcquisitionAsset{})
	default:
		return false
	}
}
