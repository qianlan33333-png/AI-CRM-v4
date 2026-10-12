package port

import "errors"

// ManualImageUploadMaxBytes applies to new manual accepts, not existing blobs
// or completed idempotency receipts.
const ManualImageUploadMaxBytes = 1 << 20

var ErrImageUploadTooLarge = errors.New("image upload exceeds 1 MiB")
