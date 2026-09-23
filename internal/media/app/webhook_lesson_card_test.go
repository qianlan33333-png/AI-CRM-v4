package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

type lessonCardRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip lessonCardRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type lessonCardMaterializerStub struct {
	calls     int
	prepared  mediaport.PreparedWebhookMiniProgram
	command   mediaport.WebhookMiniProgramMaterialization
	reference mediaport.GroupOpsMaterialReference
	err       error
}

func (stub *lessonCardMaterializerStub) MaterializeWebhookMiniProgramWithin(_ context.Context, prepared mediaport.PreparedWebhookMiniProgram, command mediaport.WebhookMiniProgramMaterialization) (mediaport.GroupOpsMaterialReference, error) {
	stub.calls++
	stub.prepared = prepared
	stub.command = command
	return stub.reference, stub.err
}

func validLessonCardPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 3))
	canvas.SetRGBA(0, 0, color.RGBA{R: 16, G: 32, B: 64, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func validLessonCardRequest() mediaport.WebhookMiniProgramRequest {
	return mediaport.WebhookMiniProgramRequest{
		AppID: dailyLessonCardAppID,
		Path:  "pages/article/article?lesson_id=11111111-2222-3333-4444-555555555555&from=learn",
		Title: "今日课卡",
	}
}

func lessonCardHTTPResponse(status int, contentType string, contentLength int64, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Content-Type": []string{contentType}},
		ContentLength: contentLength,
		Body:          io.NopCloser(bytes.NewReader(body)),
	}
}

func TestWebhookLessonCardResolverAcceptsOnlyVerifiedDailyLessonPNG(t *testing.T) {
	pngBytes := validLessonCardPNG(t)
	requests := 0
	resolver := NewWebhookLessonCardResolver(nil, lessonCardRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.String() != dailyLessonCardHost+"11111111-2222-3333-4444-555555555555.png" {
			t.Fatalf("request=%s %s", request.Method, request.URL)
		}
		return lessonCardHTTPResponse(http.StatusOK, "image/png; charset=binary", int64(len(pngBytes)), pngBytes), nil
	}))

	prepared, err := resolver.PrepareWebhookMiniProgram(context.Background(), validLessonCardRequest())
	if err != nil || requests != 1 || prepared.Width != 2 || prepared.Height != 3 || !bytes.Equal(prepared.PNG, pngBytes) {
		t.Fatalf("prepared=%+v requests=%d err=%v", prepared, requests, err)
	}
}

func TestWebhookLessonCardResolverRejectsUnsupportedBeforeHTTP(t *testing.T) {
	unsupported := []mediaport.WebhookMiniProgramRequest{
		{AppID: "wx-other", Path: validLessonCardRequest().Path, Title: "今日课卡"},
		{AppID: dailyLessonCardAppID, Path: "pages/article/article?lesson_id=11111111-2222-3333-4444-555555555555&from=other", Title: "今日课卡"},
		{AppID: dailyLessonCardAppID, Path: "pages/article/article?lesson_id=11111111-2222-3333-4444-555555555555&from=learn&extra=1", Title: "今日课卡"},
		{AppID: dailyLessonCardAppID, Path: "pages/article/article?lesson_id=11111111-2222-3333-4444-555555555555&from=learn", Title: " 带空格"},
		{AppID: dailyLessonCardAppID, Path: "pages/article/article?lesson_id=11111111-2222-3333-4444-555555555555&from=learn", Title: "含控制符\x00"},
	}
	requests := 0
	resolver := NewWebhookLessonCardResolver(nil, lessonCardRoundTripper(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("must not fetch unsupported source")
	}))
	for _, request := range unsupported {
		if _, err := resolver.PrepareWebhookMiniProgram(context.Background(), request); !errors.Is(err, mediaport.ErrWebhookMiniProgramUnsupported) {
			t.Fatalf("request=%+v err=%v", request, err)
		}
	}
	if requests != 0 {
		t.Fatalf("unsupported sources made %d HTTP requests", requests)
	}
}

func TestWebhookLessonCardResolverFailsClosedForUnverifiedSource(t *testing.T) {
	pngBytes := validLessonCardPNG(t)
	for _, test := range []struct {
		name     string
		response *http.Response
		err      error
	}{
		{name: "transport error", err: errors.New("source unavailable")},
		{name: "redirect", response: lessonCardHTTPResponse(http.StatusFound, "image/png", 0, nil)},
		{name: "wrong status", response: lessonCardHTTPResponse(http.StatusBadGateway, "image/png", 0, nil)},
		{name: "wrong media type", response: lessonCardHTTPResponse(http.StatusOK, "image/jpeg", int64(len(pngBytes)), pngBytes)},
		{name: "declared too large", response: lessonCardHTTPResponse(http.StatusOK, "image/png", int64(domain.MaxImageBytes+1), pngBytes)},
		{name: "invalid PNG", response: lessonCardHTTPResponse(http.StatusOK, "image/png", 3, []byte("bad"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := NewWebhookLessonCardResolver(nil, lessonCardRoundTripper(func(*http.Request) (*http.Response, error) {
				return test.response, test.err
			}))
			if _, err := resolver.PrepareWebhookMiniProgram(context.Background(), validLessonCardRequest()); !errors.Is(err, mediaport.ErrWebhookMiniProgramUnavailable) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestWebhookLessonCardResolverNeverFollowsRedirect(t *testing.T) {
	requests := 0
	resolver := NewWebhookLessonCardResolver(nil, lessonCardRoundTripper(func(*http.Request) (*http.Response, error) {
		requests++
		response := lessonCardHTTPResponse(http.StatusFound, "image/png", 0, nil)
		response.Header.Set("Location", "https://untrusted.example/cover.png")
		return response, nil
	}))
	if _, err := resolver.PrepareWebhookMiniProgram(context.Background(), validLessonCardRequest()); !errors.Is(err, mediaport.ErrWebhookMiniProgramUnavailable) || requests != 1 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
}

func TestWebhookLessonCardResolverRejectsUnknownLengthOversizeBody(t *testing.T) {
	oversized := bytes.Repeat([]byte("x"), domain.MaxImageBytes+1)
	resolver := NewWebhookLessonCardResolver(nil, lessonCardRoundTripper(func(*http.Request) (*http.Response, error) {
		return lessonCardHTTPResponse(http.StatusOK, "image/png", -1, oversized), nil
	}))
	if _, err := resolver.PrepareWebhookMiniProgram(context.Background(), validLessonCardRequest()); !errors.Is(err, mediaport.ErrWebhookMiniProgramUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestWebhookLessonCardResolverMaterializesOnlyVerifiedPreparedCard(t *testing.T) {
	pngBytes := validLessonCardPNG(t)
	store := &lessonCardMaterializerStub{reference: mediaport.GroupOpsMaterialReference{Kind: "miniprogram", ID: 91}}
	resolver := NewWebhookLessonCardResolver(store, lessonCardRoundTripper(func(*http.Request) (*http.Response, error) {
		return lessonCardHTTPResponse(http.StatusOK, "image/png", int64(len(pngBytes)), pngBytes), nil
	}))
	prepared, err := resolver.PrepareWebhookMiniProgram(context.Background(), validLessonCardRequest())
	if err != nil {
		t.Fatal(err)
	}
	command := mediaport.WebhookMiniProgramMaterialization{Actor: 7, IdempotencyKey: "groupops_webhook_lesson_0123456789abcdef"}
	reference, err := resolver.MaterializeWebhookMiniProgramWithin(context.Background(), prepared, command)
	if err != nil || reference.ID != 91 || store.calls != 1 || store.command != command || !bytes.Equal(store.prepared.PNG, pngBytes) {
		t.Fatalf("reference=%+v calls=%d command=%+v err=%v", reference, store.calls, store.command, err)
	}
	prepared.PNG = []byte("unverified")
	if _, err = resolver.MaterializeWebhookMiniProgramWithin(context.Background(), prepared, command); !errors.Is(err, mediaport.ErrWebhookMiniProgramUnavailable) || store.calls != 1 {
		t.Fatalf("unverified prepared err=%v calls=%d", err, store.calls)
	}
	if strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey {
		t.Fatal("test idempotency key must stay opaque")
	}
}
