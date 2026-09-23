package app

import (
	"context"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

const (
	dailyLessonCardAppID = "wx0ca836834b18e989"
	dailyLessonCardHost  = "https://ip.lhbl.com.cn/api/share/lesson-card/"
)

var dailyLessonCardPath = regexp.MustCompile("^pages/article/article[?]lesson_id=([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})&from=learn$")

// WebhookMiniProgramMaterializerStore is the narrow Media-owned persistence
// seam used while Group Ops already owns an open Unit of Work.
type WebhookMiniProgramMaterializerStore interface {
	MaterializeWebhookMiniProgramWithin(context.Context, mediaport.PreparedWebhookMiniProgram, mediaport.WebhookMiniProgramMaterialization) (mediaport.GroupOpsMaterialReference, error)
}

// WebhookLessonCardResolver implements the only approved automatic card-cover
// source. It is intentionally not a general AppID/path thumbnail fetcher.
type WebhookLessonCardResolver struct {
	store  WebhookMiniProgramMaterializerStore
	client *http.Client
}

func NewWebhookLessonCardResolver(store WebhookMiniProgramMaterializerStore, transport http.RoundTripper) *WebhookLessonCardResolver {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &WebhookLessonCardResolver{
		store: store,
		client: &http.Client{
			Transport: transport,
			Timeout:   20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (resolver *WebhookLessonCardResolver) PrepareWebhookMiniProgram(ctx context.Context, request mediaport.WebhookMiniProgramRequest) (mediaport.PreparedWebhookMiniProgram, error) {
	lessonID, ok := strictDailyLessonCard(request)
	if !ok {
		return mediaport.PreparedWebhookMiniProgram{}, mediaport.ErrWebhookMiniProgramUnsupported
	}
	if resolver == nil || resolver.client == nil {
		return mediaport.PreparedWebhookMiniProgram{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, dailyLessonCardHost+lessonID+".png", nil)
	if err != nil {
		return mediaport.PreparedWebhookMiniProgram{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	response, err := resolver.client.Do(httpRequest)
	if err != nil || response == nil {
		return mediaport.PreparedWebhookMiniProgram{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > domain.MaxImageBytes {
		return mediaport.PreparedWebhookMiniProgram{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	mediaType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if parseErr != nil || !strings.EqualFold(mediaType, "image/png") {
		return mediaport.PreparedWebhookMiniProgram{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	content, readErr := domain.ReadBounded(io.LimitReader(response.Body, domain.MaxImageBytes+1))
	if readErr != nil {
		return mediaport.PreparedWebhookMiniProgram{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	inspection, inspectErr := domain.Inspect("lesson-card.png", "image/png", content)
	if inspectErr != nil || inspection.MediaType != "image/png" {
		return mediaport.PreparedWebhookMiniProgram{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	return mediaport.PreparedWebhookMiniProgram{
		AppID:  request.AppID,
		Path:   request.Path,
		Title:  request.Title,
		PNG:    append([]byte(nil), content...),
		Width:  inspection.Width,
		Height: inspection.Height,
	}, nil
}

func (resolver *WebhookLessonCardResolver) MaterializeWebhookMiniProgramWithin(ctx context.Context, prepared mediaport.PreparedWebhookMiniProgram, command mediaport.WebhookMiniProgramMaterialization) (mediaport.GroupOpsMaterialReference, error) {
	if resolver == nil || resolver.store == nil || !validPreparedDailyLessonCard(prepared) || command.Actor < 1 || !validWebhookMaterializationKey(command.IdempotencyKey) {
		return mediaport.GroupOpsMaterialReference{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	reference, err := resolver.store.MaterializeWebhookMiniProgramWithin(ctx, prepared, command)
	if err != nil || reference.Kind != "miniprogram" || reference.ID < 1 {
		return mediaport.GroupOpsMaterialReference{}, mediaport.ErrWebhookMiniProgramUnavailable
	}
	return reference, nil
}

func strictDailyLessonCard(request mediaport.WebhookMiniProgramRequest) (string, bool) {
	if request.AppID != dailyLessonCardAppID || !validWebhookLessonTitle(request.Title) {
		return "", false
	}
	matches := dailyLessonCardPath.FindStringSubmatch(request.Path)
	if len(matches) != 2 {
		return "", false
	}
	return matches[1], true
}

func validPreparedDailyLessonCard(prepared mediaport.PreparedWebhookMiniProgram) bool {
	if _, ok := strictDailyLessonCard(mediaport.WebhookMiniProgramRequest{AppID: prepared.AppID, Path: prepared.Path, Title: prepared.Title}); !ok {
		return false
	}
	inspection, err := domain.Inspect("lesson-card.png", "image/png", prepared.PNG)
	return err == nil && inspection.MediaType == "image/png" && inspection.Width == prepared.Width && inspection.Height == prepared.Height
}

func validWebhookLessonTitle(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validWebhookMaterializationKey(value string) bool {
	return len(value) >= 16 && len(value) <= 128 && strings.TrimSpace(value) == value
}

var _ mediaport.WebhookMiniProgramResolver = (*WebhookLessonCardResolver)(nil)
