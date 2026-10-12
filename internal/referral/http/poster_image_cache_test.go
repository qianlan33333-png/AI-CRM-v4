package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func TestPosterImageCacheConcurrentBoundedAndRecovery(t *testing.T) {
	c := newPosterImageCache()
	key := posterImageKey{1, 1, "one"}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context) (referralport.CampaignPosterImage, error) {
		calls.Add(1)
		close(started)
		<-release
		return referralport.CampaignPosterImage{Content: []byte("original"), MediaType: "image/png"}, nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, e := c.read(context.Background(), key, load); e != nil {
			t.Error(e)
		}
	}()
	<-started
	if _, e := c.read(context.Background(), posterImageKey{2, 1, "other"}, load); !errors.Is(e, errPosterImageBusy) {
		t.Fatalf("must reject other cold key: %v", e)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.read(cancelled, key, load); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	waiting, stopWaiting := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() { _, e := c.read(waiting, key, load); waiterDone <- e }()
	stopWaiting()
	if e := <-waiterDone; !errors.Is(e, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", e)
	}
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := c.read(context.Background(), key, load); e != nil {
				t.Error(e)
			}
		}()
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loads=%d", calls.Load())
	}
	failed := posterImageKey{1, 2, "fail"}
	if _, e := c.read(context.Background(), failed, func(context.Context) (referralport.CampaignPosterImage, error) {
		return referralport.CampaignPosterImage{}, referralport.ErrUnavailable
	}); e == nil {
		t.Fatal("failure hidden")
	}
	if _, e := c.read(context.Background(), failed, func(context.Context) (referralport.CampaignPosterImage, error) {
		return referralport.CampaignPosterImage{Content: []byte("recovered")}, nil
	}); e != nil {
		t.Fatal(e)
	}
	c.budget = 20
	for i := 0; i < 10; i++ {
		_, e := c.read(context.Background(), posterImageKey{int64(i + 10), 1, "small"}, func(context.Context) (referralport.CampaignPosterImage, error) {
			return referralport.CampaignPosterImage{Content: []byte("1234567890")}, nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	if c.bytes > 20 || len(c.entries) != 2 {
		t.Fatalf("cache not bounded: bytes=%d entries=%d", c.bytes, len(c.entries))
	}
	if _, ok := c.entries[key]; ok {
		t.Fatal("LRU not evicted")
	}
	c.budget = 5
	if _, e := c.read(context.Background(), posterImageKey{90, 1, "too-large"}, func(context.Context) (referralport.CampaignPosterImage, error) {
		return referralport.CampaignPosterImage{Content: []byte("123456")}, nil
	}); !errors.Is(e, errPosterImageBusy) {
		t.Fatalf("capacity exceeded without busy feedback: %v", e)
	}
}

func TestPosterImageCompressionPreservesOriginalAndDimensions(t *testing.T) {
	for _, transparent := range []bool{false, true} {
		pic := image.NewNRGBA(image.Rect(0, 0, 600, 800))
		for y := 0; y < 800; y++ {
			for x := 0; x < 600; x++ {
				a := uint8(255)
				if transparent && x == 0 && y == 0 {
					a = 0
				}
				pic.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x * y), a})
			}
		}
		var source bytes.Buffer
		png.Encode(&source, pic)
		value := referralport.CampaignPosterImage{Content: source.Bytes(), MediaType: "image/png", Digest: sha256.Sum256(source.Bytes())}
		before := append([]byte(nil), value.Content...)
		got := compressPosterImage(value)
		if !bytes.Equal(before, value.Content) || got.Digest != value.Digest {
			t.Fatal("source changed")
		}
		if transparent {
			if got.MediaType != "image/png" || !bytes.Equal(got.Content, before) {
				t.Fatal("alpha lost")
			}
			continue
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(got.Content))
		if err != nil || cfg.Width != 600 || cfg.Height != 800 || len(got.Content) > len(before) {
			t.Fatalf("output=%+v err=%v", cfg, err)
		}
	}
	for _, data := range [][]byte{nil, []byte("bad image")} {
		got := compressPosterImage(referralport.CampaignPosterImage{Content: data, MediaType: "image/png"})
		if !bytes.Equal(data, got.Content) {
			t.Fatal("decode fallback lost original")
		}
	}
	tiny := image.NewRGBA(image.Rect(0, 0, 1, 1))
	tiny.Set(0, 0, color.White)
	var out bytes.Buffer
	png.Encode(&out, tiny)
	got := compressPosterImage(referralport.CampaignPosterImage{Content: out.Bytes(), MediaType: "image/png"})
	if !bytes.Equal(got.Content, out.Bytes()) {
		t.Fatal("larger JPEG retained")
	}
}

type posterReadSpy struct {
	posterHTTPStub
	meta               referralport.CampaignPosterMetadata
	content            referralport.CampaignPosterImage
	metaErr, errorRead error
	reads              atomic.Int32
	block              chan struct{}
	started            chan struct{}
}

func (s *posterReadSpy) ReadCampaignPosterMetadata(context.Context, int64, int) (referralport.CampaignPosterMetadata, error) {
	return s.meta, s.metaErr
}
func (s *posterReadSpy) ReadCampaignPosterSnapshot(ctx context.Context, _ int64, _ int, digest [32]byte) (referralport.CampaignPosterImage, error) {
	s.reads.Add(1)
	if s.started != nil {
		close(s.started)
		select {
		case <-s.block:
		case <-ctx.Done():
			return referralport.CampaignPosterImage{}, ctx.Err()
		}
	}
	if digest != s.meta.Digest {
		return referralport.CampaignPosterImage{}, referralport.ErrConflict
	}
	return s.content, s.errorRead
}
func posterRequest(h *Handler, path, etag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	h.publicPoster(w, r, 7, "1")
	return w
}
func TestPosterImageHTTPMetadataETagVersionAndVisibility(t *testing.T) {
	s := &posterReadSpy{meta: referralport.CampaignPosterMetadata{Digest: sha256.Sum256([]byte("first"))}, content: referralport.CampaignPosterImage{Content: []byte("original"), MediaType: "image/png"}}
	h := &Handler{posters: s}
	revision := referralport.PosterImageRevision(s.meta.Digest)
	path := "/api/v1/referral/campaigns/7/posters/1?v=" + revision
	first := posterRequest(h, path, "")
	if first.Code != 200 || first.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	for _, etag := range []string{first.Header().Get("ETag"), strings.TrimPrefix(first.Header().Get("ETag"), "W/"), "\"another\", " + first.Header().Get("ETag"), "*"} {
		w := posterRequest(h, path, etag)
		if w.Code != 304 || w.Body.Len() != 0 || s.reads.Load() != 1 {
			t.Fatalf("304 read bytes: %d %d", w.Code, s.reads.Load())
		}
	}
	for _, query := range []string{"?v=bad", "?v=" + revision + "&v=" + revision, "?other=1"} {
		if w := posterRequest(h, "/poster"+query, ""); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := posterRequest(h, "/poster", ""); w.Code != 200 || s.reads.Load() != 1 {
		t.Fatal("legacy URL cannot use current cache")
	}
	s.meta.Digest = sha256.Sum256([]byte("same dimensions, new bytes"))
	if w := posterRequest(h, path, first.Header().Get("ETag")); w.Code != 409 || s.reads.Load() != 1 || !strings.Contains(w.Body.String(), "poster_updated") {
		t.Fatal("stale version served")
	}
	s.metaErr = referralport.ErrNotFound
	if w := posterRequest(h, "/poster", first.Header().Get("ETag")); w.Code != 404 || s.reads.Load() != 1 {
		t.Fatal("draft/removed image leaked cache")
	}
	s.metaErr = nil
	s.errorRead = referralport.ErrConflict
	if w := posterRequest(h, "/poster", ""); w.Code != 409 || w.Header().Get("ETag") != "" {
		t.Fatal("snapshot changed while loading")
	}
}
func TestPosterImageHTTPBusyNoQueue(t *testing.T) {
	s := &posterReadSpy{meta: referralport.CampaignPosterMetadata{Digest: sha256.Sum256([]byte("one"))}, started: make(chan struct{}), block: make(chan struct{})}
	h := &Handler{posters: s}
	done := make(chan struct{})
	go func() { posterRequest(h, "/poster", ""); close(done) }()
	<-s.started
	r := httptest.NewRequest(http.MethodGet, "/poster", nil)
	w := httptest.NewRecorder()
	h.publicPoster(w, r, 8, "1")
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "poster_busy") {
		t.Fatalf("busy=%d %s", w.Code, w.Body.String())
	}
	close(s.block)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("flight stuck")
	}
}
