package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type rawRow map[string]any

func textField(r rawRow, k string) string {
	if r[k] == nil {
		return ""
	}
	switch v := r[k].(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	}
	return ""
}
func intField(r rawRow, k string) int64 { v, _ := strconv.ParseInt(textField(r, k), 10, 64); return v }
func timeField(r rawRow, k string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, textField(r, k))
}

// sourcePaidConfirmedAt only accepts an explicit provider payment fact. It
// intentionally does not fall back to updated_at: that timestamp may describe
// a later import, refund, or reconciliation rather than payment success.
func sourcePaidConfirmedAt(r rawRow) (*time.Time, error) {
	for _, key := range []string{"paid_confirmed_at", "paid_at"} {
		value := textField(r, key)
		if value == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return nil, errors.New("invalid source paid confirmation")
		}
		utc := at.UTC()
		return &utc, nil
	}
	return nil, nil
}
func tableRows(s snapshot, name string) ([]rawRow, error) {
	var rows []rawRow
	d := json.NewDecoder(bytes.NewReader(s.Tables[name]))
	d.UseNumber()
	if err := d.Decode(&rows); err != nil {
		return nil, errors.New("invalid raw table")
	}
	return rows, nil
}

type targetOrder struct {
	ID, Version                  int64
	Digest                       [32]byte
	Payer                        *int64
	Beneficiary                  *int64
	PayerIdentity                int64
	Provider, Merchant, Currency string
	Amount                       int64
	Created                      time.Time
	Items                        []ordermigration.ItemRow
}
type payerMatch struct {
	Customer, Identity int64
	Reference          identitydomain.Reference
}

func normalizeCapture(s snapshot, out, runKey string) error {
	if out == "" || runKey == "" {
		return errors.New("output-directory and run-key required")
	}
	sourceURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return errors.New("target read URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, sourceURL)
	if err != nil {
		return errors.New("target read connection failed")
	}
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return errors.New("target read transaction failed")
	}
	defer tx.Rollback(ctx)
	txctx := platformpostgres.BindTransaction(ctx, tx)
	targets := map[string]targetOrder{}
	rows, err := tx.Query(ctx, `SELECT o.id,o.source_key,o.source_row_digest,o.version,o.payer_customer_id,o.beneficiary_customer_id,COALESCE((SELECT r.payer_identity_id FROM order_history_attribution_receipts r WHERE r.order_id=o.id AND r.payer_customer_id=o.payer_customer_id AND r.outcome IN ('linked','already_linked') ORDER BY r.id DESC LIMIT 1),0) ,o.provider,o.merchant_order_no,o.amount_minor,o.currency,o.created_at,COALESCE((SELECT jsonb_agg(jsonb_build_object('line_no',i.line_no,'product_code',i.product_code,'product_name',i.product_name,'unit_amount_minor',i.unit_amount_minor,'quantity',i.quantity,'line_amount_minor',i.line_amount_minor) ORDER BY i.line_no) FROM order_items i WHERE i.order_id=o.id),'[]'::jsonb) FROM orders o WHERE o.source_system='commerce-history'`)
	if err != nil {
		return errors.New("target historical order evidence unavailable")
	}
	for rows.Next() {
		var k string
		var v targetOrder
		var digest, items []byte
		if rows.Scan(&v.ID, &k, &digest, &v.Version, &v.Payer, &v.Beneficiary, &v.PayerIdentity, &v.Provider, &v.Merchant, &v.Amount, &v.Currency, &v.Created, &items) != nil || len(digest) != 32 {
			rows.Close()
			return errors.New("invalid target historical evidence")
		}
		if json.Unmarshal(items, &v.Items) != nil {
			return errors.New("invalid immutable item evidence")
		}
		copy(v.Digest[:], digest)
		targets[k] = v
	}
	rows.Close()
	if rows.Err() != nil {
		return errors.New("target evidence read failed")
	}
	resolver := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	mapRows, err := tableRows(s, "wecom_external_contact_identity_map")
	if err != nil {
		return err
	}
	byUnion := map[string][]identitydomain.Reference{}
	for _, row := range mapRows {
		union, corp, external := textField(row, "unionid"), textField(row, "corp_id"), textField(row, "external_userid")
		if union != "" && corp != "" && external != "" && textField(row, "status") == "active" {
			byUnion[union] = append(byUnion[union], identitydomain.Reference{Kind: identitydomain.Kind("wecom_external_userid"), Scope: "wecom-corp:" + corp, Value: external, Assurance: identitydomain.AssuranceVerified, Source: "provider-history:verified-target-existing"})
		}
	}
	cache := map[string]payerMatch{}
	match := func(union string) (payerMatch, error) {
		if v, ok := cache[union]; ok {
			return v, nil
		}
		var found payerMatch
		for _, ref := range byUnion[union] {
			v, e := resolver.Resolve(txctx, ref)
			if e != nil {
				return payerMatch{}, errors.New("existing identity read failed")
			}
			if v.Status != identityport.ResolveFound {
				continue
			}
			if found.Customer != 0 && found.Customer != int64(v.CustomerID) {
				cache[union] = payerMatch{}
				return payerMatch{}, nil
			}
			if found.Identity == 0 || v.IdentityID < found.Identity {
				found = payerMatch{int64(v.CustomerID), v.IdentityID, ref}
			}
		}
		cache[union] = found
		return found, nil
	}
	manifest := ordermigration.Manifest{SchemaVersion: ordermigration.SchemaVersion, RunKey: runKey, Coverage: ordermigration.Coverage{Identities: true, WeChatPayOrders: true, WeChatPayRefunds: true, WeChatShopOrders: true, WeChatShopRefunds: true, AlipayOrders: true}}
	selectedIdentities := map[int64]ordermigration.IdentityRow{}
	subjects := map[int64][]string{}
	proofs := map[string]orderport.HistoricalDeltaPrecondition{}
	unresolved := 0
	missingRefundEvidence := 0
	frozenFieldConflicts := 0
	for _, table := range []string{"wechat_pay_orders", "wechat_shop_orders", "alipay_pay_orders"} {
		sourceRows, e := tableRows(s, table)
		if e != nil {
			return e
		}
		for _, raw := range sourceRows {
			order, e := normalizeOrder(table, raw)
			if e != nil {
				return fmt.Errorf("source order cannot normalize table=%s pk=%d reason=%s", table, intField(raw, "id"), e.Error())
			}
			v, e := match(textField(raw, "unionid"))
			if e != nil {
				return e
			}
			if old, ok := targets[order.SourceKey]; ok {
				if old.Provider != string(order.Provider) || old.Merchant != order.MerchantOrderNo || old.Amount != order.AmountMinor || old.Currency != order.Currency || !old.Created.Equal(order.CreatedAt) || !reflect.DeepEqual(old.Items, order.Items) {
					frozenFieldConflicts++
				}
				proofs[order.SourceKey] = orderport.HistoricalDeltaPrecondition{SourceDigest: old.Digest, Version: old.Version}
				// Prior Owner attribution is stronger than a fresh cross-system guess.
				if old.Payer != nil && (v.Customer == 0 || v.Customer != *old.Payer) {
					if old.PayerIdentity < 1 {
						return errors.New("historical payer lacks reusable attribution evidence")
					}
					var ref identitydomain.Reference
					var kind string
					if tx.QueryRow(ctx, `SELECT kind,scope_key,normalized_value FROM customer_identities WHERE id=$1 AND customer_id=$2 AND assurance='verified' AND status='active'`, old.PayerIdentity, *old.Payer).Scan(&kind, &ref.Scope, &ref.Value) != nil {
						return errors.New("prior payer identity unavailable")
					}
					ref.Kind = identitydomain.Kind(kind)
					ref.Assurance = identitydomain.AssuranceVerified
					ref.Source = "provider-history:verified-target-existing"
					r, e := resolver.Resolve(txctx, ref)
					if e != nil || r.Status != identityport.ResolveFound || int64(r.CustomerID) != *old.Payer || r.IdentityID != old.PayerIdentity {
						return errors.New("prior payer identity ownership changed")
					}
					v = payerMatch{*old.Payer, old.PayerIdentity, ref}
				}
				if old.Payer == nil {
					v = payerMatch{}
				} // Preserve known target's explicit unresolved state; separate attribution only.
				if old.Beneficiary != nil {
					return errors.New("unexpected prior beneficiary needs reviewed source evidence")
				}
			}
			if v.Customer > 0 {
				identityKey := "aicrm-production:existing-identity:" + strconv.FormatInt(v.Identity, 10)
				subjectKey := "aicrm-production:existing-customer:" + strconv.FormatInt(v.Customer, 10)
				if _, ok := selectedIdentities[v.Identity]; !ok {
					selectedIdentities[v.Identity] = ordermigration.IdentityRow{SourceKey: identityKey, Kind: string(v.Reference.Kind), Scope: v.Reference.Scope, Value: v.Reference.Value, Source: v.Reference.Source}
					subjects[v.Customer] = append(subjects[v.Customer], identityKey)
				}
				order.PayerIdentityKey = identityKey
				order.PayerSubjectKey = subjectKey
			} else {
				unresolved++
				d := sha256.Sum256([]byte(table + "\x00" + textField(raw, "id") + "\x00" + textField(raw, "unionid")))
				manifest.IdentityQuarantines = append(manifest.IdentityQuarantines, ordermigration.IdentityQuarantineRow{SourceKey: order.SourceKey + ":identity", ReasonCode: "existing_identity_unconfirmed", EvidenceDigest: "sha256:" + hex.EncodeToString(d[:])})
			}
			if table == "wechat_shop_orders" && textField(raw, "business_status") == "returned" {
				missingRefundEvidence++
			}
			manifest.Orders = append(manifest.Orders, order)
		}
	}
	for _, table := range []string{"wechat_pay_refunds", "wechat_shop_refunds"} {
		sourceRows, e := tableRows(s, table)
		if e != nil {
			return e
		}
		for _, raw := range sourceRows {
			r, e := normalizeRefund(table, raw)
			if e != nil {
				return fmt.Errorf("source refund cannot normalize table=%s pk=%d", table, intField(raw, "id"))
			}
			manifest.Refunds = append(manifest.Refunds, r)
		}
	}
	for _, v := range selectedIdentities {
		manifest.Identities = append(manifest.Identities, v)
	}
	sort.Slice(manifest.Identities, func(i, j int) bool { return manifest.Identities[i].SourceKey < manifest.Identities[j].SourceKey })
	for customer, keys := range subjects {
		sort.Strings(keys)
		manifest.Subjects = append(manifest.Subjects, ordermigration.SubjectRow{SourceKey: "aicrm-production:existing-customer:" + strconv.FormatInt(customer, 10), IdentityKeys: keys})
	}
	sort.Slice(manifest.Subjects, func(i, j int) bool { return manifest.Subjects[i].SourceKey < manifest.Subjects[j].SourceKey })
	present := map[string]bool{}
	for _, o := range manifest.Orders {
		present[o.SourceKey] = true
	}
	for key := range targets {
		if !present[key] {
			return errors.New("historical target missing from complete source")
		}
	}
	// Serialize even when the current Owner contract rejects unresolved history;
	// readiness is explicit, and no import is invoked by this read-only command.
	validationErr := manifest.Validate(true)
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return errors.New("manifest encoding failed")
	}
	digest := sha256.Sum256(payload)
	proof, _ := json.MarshalIndent(map[string]any{"manifest_sha256": hex.EncodeToString(digest[:]), "orders": proofs}, "", "  ")
	summary := map[string]any{"orders": len(manifest.Orders), "refunds": len(manifest.Refunds), "existing_subjects": len(manifest.Subjects), "existing_identities": len(manifest.Identities), "unresolved_orders": unresolved, "shop_refund_evidence_missing": missingRefundEvidence, "target_preconditions": len(proofs), "frozen_field_conflicts": frozenFieldConflicts, "manifest_sha256": hex.EncodeToString(digest[:]), "normalized_manifest_ready": validationErr == nil && frozenFieldConflicts == 0, "provider_effects_created": 0}
	if err = os.Mkdir(out, 0700); err != nil {
		return errors.New("normalization requires new output directory")
	}
	if err = writeExclusive(filepath.Join(out, "manifest.json"), payload); err != nil {
		return err
	}
	if err = writeExclusive(filepath.Join(out, "preconditions.json"), proof); err != nil {
		return err
	}
	audit, _ := json.MarshalIndent(summary, "", "  ")
	if err = writeExclusive(filepath.Join(out, "evidence.json"), audit); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(summary)
}

func normalizeOrder(table string, r rawRow) (ordermigration.OrderRow, error) {
	id := intField(r, "id")
	created, e := timeField(r, "created_at")
	if e != nil || id < 1 {
		return ordermigration.OrderRow{}, errors.New("invalid source order")
	}
	updated, e := timeField(r, "updated_at")
	if e != nil {
		return ordermigration.OrderRow{}, errors.New("invalid updated_at")
	}
	o := ordermigration.OrderRow{SourceKey: "aicrm-production:" + table + ":" + strconv.FormatInt(id, 10), MerchantOrderNo: textField(r, "out_trade_no"), ProviderTransactionNo: textField(r, "transaction_id"), AmountMinor: intField(r, "amount_total"), Currency: textField(r, "currency"), CreatedAt: created, UpdatedAt: updated}
	quantity := int64(1)
	switch table {
	case "wechat_pay_orders":
		o.SourceStatus = textField(r, "status")
		o.Provider = orderdomain.ProviderWeChatPay
		switch textField(r, "status") {
		case "paid":
			o.Status = "paid"
		case "failed":
			o.Status = "payment_failed"
		case "closed":
			o.Status = "closed"
		case "pending", "created", "unpaid":
			o.Status = "pending_payment"
		default:
			return o, errors.New("unknown source order status")
		}
	case "wechat_shop_orders":
		o.SourceStatus = textField(r, "business_status")
		if o.SourceStatus == "returned" {
			o.HistoryReason = "refund_evidence_missing"
		}
		o.Provider = orderdomain.ProviderWeChatShop
		o.MerchantOrderNo = textField(r, "order_id")
		quantity = intField(r, "product_count")
		switch textField(r, "business_status") {
		case "deal", "returned":
			o.Status = "paid"
		case "closed":
			o.Status = "closed"
		default:
			return o, errors.New("unknown source shop status")
		}
	case "alipay_pay_orders":
		return o, errors.New("nonzero alipay needs audited mapping")
	}
	refunded := intField(r, "refunded_amount_total")
	if refunded > 0 {
		if o.Status != "paid" || refunded > o.AmountMinor {
			return o, errors.New("invalid refund total")
		}
		o.Status = "partially_refunded"
		if refunded == o.AmountMinor {
			o.Status = "refunded"
		}
	}
	confirmedAt, e := sourcePaidConfirmedAt(r)
	if e != nil {
		return o, e
	}
	if confirmedAt != nil {
		if o.Status != "paid" && o.Status != "partially_refunded" && o.Status != "refunded" {
			return o, errors.New("paid confirmation on non-paid source order")
		}
		if confirmedAt.Before(o.CreatedAt) || confirmedAt.After(o.UpdatedAt) {
			return o, errors.New("source paid confirmation outside order timeline")
		}
		o.PaidConfirmedAt = confirmedAt
	}
	if quantity < 1 || quantity > 2147483647 || o.AmountMinor%quantity != 0 {
		return o, errors.New("invalid quantity")
	}
	o.Items = []ordermigration.ItemRow{{LineNo: 1, ProductCode: textField(r, "product_code"), ProductName: textField(r, "product_name"), UnitAmountMinor: o.AmountMinor / quantity, Quantity: int32(quantity), LineAmountMinor: o.AmountMinor}}
	return o, nil
}
func normalizeRefund(table string, r rawRow) (ordermigration.RefundRow, error) {
	id := intField(r, "id")
	occurred, e := timeField(r, "created_at")
	if e != nil || id < 1 {
		return ordermigration.RefundRow{}, errors.New("invalid refund")
	}
	v := ordermigration.RefundRow{Provider: orderdomain.ProviderWeChatPay, SourceKey: "aicrm-production:" + table + ":" + strconv.FormatInt(id, 10), Status: textField(r, "status"), MerchantOrderNo: textField(r, "out_trade_no"), RefundNo: textField(r, "out_refund_no"), ProviderRefundNo: textField(r, "refund_id"), AmountMinor: intField(r, "refund_amount_total"), Reason: strings.TrimSpace(textField(r, "reason")), OccurredAt: occurred}
	if table == "wechat_shop_refunds" {
		return v, errors.New("nonzero shop refunds require audited mapping")
	}
	if v.Reason == "" {
		v.Reason = "历史退款"
	}
	if !v.ValidStatus() {
		return v, errors.New("unknown refund status")
	}
	return v, nil
}
