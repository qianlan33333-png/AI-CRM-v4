package sidebar

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

const warehouseSidebarContract = "crm-sidebar-ai-operations.v1"
const warehousePrivateScopeContract = "crm-private-message-scope.v1"
const warehouseSidebarPath = "/api/local/v3/sidebar-ai-operations"

// Covers the existing 40 * 16000-byte dialogue contract and 32000-byte summary
// even when JSON escapes every byte, while keeping the response bounded.
const warehouseResponseLimit = 4 << 20

var errWarehouseContract = errors.New("warehouse scoped read unavailable")

// HTTPWarehouseReader consumes the Context Center's protected, scoped read
// contract. It never falls back to the customer-wide workbench chat projection,
// triggers a refresh, or opens a CRM/warehouse database connection.
type HTTPWarehouseReader struct {
	endpoint *url.URL
	username string
	password string
	client   *http.Client
}

// The optional client is for composition/transport tests. Runtime configuration
// requires HTTPS; HTTP is accepted only with an explicitly injected client.
func NewHTTPWarehouseReader(origin, username, password string, client *http.Client) (*HTTPWarehouseReader, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && client != nil)) ||
		strings.TrimSpace(username) != username || username == "" || strings.ContainsAny(username, ":\r\n") || password == "" {
		return nil, errWarehouseContract
	}
	u.Path = warehouseSidebarPath
	u.RawPath = ""
	c := http.Client{Timeout: aiReadTimeout}
	if client != nil {
		c = *client
		if c.Timeout == 0 || c.Timeout > aiReadTimeout {
			c.Timeout = aiReadTimeout
		}
	}
	// A redirect must never carry a credential or scoped identifiers to another
	// service, including a redirect on the same origin.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPWarehouseReader{endpoint: u, username: username, password: password, client: &c}, nil
}

func (reader *HTTPWarehouseReader) ReadAIOperations(ctx context.Context, p Principal, id customerdomain.CustomerID) (AIWarehouseSnapshot, error) {
	if id < 1 || p.CorpID == "" || p.EmployeeID == "" || strings.TrimSpace(p.CorpID) != p.CorpID || strings.TrimSpace(p.EmployeeID) != p.EmployeeID {
		return AIWarehouseSnapshot{}, errWarehouseContract
	}
	u := *reader.endpoint
	q := url.Values{}
	q.Set("customer_id", strconv.FormatInt(int64(id), 10))
	q.Set("corp_id", p.CorpID)
	q.Set("employee_id", p.EmployeeID)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return AIWarehouseSnapshot{}, errWarehouseContract
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(reader.username, reader.password)
	resp, err := reader.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return AIWarehouseSnapshot{}, ctx.Err()
		}
		return AIWarehouseSnapshot{}, errWarehouseContract
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return AIWarehouseSnapshot{}, errWarehouseContract
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, warehouseResponseLimit+1))
	if err != nil || len(body) > warehouseResponseLimit {
		return AIWarehouseSnapshot{}, errWarehouseContract
	}
	var wire struct {
		OK                  bool   `json:"ok"`
		Contract            string `json:"contract"`
		MessageScopeVersion string `json:"message_scope_version"`
		Snapshot            struct {
			CustomerID          customerdomain.CustomerID `json:"customer_id"`
			CorpID              string                    `json:"corp_id"`
			EmployeeID          string                    `json:"employee_id"`
			SourceVersion       string                    `json:"source_version"`
			Summary             string                    `json:"summary"`
			SummaryUpdatedAt    *time.Time                `json:"summary_updated_at"`
			SyncedAt            *time.Time                `json:"synced_at"`
			Messages            []AIMessage               `json:"messages"`
			LastCustomerMessage *AIMessage                `json:"last_customer_message"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || !wire.OK || wire.Contract != warehouseSidebarContract || wire.MessageScopeVersion != warehousePrivateScopeContract ||
		wire.Snapshot.CustomerID != id || wire.Snapshot.CorpID != p.CorpID || wire.Snapshot.EmployeeID != p.EmployeeID ||
		wire.Snapshot.SourceVersion == "" || wire.Snapshot.SyncedAt == nil || wire.Snapshot.SyncedAt.IsZero() || wire.Snapshot.SyncedAt.After(time.Now().Add(2*time.Minute)) ||
		len(wire.Snapshot.Messages) > 40 || len(wire.Snapshot.Summary) > 32000 {
		return AIWarehouseSnapshot{}, errWarehouseContract
	}
	if err := ctx.Err(); err != nil {
		return AIWarehouseSnapshot{}, err
	}
	s := wire.Snapshot
	return AIWarehouseSnapshot{CustomerID: s.CustomerID, CorpID: s.CorpID, EmployeeID: s.EmployeeID, SourceVersion: s.SourceVersion,
		Summary: s.Summary, SummaryUpdatedAt: s.SummaryUpdatedAt, SyncedAt: s.SyncedAt, Messages: s.Messages, LastCustomerMessage: s.LastCustomerMessage}, nil
}

var _ AIWarehouseReader = (*HTTPWarehouseReader)(nil)
