package http

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"sync"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

const posterImageCacheBytes = 32 << 20

var errPosterImageBusy = errors.New("poster image processor busy")
var errPosterImageLarger = errors.New("encoded poster is not smaller")

type posterImageKey struct {
	campaign int64
	slot     int
	revision string
}
type posterImageEntry struct {
	key   posterImageKey
	image referralport.CampaignPosterImage
}
type posterImageFlight struct {
	done  chan struct{}
	image referralport.CampaignPosterImage
	err   error
}

// This disposable cache owns only public bytes. A single cold flight bounds
// snapshot reads and image work; another cold key is rejected, never queued.
type posterImageCache struct {
	mu        sync.Mutex
	entries   map[posterImageKey]*list.Element
	recent    *list.List
	flight    *posterImageFlight
	flightKey posterImageKey
	bytes     int
	budget    int
}

func newPosterImageCache() *posterImageCache {
	return &posterImageCache{entries: make(map[posterImageKey]*list.Element), recent: list.New(), budget: posterImageCacheBytes}
}

func (c *posterImageCache) read(ctx context.Context, key posterImageKey, load func(context.Context) (referralport.CampaignPosterImage, error)) (referralport.CampaignPosterImage, error) {
	if err := ctx.Err(); err != nil {
		return referralport.CampaignPosterImage{}, err
	}
	c.mu.Lock()
	if cached := c.entries[key]; cached != nil {
		c.recent.MoveToFront(cached)
		value := cached.Value.(posterImageEntry).image
		c.mu.Unlock()
		return value, nil
	}
	if flight := c.flight; flight != nil {
		same := key == c.flightKey
		c.mu.Unlock()
		if !same {
			return referralport.CampaignPosterImage{}, errPosterImageBusy
		}
		select {
		case <-ctx.Done():
			return referralport.CampaignPosterImage{}, ctx.Err()
		case <-flight.done:
			return flight.image, flight.err
		}
	}
	flight := &posterImageFlight{done: make(chan struct{})}
	c.flight, c.flightKey = flight, key
	c.mu.Unlock()
	value, err := load(ctx)
	if err == nil {
		value = compressPosterImage(value)
		err = ctx.Err()
	}
	c.mu.Lock()
	if err == nil && len(value.Content) > c.budget {
		err = errPosterImageBusy
		value = referralport.CampaignPosterImage{}
	}
	if err == nil {
		for c.bytes+len(value.Content) > c.budget {
			oldest := c.recent.Back()
			entry := oldest.Value.(posterImageEntry)
			delete(c.entries, entry.key)
			c.bytes -= len(entry.image.Content)
			c.recent.Remove(oldest)
		}
		c.entries[key] = c.recent.PushFront(posterImageEntry{key: key, image: value})
		c.bytes += len(value.Content)
	}
	flight.image, flight.err = value, err
	c.flight = nil
	close(flight.done)
	c.mu.Unlock()
	return value, err
}

type smallerPosterWriter struct {
	bytes.Buffer
	limit int
}

func (w *smallerPosterWriter) Write(p []byte) (int, error) {
	if w.Len()+len(p) > w.limit {
		return 0, errPosterImageLarger
	}
	return w.Buffer.Write(p)
}

func compressPosterImage(source referralport.CampaignPosterImage) referralport.CampaignPosterImage {
	config, format, err := image.DecodeConfig(bytes.NewReader(source.Content))
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 5000 || config.Height > 10000 {
		return source
	}
	decoded, _, err := image.Decode(bytes.NewReader(source.Content))
	if err != nil {
		return source
	}
	if opaque, ok := decoded.(interface{ Opaque() bool }); !ok || !opaque.Opaque() {
		return source
	}
	out := &smallerPosterWriter{limit: len(source.Content) - 1}
	if err := jpeg.Encode(out, decoded, &jpeg.Options{Quality: referralport.PosterImageJPEGQuality}); err != nil || out.Len() == 0 {
		return source
	}
	return referralport.CampaignPosterImage{Content: out.Bytes(), MediaType: "image/jpeg", Digest: source.Digest}
}
