package domain

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	MaxImageBytes  = 10 << 20
	MaxImageSide   = 10_000
	MaxImagePixels = 40_000_000
)

var ErrUnsafeImage = errors.New("unsafe image upload")

type Inspection struct {
	MediaType string
	Width     int32
	Height    int32
}

func ReadBounded(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, ErrUnsafeImage
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxImageBytes+1))
	if err != nil || len(data) == 0 || len(data) > MaxImageBytes {
		return nil, ErrUnsafeImage
	}
	return data, nil
}

func Inspect(filename, declaredMediaType string, data []byte) (Inspection, error) {
	if !safeFilename(filename) || len(data) == 0 || len(data) > MaxImageBytes {
		return Inspection{}, ErrUnsafeImage
	}
	mediaType, format := formatContract(declaredMediaType)
	if mediaType == "" {
		return Inspection{}, ErrUnsafeImage
	}
	config, actual, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || actual != format || config.Width < 1 || config.Height < 1 ||
		config.Width > MaxImageSide || config.Height > MaxImageSide ||
		int64(config.Width)*int64(config.Height) > MaxImagePixels {
		return Inspection{}, ErrUnsafeImage
	}
	if _, actual, err = image.Decode(bytes.NewReader(data)); err != nil || actual != format {
		return Inspection{}, ErrUnsafeImage
	}
	return Inspection{MediaType: mediaType, Width: int32(config.Width), Height: int32(config.Height)}, nil
}

func formatContract(value string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0])) {
	case "image/png":
		return "image/png", "png"
	case "image/jpeg":
		return "image/jpeg", "jpeg"
	case "image/gif":
		return "image/gif", "gif"
	default:
		return "", ""
	}
}

func safeFilename(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 255 ||
		value != strings.TrimSpace(value) || value == "." || value == ".." || strings.ContainsAny(value, `/\`) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
