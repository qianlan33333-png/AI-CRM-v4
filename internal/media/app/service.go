package app

import platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"

// Service is the read-side application boundary for Media.
//
// The donor's write service also used this name, but its implementation
// depends on v2's event and receipt ports.  This preparation slice intentionally
// keeps only read/transform behavior; transactional write composition is left
// to the v3 Media owner/Terra integration.  The store is asserted by each
// read use case to its Media-local contract, so no shared platform store or
// cross-domain dependency is introduced here.
type Service struct {
	uow   platformport.UnitOfWork
	store any
}

// NewReadService constructs the Media read-side service.  The supplied store
// must implement the local contract required by the selected read operation
// (ImageListStore, FacetStore, ImageDetailStore, ImageVariantStore, or
// mediaport.ImageMetadataReader).
func NewReadService(uow platformport.UnitOfWork, store any) *Service {
	return &Service{uow: uow, store: store}
}
