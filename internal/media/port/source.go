package port

import (
	"context"
	"errors"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

// SourceScanner is the narrow, Media-owned read contract used to prepare
// provider media. SourceRef is opaque to consumers: only Media may resolve it
// to a stable material ID or read its private blob.
type SourceScanner interface {
	ListEnabledSourceSnapshots(context.Context, SnapshotPageRequest) (SnapshotPage, error)
	GetSourceSnapshot(context.Context, string) (SourceSnapshot, error)
	ReadSourceBytes(context.Context, SourceReadRequest) (SourceContent, error)
}

// These aliases keep Media's stable source contract exactly identical to the
// Outbound consumer port. They avoid a second conversion layer that could
// accidentally lose the digest/version CAS pair.
type SnapshotPageRequest = outboundport.MaterialSnapshotPageRequest
type SnapshotPage = outboundport.MaterialSnapshotPage
type SourceSnapshot = outboundport.MaterialSourceSnapshot
type SourceReadRequest = outboundport.MaterialSourceSnapshot
type SourceContent = outboundport.MaterialSourceContent

// ExcelCoverLibrary is the small cross-domain Media contract for controlled
// Excel batches. The ImageID remains Media-owned; AI Assistant stores it as an
// opaque frozen reference alongside the verified source digest.
type ExcelCoverLibrary interface {
	CreateOrReuseExcelCover(context.Context, ExcelCoverUpload) (ExcelCover, error)
	SelectEnabledExcelCover(context.Context, int64) (ExcelCover, error)
}

// ExcelCoverReader retrieves only the bytes for an already-frozen Media
// image/digest pair. It permits historical batch readback after an operator
// disables the library item, while never accepting a mutable current image.
type ExcelCoverReader interface {
	ReadExcelCover(context.Context, int64, [32]byte) (SourceContent, error)
}

type ExcelCoverUpload struct {
	Actor          int64
	IdempotencyKey string
	FileName       string
	DeclaredType   string
	Content        []byte
}

type ExcelCover struct {
	ImageID       int64
	ContentDigest [32]byte
}

var (
	// ErrSourceNotFound means a syntactically valid Media-owned source ref has
	// no enabled current source. Callers may render this as a normal 404.
	ErrSourceNotFound = errors.New("media source not found")
	// ErrSourceUnavailable is reserved for infrastructure or corrupt-source
	// failures. It must never be translated to not-found.
	ErrSourceUnavailable = errors.New("media source unavailable")
)

func ValidSourceSnapshot(s SourceSnapshot) bool {
	return ValidSourceRef(s.SourceRef) && (s.SourceType == "image" || s.SourceType == "file") &&
		s.FileName != "" && s.MediaType != "" && s.SizeBytes > 0 && s.SnapshotVersion > 0 && s.ContentDigest != [32]byte{}
}

func ValidSourceRef(value string) bool {
	if len(value) < 3 || len(value) > 80 {
		return false
	}
	for _, prefix := range []string{"image:", "attachment:"} {
		if len(value) > len(prefix) && value[:len(prefix)] == prefix {
			for _, digit := range value[len(prefix):] {
				if digit < '0' || digit > '9' {
					return false
				}
			}
			return value[len(prefix)] != '0'
		}
	}
	return false
}
