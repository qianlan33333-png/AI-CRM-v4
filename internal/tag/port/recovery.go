package port

import "context"

// CatalogMutationRecovery is a Tag-owned operation, including archived rows.
// Its ID selects the original immutable dispatch, never a replacement write.
type CatalogMutationRecovery struct {
	Generation int64                    `json:"generation"`
	ID         int64                    `json:"id"`
	Operation  CatalogMutationOperation `json:"operation"`
	Name       string                   `json:"name"`
	State      string                   `json:"state"`
}
type CatalogMutationRecoveryStore interface {
	ListCatalogMutationRecoveries(context.Context) ([]CatalogMutationRecovery, error)
	LockCatalogMutationRecovery(context.Context, int64) (CatalogMutationDispatch, error)
}
