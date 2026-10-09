package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type PublishedPoster struct {
	Slot                   int
	SourceImageID          int64
	Description, MediaType string
	ShareText              string
	Content                []byte
	Width, Height          int
	QRLayout               *referralport.PosterQRLayout
}

func (r *Repository) ListCampaignPostersWithin(ctx context.Context, campaignID int64) ([]PublishedPoster, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT slot,source_image_id,description,media_type,share_text,image_width,image_height,qr_layout FROM referral_campaign_posters WHERE campaign_id=$1 ORDER BY slot`, campaignID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]PublishedPoster, 0, 3)
	for rows.Next() {
		var poster PublishedPoster
		var layout []byte
		if err := rows.Scan(&poster.Slot, &poster.SourceImageID, &poster.Description, &poster.MediaType, &poster.ShareText, &poster.Width, &poster.Height, &layout); err != nil {
			return nil, mapError(err)
		}
		if len(layout) > 0 {
			if err := json.Unmarshal(layout, &poster.QRLayout); err != nil {
				return nil, ErrInvalid
			}
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
		var layout []byte
		if poster.QRLayout != nil {
			layout, err = json.Marshal(poster.QRLayout)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO referral_campaign_posters(campaign_id,slot,source_image_id,description,media_type,image_bytes,image_sha256,published_at,share_text,image_width,image_height,qr_layout) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, campaignID, poster.Slot, poster.SourceImageID, poster.Description, poster.MediaType, poster.Content, digest[:], at.UTC(), poster.ShareText, poster.Width, poster.Height, layout)
		if err != nil {
			return mapError(err)
		}
	}
	return nil
}

func (r *Repository) UpdateCampaignPosterLayoutWithin(ctx context.Context, campaignID int64, poster PublishedPoster) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if campaignID < 1 || poster.QRLayout == nil || !poster.QRLayout.Fits(poster.Width, poster.Height) {
		return ErrInvalid
	}
	layout, err := json.Marshal(poster.QRLayout)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE referral_campaign_posters SET qr_layout=$1,image_width=$2,image_height=$3 WHERE campaign_id=$4 AND slot=$5 AND source_image_id=$6`, layout, poster.Width, poster.Height, campaignID, poster.Slot, poster.SourceImageID)
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() != 1 {
		return referralport.ErrConflict
	}
	return nil
}
