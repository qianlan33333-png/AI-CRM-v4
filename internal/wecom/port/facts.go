// Package port contains verified WeCom protocol facts that may cross domain boundaries.
package port

import (
	"context"
	"strings"
)

// VerifiedExternalContact is produced only after a trusted WeCom/JSSDK or
// callback adapter has verified the provider context. HTTP request bodies may
// not construct this value directly.
type VerifiedExternalContact struct {
	CorpID         string
	ExternalUserID string
	Source         string
}

func (fact VerifiedExternalContact) Valid() bool {
	return strings.TrimSpace(fact.CorpID) == fact.CorpID && fact.CorpID != "" &&
		strings.TrimSpace(fact.ExternalUserID) == fact.ExternalUserID && fact.ExternalUserID != "" &&
		strings.TrimSpace(fact.Source) == fact.Source && fact.Source != ""
}

// TagCatalogReader is a narrow, read-only connector contract. It contains no
// customer target, mark/unmark, credential, or general Provider-write method.
type TagCatalogReader interface {
	ListTagCatalog(context.Context) ([]TagCatalogGroup, error)
}

type TagCatalogGroup struct {
	ID    string
	Name  string
	Order int32
	Tags  []TagCatalogTag
}

type TagCatalogTag struct {
	ID      string
	Name    string
	Order   int32
	Deleted bool
}

// TagCatalogMutation is an Outbound-only provider write request. Both IDs are
// Tag-owned provider bindings, never customer or identity values.
type TagCatalogMutation struct {
	Operation                      string
	GroupName, TagName             string
	ProviderGroupID, ProviderTagID string
}

type TagCatalogMutationResult struct {
	ProviderGroupID, ProviderTagID string
}

type TagCatalogMutationWriter interface {
	MutateTagCatalog(context.Context, TagCatalogMutation) (TagCatalogMutationResult, error)
}

// ExternalContactDescriptionUpdate is an Outbound-only provider request for
// one existing follow relationship. It carries no customer identity: the
// caller must freeze and persist that local identity separately before it may
// resolve these provider identifiers at execution time.
type ExternalContactDescriptionUpdate struct {
	EmployeeID     string
	ExternalUserID string
	Description    string
}

// ExternalContactDescriptionWriter is intentionally a one-operation port.
// The WeCom adapter can implement the leaf, but only Outbound may receive it
// from composition and invoke it through an external effect.
type ExternalContactDescriptionWriter interface {
	UpdateExternalContactDescription(context.Context, ExternalContactDescriptionUpdate) error
}

// ProviderCallFailure identifies only whether the catalog request itself may
// have crossed a network boundary. It never carries Provider status/body data.
type ProviderCallFailure interface {
	error
	ProviderCallAttempted() bool
}
