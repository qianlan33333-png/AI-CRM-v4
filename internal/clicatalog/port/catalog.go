// Package port defines transport metadata. It does not authorize or execute business operations.
package port

import (
	_ "embed"
	"encoding/json"
)

type Binding struct {
	Method              string           `json:"method"`
	Path                string           `json:"path"`
	Auth                string           `json:"auth"`
	Capability          string           `json:"capability,omitempty"`
	Scope               string           `json:"scope,omitempty"`
	Parameters          []map[string]any `json:"parameters,omitempty"`
	Body                map[string]any   `json:"body,omitempty"`
	ContentType         string           `json:"content_type,omitempty"`
	IdempotencyRequired bool             `json:"idempotency_required"`
	BodyRequired        bool             `json:"body_required"`
	CursorOnly          bool             `json:"cursor_only,omitempty"`
}

type Operation struct {
	ID             string         `json:"id"`
	Group          string         `json:"group"`
	Module         string         `json:"module"`
	Command        string         `json:"command"`
	Summary        string         `json:"summary"`
	Owner          string         `json:"owner"`
	Execution      string         `json:"execution"`
	Implementation string         `json:"implementation"`
	Evidence       string         `json:"evidence"`
	Version        int            `json:"version"`
	Responses      map[string]any `json:"responses,omitempty"`
	Prerequisites  []string       `json:"prerequisites"`
	Binding        *Binding       `json:"binding,omitempty"`
	BlockedReason  string         `json:"blocked_reason,omitempty"`
}

type Module struct {
	Category string   `json:"category"`
	Group    string   `json:"group"`
	Name     string   `json:"name"`
	Sections []string `json:"sections"`
}

type Catalog struct {
	Version     int            `json:"version"`
	Modules     []Module       `json:"modules"`
	Operations  []Operation    `json:"operations"`
	Schemas     map[string]any `json:"schemas"`
	CLIContract map[string]any `json:"cli_contract"`
}

//go:embed catalog.json
var catalogJSON []byte

// Load returns a fresh copy so caller annotations cannot change the registry.
func Load() Catalog {
	var catalog Catalog
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic("invalid compiled CLI catalog")
	}
	return catalog
}

func Find(id string) (Operation, bool) {
	for _, op := range Load().Operations {
		if op.ID == id {
			return op, true
		}
	}
	return Operation{}, false
}
