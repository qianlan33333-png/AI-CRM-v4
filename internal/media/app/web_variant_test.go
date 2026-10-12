package app

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
)

func webVariant(t *testing.T, mime string, content []byte) ImageVariant {
	t.Helper()
	inspection, err := domain.Inspect("image."+map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif"}[mime], mime, content)
	if err != nil {
		t.Fatal(err)
	}
	got, err := imageVariantFor("web_1080", inspection, content)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWebImageVariantPreservesHighBitDepthPNG(t *testing.T) {
	img := image.NewNRGBA64(image.Rect(0, 0, 1200, 20))
	for x := 0; x < 1200; x++ {
		img.SetNRGBA64(x, 0, color.NRGBA64{R: uint16(x), G: 30001, B: 49003, A: 32001})
	}
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	if original.Bytes()[24] != 16 {
		t.Fatal("fixture is not a 16-bit PNG")
	}
	got := webVariant(t, "image/png", original.Bytes())
	if !bytes.Equal(got.Content, original.Bytes()) {
		t.Fatal("16-bit pixels reduced to 8-bit")
	}
}

func TestWebImageVariantWidthOnly(t *testing.T) {
	for _, size := range [][2]int{{1079, 3000}, {1080, 5000}, {1081, 3000}, {2160, 6000}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			img := image.NewNRGBA(image.Rect(0, 0, size[0], size[1]))
			for y := 0; y < size[1]; y++ {
				for x := 0; x < size[0]; x++ {
					img.SetNRGBA(x, y, color.NRGBA{R: uint8(x / 20), G: uint8(y / 20), A: uint8(128 + x%128)})
				}
			}
			var input bytes.Buffer
			if err := png.Encode(&input, img); err != nil {
				t.Fatal(err)
			}
			got := webVariant(t, "image/png", input.Bytes())
			if size[0] <= 1080 {
				if !bytes.Equal(got.Content, input.Bytes()) {
					t.Fatal("narrow image was re-encoded")
				}
				return
			}
			if bytes.Equal(got.Content, input.Bytes()) {
				if size[0] != 1081 {
					t.Fatal("large fixture did not produce beneficial optimisation")
				}
				return
			}
			if len(got.Content) >= input.Len() {
				t.Fatal("candidate did not reduce bytes")
			}
			decoded, _, err := image.Decode(bytes.NewReader(got.Content))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Bounds().Dx() != 1080 || decoded.Bounds().Dy() != size[1]*1080/size[0] {
				t.Fatalf("wrong width-only dimensions: %v", decoded.Bounds())
			}
			_, _, _, alpha := decoded.At(0, 0).RGBA()
			if alpha == 65535 {
				t.Fatal("PNG transparency flattened")
			}
		})
	}
}

func TestWebImageVariantInflationAndUnsupportedPreserveOriginal(t *testing.T) {
	noise := image.NewRGBA(image.Rect(0, 0, 1200, 2500))
	var v uint32 = 123456789
	for y := 0; y < 2500; y++ {
		for x := 0; x < 1200; x++ {
			v ^= v << 13
			v ^= v >> 17
			v ^= v << 5
			noise.SetRGBA(x, y, color.RGBA{R: uint8(v), G: uint8(v >> 8), B: uint8(v >> 16), A: 255})
		}
	}
	var original bytes.Buffer
	jpeg.Encode(&original, noise, &jpeg.Options{Quality: 5})
	inspected, err := domain.Inspect("image.jpg", "image/jpeg", original.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := resizeWebImage(original.Bytes(), inspected, 1080)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate) <= original.Len() {
		t.Fatal("fixture no longer reproduces inflation")
	}
	got := webVariant(t, "image/jpeg", original.Bytes())
	if !bytes.Equal(got.Content, original.Bytes()) {
		t.Fatal("larger result was delivered")
	}
	// Valid APP1 segment; the pixel decoder ignores it but browsers may need its
	// orientation. The new optimiser must preserve it instead of guessing.
	var metadataOriginal bytes.Buffer
	if err := jpeg.Encode(&metadataOriginal, image.NewRGBA(image.Rect(0, 0, 2400, 100)), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	metadataInspection, err := domain.Inspect("metadata.jpg", "image/jpeg", metadataOriginal.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	smaller, err := resizeWebImage(metadataOriginal.Bytes(), metadataInspection, 1080)
	if err != nil || len(smaller) >= metadataOriginal.Len() {
		t.Fatal("metadata fixture must otherwise benefit from optimisation")
	}
	exif := append([]byte{0xff, 0xd8, 0xff, 0xe1, 0, 8}, []byte("Exif\x00\x00")...)
	exif = append(exif, metadataOriginal.Bytes()[2:]...)
	got = webVariant(t, "image/jpeg", exif)
	if !bytes.Equal(got.Content, exif) {
		t.Fatal("APP1 metadata lost")
	}
	var pngOriginal bytes.Buffer
	if err := png.Encode(&pngOriginal, image.NewRGBA(image.Rect(0, 0, 2400, 100))); err != nil {
		t.Fatal(err)
	}
	// A non-default gamma chunk is valid but would be discarded by png.Encode.
	chunk := make([]byte, 16)
	binary.BigEndian.PutUint32(chunk[:4], 4)
	copy(chunk[4:8], "gAMA")
	binary.BigEndian.PutUint32(chunk[8:12], 100000)
	binary.BigEndian.PutUint32(chunk[12:16], crc32.ChecksumIEEE(chunk[4:12]))
	withGamma := append(append(append([]byte{}, pngOriginal.Bytes()[:33]...), chunk...), pngOriginal.Bytes()[33:]...)
	got = webVariant(t, "image/png", withGamma)
	if !bytes.Equal(got.Content, withGamma) {
		t.Fatal("PNG gamma metadata lost")
	}
	frame := image.NewPaletted(image.Rect(0, 0, 1200, 80), color.Palette{color.Black, color.White})
	var animated bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{3, 7}, LoopCount: 2}); err != nil {
		t.Fatal(err)
	}
	got = webVariant(t, "image/gif", animated.Bytes())
	if !bytes.Equal(got.Content, animated.Bytes()) {
		t.Fatal("animation lost")
	}
}
