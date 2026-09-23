package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type radarMediaReader interface {
	Image(context.Context, int64) (map[string]any, []byte, string, error)
	Attachment(context.Context, int64) (map[string]any, []byte, error)
}
type radarContentAdapter struct{ media radarMediaReader }

type radarMediaReferenceReader interface {
	ImageExists(context.Context, int64) (bool, error)
	AttachmentExists(context.Context, int64) (bool, error)
}
type radarMediaReferenceAdapter struct{ media radarMediaReferenceReader }

func (adapter radarMediaReferenceAdapter) ValidateRadarMedia(ctx context.Context, kind radar.ContentType, id radar.MediaID) error {
	if adapter.media == nil || !id.Valid() {
		return errors.New("radar media unavailable")
	}
	var ok bool
	var err error
	switch kind {
	case radar.ContentTypeImage:
		ok, err = adapter.media.ImageExists(ctx, int64(id))
	case radar.ContentTypePDF:
		ok, err = adapter.media.AttachmentExists(ctx, int64(id))
	default:
		return errors.New("radar media type invalid")
	}
	if err != nil || !ok {
		return errors.New("radar media unavailable")
	}
	return nil
}

func (adapter radarContentAdapter) ReadRadarContent(ctx context.Context, kind radar.ContentType, id radar.MediaID) (radarport.Content, error) {
	if adapter.media == nil || !id.Valid() {
		return radarport.Content{}, errors.New("radar media unavailable")
	}
	switch kind {
	case radar.ContentTypeImage:
		metadata, content, digest, err := adapter.media.Image(ctx, int64(id))
		if err != nil || metadata["enabled"] == false {
			return radarport.Content{}, errors.New("radar image unavailable")
		}
		mime, _ := metadata["mime_type"].(string)
		name, _ := metadata["file_name"].(string)
		if !strings.HasPrefix(mime, "image/") {
			return radarport.Content{}, errors.New("radar image invalid")
		}
		return radarport.Content{Bytes: append([]byte(nil), content...), MediaType: mime, FileName: name, ETag: `"` + strings.TrimPrefix(digest, "sha256:") + `"`}, nil
	case radar.ContentTypePDF:
		metadata, content, err := adapter.media.Attachment(ctx, int64(id))
		if err != nil || metadata["enabled"] == false || metadata["mime_type"] != "application/pdf" {
			return radarport.Content{}, errors.New("radar pdf unavailable")
		}
		name, _ := metadata["file_name"].(string)
		return radarport.Content{Bytes: append([]byte(nil), content...), MediaType: "application/pdf", FileName: name}, nil
	default:
		return radarport.Content{}, errors.New("radar media type invalid")
	}
}

func mountRadar(next, radarHandler, radarUI http.Handler, authentication accessAuthentication) http.Handler {
	if next == nil || radarHandler == nil || radarUI == nil || authentication == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/admin/radar-links"), strings.HasPrefix(r.URL.Path, "/r/"), strings.HasPrefix(r.URL.Path, "/api/public/radar/"), strings.HasPrefix(r.URL.Path, "/api/h5/radar-contents/"):
			radarHandler.ServeHTTP(w, r)
		case r.URL.Path == "/admin/radar-links" || r.URL.Path == "/admin/radar.html" || r.URL.Path == "/admin/radarDetail.html" || r.URL.Path == "/admin/radarForm.html":
			requireAdminSession(authentication, radarUI).ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}
