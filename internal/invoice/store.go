package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	invoiceport "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type Repository struct{}

func (Repository) officialResponse(ctx context.Context, id string, artifact effects.ResultArtifact) error {
	if artifact.Kind != invoiceport.OfficialResponseArtifact || !artifact.Valid() {
		return invoiceport.ErrInvalid
	}
	var response invoiceport.OfficialResponse
	if json.Unmarshal(artifact.Payload, &response) != nil || response.HTTPStatus < 400 || response.HTTPStatus > 599 || response.Code == "" || len(response.Code) > 64 || len(response.Message) > 4096 {
		return invoiceport.ErrInvalid
	}
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO invoice_audit_events(application_id,event_type,evidence_digest,material) VALUES($1,'official_response',$2,$3)`, id, string(artifact.Digest), artifact.Payload)
	return e
}

const columns = `id,order_id,customer_id,identity_id,invoice_id,state,card_state,snapshot,buyer,refund_confirmed,issue_effect,reverse_effect,blue_number,red_number,reverse_attempt,issue_attempt,dispatch_attempt,updated_at`

func scan(row interface{ Scan(...any) error }) (invoiceport.Application, error) {
	var a invoiceport.Application
	var s, b []byte
	e := row.Scan(&a.ID, &a.OrderID, &a.CustomerID, &a.IdentityID, &a.InvoiceID, &a.State, &a.CardState, &s, &b, &a.RefundConfirmed, &a.IssueEffect, &a.ReverseEffect, &a.BlueNumber, &a.RedNumber, &a.ReverseAttempt, &a.IssueAttempt, &a.DispatchAttempt, &a.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return a, invoiceport.ErrNotFound
	}
	if e != nil {
		return a, e
	}
	if json.Unmarshal(s, &a.Snapshot) != nil || json.Unmarshal(b, &a.Buyer) != nil {
		return a, invoiceport.ErrUnavailable
	}
	return a, nil
}
func (Repository) get(ctx context.Context, id string, ids []int64, lock bool) (invoiceport.Application, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return invoiceport.Application{}, e
	}
	q := `SELECT ` + columns + ` FROM invoice_applications WHERE id=$1`
	args := []any{id}
	if ids != nil {
		if len(ids) == 0 {
			return invoiceport.Application{}, invoiceport.ErrNotFound
		}
		q += ` AND customer_id=ANY($2)`
		args = append(args, ids)
	}
	if lock {
		q += ` FOR UPDATE`
	}
	return scan(tx.QueryRow(ctx, q, args...))
}
func (Repository) byOrder(ctx context.Context, id int64) (invoiceport.Application, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return invoiceport.Application{}, e
	}
	return scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM invoice_applications WHERE order_id=$1`, id))
}
func (Repository) update(ctx context.Context, a invoiceport.Application, event, evidence string) error {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	buyer, e := json.Marshal(a.Buyer)
	if e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `UPDATE invoice_applications SET state=$2,card_state=$3,buyer=$4,refund_confirmed=$5,issue_effect=$6,reverse_effect=$7,blue_number=$8,red_number=$9,reverse_attempt=$10,invoice_id=$11,issue_attempt=$12,dispatch_attempt=$13,updated_at=now() WHERE id=$1`, a.ID, a.State, a.CardState, buyer, a.RefundConfirmed, a.IssueEffect, a.ReverseEffect, a.BlueNumber, a.RedNumber, a.ReverseAttempt, a.InvoiceID, a.IssueAttempt, a.DispatchAttempt)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return invoiceport.ErrNotFound
	}
	_, e = tx.Exec(ctx, `INSERT INTO invoice_audit_events(application_id,event_type,evidence_digest)VALUES($1,$2,$3)`, a.ID, event, evidence)
	return e
}

// recover is called with the application and its Order locked. Cascade keeps all
// existing owner receipts/audit attached; the material preserves original facts.
func (Repository) recover(ctx context.Context, a *invoiceport.Application, targetID string, fresh invoiceport.Snapshot, proof invoiceport.Result) error {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	material, e := json.Marshal(struct {
		PreviousID        string               `json:"previous_application_id"`
		PreviousInvoiceID string               `json:"previous_invoice_id"`
		Before            invoiceport.Snapshot `json:"before"`
		After             invoiceport.Snapshot `json:"after"`
	}{a.ID, a.InvoiceID, a.Snapshot, fresh})
	if e != nil {
		return e
	}
	raw, e := json.Marshal(fresh)
	if e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `UPDATE invoice_applications SET id=$2,snapshot=$3,updated_at=now() WHERE id=$1 AND state='failed' AND invoice_id=$4 AND blue_number=''`, a.ID, targetID, raw, a.InvoiceID)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return invoiceport.ErrConflict
	}
	a.ID = targetID
	a.Snapshot = fresh
	_, e = tx.Exec(ctx, `INSERT INTO invoice_audit_events(application_id,event_type,evidence_digest,material)VALUES($1,'failed_recovery',$2,$3)`, a.ID, string(effects.Hash("invoice.recovery", string(material), proof.InvoiceID, proof.Status)), material)
	return e
}
