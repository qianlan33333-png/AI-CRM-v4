package port

import (
	"context"
	"strings"
	"time"
)

// EffectiveAcquisitionState is shared by the Provider request and the
// completion-side attribution binding. WeCom limits contact-way state to 30
// characters; the 27 hex characters retain 108 bits of asset identity.
func EffectiveAcquisitionState(configured, sourceDigest string) string {
	if configured != "" {
		return configured
	}
	hex := strings.TrimPrefix(sourceDigest, "sha256:")
	if len(hex) < 27 {
		return ""
	}
	return "ca-" + hex[:27]
}

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
