package port

import (
	_ "embed"
	"encoding/json"
	"math"
)

// PosterQRLayout uses original image pixels, measured from the top-left.
type PosterQRLayout struct {
	X    int `json:"x"`
	Y    int `json:"y"`
	Size int `json:"size"`
}

func (l PosterQRLayout) Fits(width, height int) bool {
	return l.X >= 0 && l.Y >= 0 && l.Size > 0 && l.Size <= width && l.Size <= height && l.X <= width-l.Size && l.Y <= height-l.Size
}

type PosterTemplate struct {
	Width    int            `json:"width"`
	Height   int            `json:"height"`
	QRLayout PosterQRLayout `json:"qr_layout"`
}

//go:embed poster_template.json
var posterTemplateJSON []byte

var referencePosterTemplate = func() PosterTemplate {
	var value PosterTemplate
	if err := json.Unmarshal(posterTemplateJSON, &value); err != nil || !value.QRLayout.Fits(value.Width, value.Height) {
		panic("invalid embedded referral poster template")
	}
	return value
}()

func ReferencePosterTemplate() PosterTemplate { return referencePosterTemplate }

func DefaultPosterQRLayout(width, height int) PosterQRLayout {
	if width == referencePosterTemplate.Width && height == referencePosterTemplate.Height {
		return referencePosterTemplate.QRLayout
	}
	// Already-published legacy artwork keeps its existing composition geometry.
	size := int(math.Floor(math.Min(float64(width)*.25, float64(height)*.19) + .5))
	return PosterQRLayout{X: width - size - int(math.Floor(float64(width)*.06+.5)), Y: height - size - int(math.Floor(float64(height)*.06+.5)), Size: size}
}

type PosterLayoutUpdate struct {
	Slot int `json:"slot"`
	PosterQRLayout
}

type SetCampaignPosterLayoutsCommand struct {
	CampaignID, ExpectedVersion, ActorAdminID int64
	IdempotencyKey                            string
	Posters                                   []PosterLayoutUpdate
}

type CampaignPosterLayoutConfiguration struct {
	CampaignID, Version int64
	Posters             []CampaignPoster
}
