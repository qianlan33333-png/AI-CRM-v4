package main

import (
	"context"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type referralPosterMediaReader struct {
	media mediaport.EnabledImageVariantReader
}

func (r referralPosterMediaReader) ReadEnabledPosterImage(ctx context.Context, id int64) (referralport.CampaignPosterImage, error) {
	variant, err := r.media.GetEnabledImageVariant(ctx, id, "original")
	if err != nil {
		return referralport.CampaignPosterImage{}, err
	}
	return referralport.CampaignPosterImage{Content: variant.Content, MediaType: variant.MediaType}, nil
}
