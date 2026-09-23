package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// commercePushTargetConfig is the protected deployment representation for an
// opaque Product target reference. Neither Product's database records nor its
// HTTP response can disclose endpoint or signing material.
type commercePushTargetConfig struct {
	Slot             string                        `json:"slot"`
	Endpoint         string                        `json:"endpoint"`
	SigningKey       string                        `json:"signing_key"`
	Version          string                        `json:"version"`
	TenantID         string                        `json:"tenant_id"`
	BuyerID          outbound.CommercePushIdentity `json:"buyer_id"`
	BuyerOpenID      outbound.CommercePushIdentity `json:"buyer_openid"`
	BuyerUnionID     outbound.CommercePushIdentity `json:"buyer_unionid"`
	BuyerPhone       outbound.CommercePushIdentity `json:"buyer_phone"`
	BeneficiaryPhone outbound.CommercePushIdentity `json:"beneficiary_phone"`
}

// commercePushTargets is the composition-owned whitelist. It returns copies
// so one request cannot mutate the policy later reused by another event.
type commercePushTargets struct {
	enabled bool
	values  map[string]outbound.CommercePushTarget
}

func (c commercePushTargets) CommercePushProviderEnabled() bool { return c.enabled }

func (c commercePushTargets) CommercePushTarget(_ context.Context, reference string) (outbound.CommercePushTarget, bool, error) {
	if strings.TrimSpace(reference) != reference || reference == "" {
		return outbound.CommercePushTarget{}, false, errors.New("invalid commerce push target reference")
	}
	value, found := c.values[reference]
	if !found {
		return outbound.CommercePushTarget{}, false, nil
	}
	value.SigningKey = append([]byte(nil), value.SigningKey...)
	return value, true, nil
}

func commercePushTargetsFromRuntime(value platformconfig.CommercePush) (commercePushTargets, error) {
	out := commercePushTargets{enabled: value.ProviderEnabled, values: map[string]outbound.CommercePushTarget{}}
	if value.TargetsJSON == "" {
		return out, nil
	}
	var configured map[string]commercePushTargetConfig
	if err := json.Unmarshal([]byte(value.TargetsJSON), &configured); err != nil || len(configured) == 0 || len(configured) > 100 {
		return commercePushTargets{}, errors.New("invalid commerce push target configuration")
	}
	for reference, source := range configured {
		if strings.TrimSpace(reference) != reference || reference == "" || len(reference) > 128 {
			return commercePushTargets{}, errors.New("invalid commerce push target reference")
		}
		var signingKey []byte
		if source.SigningKey != "" {
			decoded, err := base64.RawStdEncoding.DecodeString(source.SigningKey)
			if err != nil || len(decoded) > 4096 {
				return commercePushTargets{}, errors.New("invalid commerce push signing key")
			}
			signingKey = decoded
		}
		target := outbound.CommercePushTarget{
			Reference: reference, Slot: source.Slot, Endpoint: source.Endpoint, SigningKey: signingKey,
			Version: source.Version, TenantID: source.TenantID, BuyerID: source.BuyerID,
			BuyerOpenID: source.BuyerOpenID, BuyerUnionID: source.BuyerUnionID, BuyerPhone: source.BuyerPhone,
			BeneficiaryPhone: source.BeneficiaryPhone,
		}
		if err := outbound.ValidateCommercePushTarget(target); err != nil {
			return commercePushTargets{}, errors.New("invalid commerce push target configuration")
		}
		out.values[reference] = target
	}
	return out, nil
}

var _ outbound.CommercePushTargetResolver = commercePushTargets{}

// commerceProductConfigurationReader is the only Product read exposed to the
// Order-event consumer. It preserves the caller's PostgreSQL transaction and
// avoids exposing Product's concrete store outside the composition root.
type commerceProductConfigurationReader struct {
	reader interface {
		ReadCommerceExternalPushConfigurationForOrder(context.Context, productport.ID) (productport.ExternalPushConfiguration, error)
	}
}

func (c commerceProductConfigurationReader) ReadExternalPushConfigurationForOrder(ctx context.Context, id productport.ID) (productport.ExternalPushConfiguration, error) {
	if c.reader == nil {
		return productport.ExternalPushConfiguration{}, errors.New("product external push reader is unavailable")
	}
	return c.reader.ReadCommerceExternalPushConfigurationForOrder(ctx, id)
}

var _ productport.ExternalPushConfigurationReader = commerceProductConfigurationReader{}
