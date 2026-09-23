package port

import (
	"context"
	"time"
)

type PublishedLinkMutation struct {
	ReceiptID                   int64
	Operation, LinkID, LinkName string
	UserIDs                     []string
	DepartmentIDs               []int64
	SkipVerify                  bool
}
type PublishedLinkMutationReader interface {
	ReadPublishedLinkMutation(context.Context, string) (PublishedLinkMutation, error)
}

type LinkMutationCompletion struct {
	EffectRef, State, LinkID, URL, OutcomeDigest         string
	BusinessEndpointDispatched, RealExternalCallExecuted bool
	CompletedAt                                          time.Time
}
type LinkMutationCompletionWriter interface {
	CompleteLinkMutation(context.Context, LinkMutationCompletion) error
}
