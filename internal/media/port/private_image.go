package port

import "context"
import "errors"

var ErrPrivateImageInvalid = errors.New("invalid private image")
var ErrPrivateImageNotFound = errors.New("private image not found")

type PrivateImageUpload struct {
	FileName  string
	MediaType string
	Content   []byte
}
type PrivateImageInfo struct {
	Ref           string
	FileName      string
	MediaType     string
	SizeBytes     int64
	Width, Height int32
	Digest        [32]byte
}
type PrivateImageContent struct {
	Info    PrivateImageInfo
	Content []byte
}

// Inspection happens before a business transaction. Persistence and reading
// require the caller's transaction; this Port never opens another UoW.
type PrivateImageStorage interface {
	InspectPrivateImage(PrivateImageUpload) (PrivateImageInfo, error)
	StorePrivateImageWithin(context.Context, PrivateImageUpload, PrivateImageInfo) (PrivateImageInfo, error)
	ReadPrivateImageWithin(context.Context, string) (PrivateImageContent, error)
}
