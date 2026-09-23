package port

import "context"

// PublicLeadQRCode is a persisted, active Channel QR fact. Reading it never
// publishes, refreshes, or contacts a provider.
type PublicLeadQRCode struct{ URL string }

type PublicLeadQRCodeReader interface {
	ReadPublicLeadQRCode(context.Context, int64) (PublicLeadQRCode, error)
}
