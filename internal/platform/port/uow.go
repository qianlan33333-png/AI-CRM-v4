// Package port contains platform contracts that never import business domains.
package port

import "context"

// UnitOfWork supplies a transaction-bound context to a callback.
// Implementations must reject nested transactions unless explicitly designed otherwise.
type UnitOfWork interface {
	Within(context.Context, func(context.Context) error) error
}
