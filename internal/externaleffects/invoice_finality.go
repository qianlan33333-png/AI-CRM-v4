package externaleffects

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// Only a verified Invoice query can close an unknown invoice attempt. SKIP
// LOCKED avoids inverting the completion sink's effect -> owner lock order.
// Active attempts retain their original fence and are never retried here.
func (r *Repository) ReconcileInvoiceWithin(ctx context.Context, ref string, evidence p.Digest) error {
	return r.invoiceFinality(ctx, ref, evidence, false)
}
func (r *Repository) CancelInvoiceWithin(ctx context.Context, ref string, evidence p.Digest) error {
	return r.invoiceFinality(ctx, ref, evidence, true)
}
func (r *Repository) invoiceFinality(ctx context.Context, ref string, evidence p.Digest, cancel bool) error {
	if !p.ValidDigest(evidence) {
		return ErrInvalid
	}
	id, e := parseEffectID(ref)
	if e != nil {
		return e
	}
	tx, e := pg.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	var owner, kind, state string
	var generation, fence int64
	var expired bool
	e = tx.QueryRow(ctx, `SELECT owner,kind,state,generation,lease_fence,coalesce(lease_expires_at<=clock_timestamp(),false) FROM external_effects WHERE id=$1 FOR UPDATE SKIP LOCKED`, id).Scan(&owner, &kind, &state, &generation, &fence, &expired)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if owner != string(p.OwnerInvoice) || (kind != string(p.KindInvoiceIssue) && kind != string(p.KindInvoiceReverse)) {
		return ErrInvalid
	}
	next := "reconciled"
	if cancel {
		if state != "queued" && state != "accepted" {
			return nil
		}
		next = "cancelled"
	} else {
		if state != "outcome_unknown" || !expired {
			return nil
		}
		tag, e := tx.Exec(ctx, `UPDATE external_effect_attempts SET state='reconciled',evidence_digest=$4,completed_at=clock_timestamp() WHERE effect_id=$1 AND generation=$2 AND fence=$3 AND state='outcome_unknown'`, id, generation, fence, evidence)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrReconcileRequired
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE external_effects SET state=$2,updated_at=clock_timestamp() WHERE id=$1`, id, next); e != nil {
		return e
	}
	operation := "reconcile"
	if cancel {
		operation = "cancel"
	}
	receipt := p.Hash("invoice.finality", ref, operation, string(evidence))
	_, e = tx.Exec(ctx, `INSERT INTO external_effect_operation_receipts(operation,effect_id,receipt_key_digest,command_digest,state)VALUES($1,$2,$3,$3,$4)`, "complete", id, receipt, next)
	return e
}
