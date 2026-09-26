package store

import (
	"context"
	"crypto/sha256"
	"time"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type PublishedPoster struct {
	Slot                   int
	SourceImageID          int64
	Description, MediaType string
	Content                []byte
}

func (r *Repository) ListCampaignPostersWithin(ctx context.Context, campaignID int64) ([]PublishedPoster, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT slot,source_image_id,description,media_type FROM referral_campaign_posters WHERE campaign_id=$1 ORDER BY slot`, campaignID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]PublishedPoster, 0, 3)
	for rows.Next() {
		var poster PublishedPoster
		if err := rows.Scan(&poster.Slot, &poster.SourceImageID, &poster.Description, &poster.MediaType); err != nil {
			return nil, mapError(err)
		}
		result = append(result, poster)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (r *Repository) ReadCampaignPosterWithin(ctx context.Context, campaignID int64, slot int) (referralport.CampaignPosterImage, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referralport.CampaignPosterImage{}, err
	}
	if campaignID < 1 || slot < 1 || slot > 3 {
		return referralport.CampaignPosterImage{}, ErrInvalid
	}
	var image referralport.CampaignPosterImage
	err = tx.QueryRow(ctx, `SELECT image_bytes,media_type FROM referral_campaign_posters WHERE campaign_id=$1 AND slot=$2`, campaignID, slot).Scan(&image.Content, &image.MediaType)
	if err != nil {
		return referralport.CampaignPosterImage{}, mapError(err)
	}
	return image, nil
}

func (r *Repository) ReplaceCampaignPostersWithin(ctx context.Context, campaignID int64, posters []PublishedPoster, at time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if campaignID < 1 || len(posters) > 3 || at.IsZero() {
		return ErrInvalid
	}
	if _, err = tx.Exec(ctx, `DELETE FROM referral_campaign_posters WHERE campaign_id=$1`, campaignID); err != nil {
		return mapError(err)
	}
	for index, poster := range posters {
		if poster.Slot != index+1 || poster.SourceImageID < 1 || len(poster.Content) == 0 || len(poster.Content) > 5<<20 || len([]rune(poster.Description)) > 80 || (poster.MediaType != "image/png" && poster.MediaType != "image/jpeg") {
			return ErrInvalid
		}
		digest := sha256.Sum256(poster.Content)
		_, err = tx.Exec(ctx, `INSERT INTO referral_campaign_posters(campaign_id,slot,source_image_id,description,media_type,image_bytes,image_sha256,published_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, campaignID, poster.Slot, poster.SourceImageID, poster.Description, poster.MediaType, poster.Content, digest[:], at.UTC())
		if err != nil {
			return mapError(err)
		}
	}
	return nil
}
