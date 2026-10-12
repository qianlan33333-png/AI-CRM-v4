package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

var (
	ErrInvalidImageVariant     = errors.New("invalid image variant")
	ErrImageVariantNotFound    = errors.New("image variant not found")
	ErrImageVariantUnavailable = errors.New("image variant unavailable")
)

type ImageVariantRow struct {
	ID                          int64
	FileName, MimeType          string
	FileSize, Width, Height     int32
	Enabled                     bool
	ImageChecksum, BlobChecksum []byte
	Content                     []byte
}

// ImageVariantStore is deliberately local to the app package. Variant reads
// have one Media-owned, transaction-bound projection and do not extend the
// cross-domain Media port.
type ImageVariantStore interface {
	ReadImageVariant(context.Context, int64) (ImageVariantRow, error)
}

// ImageVariant is fully materialized before the HTTP layer writes a header or
// body. ETag is an already-quoted strong SHA-256 tag for Content.
type ImageVariant = mediaport.ImageVariant

type imageVariantSpec struct {
	limit int32
}

var imageVariantSpecs = map[string]imageVariantSpec{
	"thumb_160":   {limit: 160},
	"thumb_320":   {limit: 320},
	"mobile_1080": {limit: 1080},
	"large_1440":  {limit: 1440},
	"original":    {},
	"web_1080":    {limit: 1080},
}

func (service *Service) GetImageVariant(ctx context.Context, imageID int64, key string) (ImageVariant, error) {
	return service.getImageVariant(ctx, imageID, key, false)
}

// GetEnabledImageVariant serves a variant only when the Media-owned image is
// enabled. It is used by viewer surfaces whose list contract already excludes
// disabled images, so a guessed identifier cannot reveal hidden media.
func (service *Service) GetEnabledImageVariant(ctx context.Context, imageID int64, key string) (ImageVariant, error) {
	return service.getImageVariant(ctx, imageID, key, true)
}

func (service *Service) getImageVariant(ctx context.Context, imageID int64, key string, enabledOnly bool) (ImageVariant, error) {
	if imageID < 1 || !ValidImageVariantKey(key) {
		return ImageVariant{}, ErrInvalidImageVariant
	}
	if service == nil || ctx == nil || service.uow == nil || service.store == nil {
		return ImageVariant{}, ErrImageVariantUnavailable
	}
	store, ok := service.store.(ImageVariantStore)
	if !ok {
		return ImageVariant{}, ErrImageVariantUnavailable
	}

	var row ImageVariantRow
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		value, err := store.ReadImageVariant(tx, imageID)
		if err != nil {
			return err
		}
		row = value
		return nil
	}); err != nil {
		if errors.Is(err, ErrImageVariantNotFound) {
			return ImageVariant{}, ErrImageVariantNotFound
		}
		return ImageVariant{}, ErrImageVariantUnavailable
	}
	if enabledOnly && !row.Enabled {
		return ImageVariant{}, ErrImageVariantNotFound
	}

	inspection, err := inspectImageVariantRow(row, imageID)
	if err != nil {
		return ImageVariant{}, ErrImageVariantUnavailable
	}
	result, err := imageVariantFor(key, inspection, row.Content)
	if err != nil {
		return ImageVariant{}, ErrImageVariantUnavailable
	}
	return result, nil
}

func ValidImageVariantKey(key string) bool {
	_, ok := imageVariantSpecs[key]
	return ok
}

func inspectImageVariantRow(row ImageVariantRow, imageID int64) (domain.Inspection, error) {
	if row.ID != imageID || row.FileSize < 1 || len(row.Content) != int(row.FileSize) ||
		len(row.ImageChecksum) != sha256.Size || len(row.BlobChecksum) != sha256.Size ||
		subtle.ConstantTimeCompare(row.ImageChecksum, row.BlobChecksum) != 1 {
		return domain.Inspection{}, ErrImageVariantUnavailable
	}
	checksum := sha256.Sum256(row.Content)
	if subtle.ConstantTimeCompare(row.ImageChecksum, checksum[:]) != 1 {
		return domain.Inspection{}, ErrImageVariantUnavailable
	}
	inspection, err := domain.Inspect(row.FileName, row.MimeType, row.Content)
	if err != nil || inspection.MediaType != row.MimeType || inspection.Width != row.Width || inspection.Height != row.Height {
		return domain.Inspection{}, ErrImageVariantUnavailable
	}
	return inspection, nil
}

func imageVariantFor(key string, inspection domain.Inspection, content []byte) (ImageVariant, error) {
	spec, ok := imageVariantSpecs[key]
	if !ok || len(content) == 0 || len(content) > domain.MaxImageBytes {
		return ImageVariant{}, ErrImageVariantUnavailable
	}
	output := content
	if key == "web_1080" {
		if inspection.Width > spec.limit && safeWebImageMetadata(inspection.MediaType, content) {
			candidate, err := resizeWebImage(content, inspection, spec.limit)
			if err == nil && len(candidate) > 0 && len(candidate) < len(content) {
				output = candidate
			}
		}
	} else if key != "original" && maxImageDimension(inspection.Width, inspection.Height) > spec.limit {
		if inspection.MediaType == "image/gif" {
			return ImageVariant{}, ErrImageVariantUnavailable
		}
		var err error
		output, err = resizeImageVariant(content, inspection, spec.limit)
		if err != nil {
			return ImageVariant{}, ErrImageVariantUnavailable
		}
	}
	if len(output) == 0 || len(output) > domain.MaxImageBytes {
		return ImageVariant{}, ErrImageVariantUnavailable
	}
	digest := sha256.Sum256(output)
	return ImageVariant{
		Content: append([]byte(nil), output...), MediaType: inspection.MediaType,
		ETag: `"` + hex.EncodeToString(digest[:]) + `"`,
	}, nil
}

// Metadata with direction or non-default colour semantics, and animations,
// keep their exact original bytes rather than silently losing those facts.
func safeWebImageMetadata(mediaType string, data []byte) bool {
	switch mediaType {
	case "image/jpeg":
		for p := 2; p+4 <= len(data); {
			if data[p] != 0xff {
				return false
			}
			for p < len(data) && data[p] == 0xff {
				p++
			}
			if p >= len(data) {
				return false
			}
			marker := data[p]
			p++
			if marker == 0xda || marker == 0xd9 {
				return true
			}
			if marker == 0xe1 || marker == 0xe2 || marker == 0xee {
				return false
			}
			if marker == 0x01 || (marker >= 0xd0 && marker <= 0xd8) {
				continue
			}
			if p+2 > len(data) {
				return false
			}
			length := int(binary.BigEndian.Uint16(data[p : p+2]))
			if length < 2 || length > len(data)-p {
				return false
			}
			p += length
		}
	case "image/png":
		if len(data) > 24 && data[24] == 16 {
			return false
		}
		for p := 8; p+12 <= len(data); {
			length := uint64(binary.BigEndian.Uint32(data[p : p+4]))
			if length+12 > uint64(len(data)-p) {
				return false
			}
			switch string(data[p+4 : p+8]) {
			case "eXIf", "iCCP", "gAMA", "cHRM", "acTL":
				return false
			case "IEND":
				return true
			}
			p += int(length) + 12
		}
	}
	return false
}

func resizeWebImage(content []byte, inspection domain.Inspection, width int32) ([]byte, error) {
	source, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	height := int64(inspection.Height) * int64(width) / int64(inspection.Width)
	if height < 1 {
		height = 1
	}
	destination := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	xdraw.BiLinear.Scale(destination, destination.Bounds(), source, source.Bounds(), xdraw.Src, nil)
	var output bytes.Buffer
	if inspection.MediaType == "image/png" {
		err = png.Encode(&output, destination)
	} else {
		err = jpeg.Encode(&output, destination, &jpeg.Options{Quality: 90})
	}
	return output.Bytes(), err
}

func maxImageDimension(width, height int32) int32 {
	if width > height {
		return width
	}
	return height
}

func resizeImageVariant(content []byte, inspection domain.Inspection, limit int32) ([]byte, error) {
	if limit < 1 || inspection.Width < 1 || inspection.Height < 1 {
		return nil, ErrImageVariantUnavailable
	}
	source, format, err := image.Decode(bytes.NewReader(content))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, ErrImageVariantUnavailable
	}
	bounds := source.Bounds()
	if bounds.Dx() != int(inspection.Width) || bounds.Dy() != int(inspection.Height) {
		return nil, ErrImageVariantUnavailable
	}
	width, height, err := scaledImageDimensions(inspection.Width, inspection.Height, limit)
	if err != nil {
		return nil, err
	}
	// The frozen transform is deterministic nearest-neighbor. The standard
	// library has no image.NearestNeighbor scaler, so map each destination
	// coordinate to its source bucket directly.
	resized := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		sourceY := bounds.Min.Y + y*bounds.Dy()/int(height)
		for x := 0; x < int(width); x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/int(width)
			resized.Set(x, y, source.At(sourceX, sourceY))
		}
	}

	var encoded bytes.Buffer
	switch inspection.MediaType {
	case "image/png":
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&encoded, resized)
	case "image/jpeg":
		err = jpeg.Encode(&encoded, resized, &jpeg.Options{Quality: 90})
	default:
		return nil, ErrImageVariantUnavailable
	}
	if err != nil || encoded.Len() == 0 || encoded.Len() > domain.MaxImageBytes {
		return nil, ErrImageVariantUnavailable
	}
	return encoded.Bytes(), nil
}

func scaledImageDimensions(width, height, limit int32) (int32, int32, error) {
	if width < 1 || height < 1 || limit < 1 || width > domain.MaxImageSide || height > domain.MaxImageSide ||
		int64(width)*int64(height) > domain.MaxImagePixels {
		return 0, 0, ErrImageVariantUnavailable
	}
	maximum := maxImageDimension(width, height)
	if maximum <= limit {
		return width, height, nil
	}
	newWidth := int64(width) * int64(limit) / int64(maximum)
	newHeight := int64(height) * int64(limit) / int64(maximum)
	if newWidth < 1 {
		newWidth = 1
	}
	if newHeight < 1 {
		newHeight = 1
	}
	if newWidth > math.MaxInt32 || newHeight > math.MaxInt32 || newWidth*newHeight > domain.MaxImagePixels {
		return 0, 0, ErrImageVariantUnavailable
	}
	return int32(newWidth), int32(newHeight), nil
}

func ValidImageVariantETag(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
		return false
	}
	for index := 1; index < len(value)-1; index++ {
		character := value[index]
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
