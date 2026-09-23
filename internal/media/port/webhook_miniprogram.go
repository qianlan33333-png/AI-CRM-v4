package port

import (
	"context"
	"errors"
)

// WebhookMiniProgramRequest is the caller-declared card metadata after the
// Group Ops application has accepted only a valid plan and bound target set.
// The Media owner decides whether this AppID/path has an approved cover
// source; callers never supply a cover URL or provider media ID.
type WebhookMiniProgramRequest struct {
	AppID string
	Path  string
	Title string
}

// PreparedWebhookMiniProgram is deliberately transient. Its bytes are a
// verified Media provider-read result, not a local material ID or a Provider
// media credential. Group Ops passes it back to this Media Port unchanged.
type PreparedWebhookMiniProgram struct {
	AppID  string
	Path   string
	Title  string
	PNG    []byte
	Width  int32
	Height int32
}

// WebhookMiniProgramMaterialization identifies one immutable local Media
// mutation. It is called only from the Group Ops UoW after the plan lock,
// run reservation, replay check and binding recheck have succeeded.
type WebhookMiniProgramMaterialization struct {
	Actor          int64
	IdempotencyKey string
}

var (
	// ErrWebhookMiniProgramUnsupported means the caller requested an automatic
	// cover outside Media's explicitly approved AppID/path contract. It is an
	// input rejection and must happen before any provider read.
	ErrWebhookMiniProgramUnsupported = errors.New("webhook miniprogram cover unsupported")
	// ErrWebhookMiniProgramUnavailable means an approved source could not be
	// fetched or verified safely. It never causes a Group Ops run to be saved.
	ErrWebhookMiniProgramUnavailable = errors.New("webhook miniprogram cover unavailable")
)

// WebhookMiniProgramResolver is a Media-owned restricted provider-read and
// local-materialization Port. Prepare never opens a transaction. Materialize
// requires the caller's existing transaction and must not start another one.
type WebhookMiniProgramResolver interface {
	PrepareWebhookMiniProgram(context.Context, WebhookMiniProgramRequest) (PreparedWebhookMiniProgram, error)
	MaterializeWebhookMiniProgramWithin(context.Context, PreparedWebhookMiniProgram, WebhookMiniProgramMaterialization) (GroupOpsMaterialReference, error)
}
