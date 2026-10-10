package sidebar

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const warehouseGenerationContract = "crm-sidebar-manual-generation.v1"
const warehouseGenerationPath = "/api/local/v3/sidebar-manual-generation"
const warehouseGenerationTimeout = 35 * time.Second

// HTTPWarehouseGenerator invokes the migrated native Agent only for the
// server-authorized, frozen bilateral snapshot. Its owning Agent keeps the
// durable request receipt. This client performs one POST and never retries.
type HTTPWarehouseGenerator struct {
	transport *HTTPWarehouseReader
}

func NewHTTPWarehouseGenerator(origin, username, password string, client *http.Client) (*HTTPWarehouseGenerator, error) {
	r, err := NewHTTPWarehouseReader(origin, username, password, client)
	if err != nil {
		return nil, err
	}
	r.endpoint.Path = warehouseGenerationPath
	r.client.Timeout = warehouseGenerationTimeout
	if client != nil && client.Timeout > 0 && client.Timeout < warehouseGenerationTimeout {
		r.client.Timeout = client.Timeout
	}
	return &HTTPWarehouseGenerator{transport: r}, nil
}

func (g *HTTPWarehouseGenerator) GenerateSidebarSuggestion(ctx context.Context, input AISuggestionInput) (string, error) {
	s := input.Snapshot
	if s.CustomerID < 1 || s.CorpID != input.Principal.CorpID || s.EmployeeID != input.Principal.EmployeeID || s.CorpID == "" || s.EmployeeID == "" ||
		s.SourceVersion == "" || len(input.IdempotencyKey) < 1 || len(input.IdempotencyKey) > 2048 {
		return "", errWarehouseContract
	}
	// Snapshot's public JSON intentionally omits dialogue and viewer identifiers.
	// This private server-to-Agent envelope explicitly includes both for checking
	// against the currently published employee-scoped source before/after calling.
	private := map[string]any{"customer_id": s.CustomerID, "corp_id": s.CorpID, "employee_id": s.EmployeeID,
		"source_version": s.SourceVersion, "summary": s.Summary, "summary_updated_at": s.SummaryUpdatedAt,
		"synced_at": s.SyncedAt, "messages": s.Messages, "last_customer_message": s.LastCustomerMessage}
	body, err := json.Marshal(map[string]any{"contract": warehouseGenerationContract, "idempotency_key": input.IdempotencyKey, "snapshot": private})
	if err != nil || len(body) > warehouseResponseLimit {
		return "", errWarehouseContract
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.transport.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", errWarehouseContract
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(g.transport.username, g.transport.password)
	response, err := g.transport.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errWarehouseContract
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errWarehouseContract
	}
	result, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(result) > 256<<10 {
		return "", errWarehouseContract
	}
	var wire struct {
		OK                   bool   `json:"ok"`
		Contract             string `json:"contract"`
		Text                 string `json:"text"`
		SourceVersion        string `json:"source_version"`
		AutomaticSendAllowed *bool  `json:"automatic_send_allowed"`
	}
	if json.Unmarshal(result, &wire) != nil || !wire.OK || wire.Contract != warehouseGenerationContract || wire.SourceVersion != s.SourceVersion ||
		wire.AutomaticSendAllowed == nil || *wire.AutomaticSendAllowed || strings.TrimSpace(wire.Text) == "" || len(wire.Text) > 32000 {
		return "", errWarehouseContract
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return wire.Text, nil
}

var _ AISuggestionGenerator = (*HTTPWarehouseGenerator)(nil)
