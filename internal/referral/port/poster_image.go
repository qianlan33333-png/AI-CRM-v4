package port

import (
	"crypto/sha256"
	"encoding/hex"
)

// Changing the representation policy must also change this version, so a
// browser never treats different encoded bytes as one immutable resource.
const PosterImageEncodingVersion = "jpeg95-v1"
const PosterImageJPEGQuality = 95

type CampaignPosterMetadata struct {
	Digest [sha256.Size]byte
}

func PosterImageRevision(digest [sha256.Size]byte) string {
	value := sha256.New()
	_, _ = value.Write(digest[:])
	_, _ = value.Write([]byte(PosterImageEncodingVersion))
	return hex.EncodeToString(value.Sum(nil))
}
