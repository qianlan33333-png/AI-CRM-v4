package domain

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestInspectAcceptsExactPNGJPEGAndGIF(t *testing.T) {
	for _, test := range []struct {
		name, mime string
		encode     func(*bytes.Buffer) error
	}{
		{"png", "image/png", func(out *bytes.Buffer) error { return png.Encode(out, solid(color.RGBA{R: 1, A: 255})) }},
		{"jpeg", "image/jpeg", func(out *bytes.Buffer) error { return jpeg.Encode(out, solid(color.RGBA{G: 1, A: 255}), nil) }},
		{"gif", "image/gif", func(out *bytes.Buffer) error { return gif.Encode(out, solid(color.RGBA{B: 1, A: 255}), nil) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var data bytes.Buffer
			if err := test.encode(&data); err != nil {
				t.Fatal(err)
			}
			result, err := Inspect("safe."+test.name, test.mime, data.Bytes())
			if err != nil || result.MediaType != test.mime || result.Width != 1 || result.Height != 1 {
				t.Fatalf("unexpected inspection: %#v %v", result, err)
			}
		})
	}
}

func TestInspectRejectsMismatchTruncationDimensionsAndUnsafePath(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, solid(color.Black)); err != nil {
		t.Fatal(err)
	}
	wide := image.NewRGBA(image.Rect(0, 0, MaxImageSide+1, 1))
	var wideData bytes.Buffer
	if err := png.Encode(&wideData, wide); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, filename, mime string
		data                 []byte
	}{
		{"mime mismatch", "safe.png", "image/jpeg", pngData.Bytes()},
		{"truncated", "safe.png", "image/png", pngData.Bytes()[:len(pngData.Bytes())-4]},
		{"side boundary", "wide.png", "image/png", wideData.Bytes()},
		{"path separator", "../unsafe.png", "image/png", pngData.Bytes()},
		{"control", "unsafe\x00.png", "image/png", pngData.Bytes()},
		{"unsupported", "safe.webp", "image/webp", pngData.Bytes()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Inspect(test.filename, test.mime, test.data); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func solid(value color.Color) image.Image {
	result := image.NewRGBA(image.Rect(0, 0, 1, 1))
	result.Set(0, 0, value)
	return result
}

func TestReadBoundedEnforcesTenMiB(t *testing.T) {
	if _, err := ReadBounded(bytes.NewReader(make([]byte, MaxImageBytes))); err != nil {
		t.Fatalf("exact boundary rejected: %v", err)
	}
	if _, err := ReadBounded(bytes.NewReader(make([]byte, MaxImageBytes+1))); err == nil {
		t.Fatal("over boundary accepted")
	}
}
