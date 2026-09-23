package port

import (
	"context"
	"time"
)

// PublishedConfig is the immutable, versioned material Outbound may read
// after EER has committed an attempt. It contains staff provider references
// but never a customer external identifier.
type PublishedConfig struct {
	AssetID, ChannelID, ConfigVersion, AssetVersion int64
	Kind, Operation, TargetProviderAssetRef         string
	ChannelCode, ChannelName, StateValue            string
	SkipVerify                                      bool
	StaffProviderRefs                               []string
}

type PublishedConfigReader interface {
	ReadPublishedConfig(context.Context, string) (PublishedConfig, error)
}

type AssetCompletion struct {
	EffectRef, State, ProviderAssetRef, ResultURL, ResultDigest string
	Attempt                                                     int32
	CompletedAt                                                 time.Time
}

type AssetCompletionWriter interface {
	CompletePublishedAsset(context.Context, AssetCompletion) error
}
