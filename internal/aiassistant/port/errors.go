package port

import "errors"

// Stable command failures shared by Owner adapters. Application aliases retain
// sentinel identity so existing errors.Is and HTTP contracts stay unchanged.
var (
	ErrInvalid             = errors.New("invalid AI Assistant command")
	ErrNotFound            = errors.New("AI Assistant record not found")
	ErrConflict            = errors.New("AI Assistant command conflict")
	ErrIdempotencyConflict = errors.New("AI Assistant idempotency key conflict")
)
