package port

import "context"

// Staging accepts only a write-only APIv3 key and returns a non-secret opaque
// file reference. The reference becomes active through the normal release.
type InvoiceKeyStager interface {
	StageInvoiceKey(context.Context, string) (string, error)
}
