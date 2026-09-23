// Package port defines the machine-facing host boundary. The composition root
// adapts each operation to its owning domain Port; this package never imports a
// business module's app, store, HTTP handler, or tables.
package port

import (
	"context"
	"net/http"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type Request struct {
	Method    string
	Path      string
	PathParts map[string]string
	Query     map[string][]string
	Body      []byte
	Principal accessdomain.MachinePrincipal
}

type Response struct {
	Status int
	Body   any
	Header http.Header
}

// Executor is implemented only by a composition-owned adapter. It is the
// narrow bridge to existing domain Ports and preserves their UoW, idempotency,
// approval, River, and External Effects semantics.
type Executor interface {
	Execute(context.Context, Request) (Response, error)
}
