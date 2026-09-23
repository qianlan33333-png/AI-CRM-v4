// Package store owns Order PostgreSQL persistence. Every operation requires a
// transaction-bound context supplied by the shared platform Unit of Work.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrInvalid = errors.New("invalid order persistence request")

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}

func (r *Repository) Within(ctx context.Context, fn func(context.Context) error) error {
	if r == nil || r.uow == nil || fn == nil {
		return ErrInvalid
	}
	return r.uow.Within(ctx, fn)
}

func transaction(ctx context.Context) (pgx.Tx, error) {
	return platformpostgres.RequireTransaction(ctx)
}

type rowScanner interface{ Scan(...any) error }

const orderColumns = `id,provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at`

func scanOrder(row rowScanner) (domain.Snapshot, error) {
	var snapshot domain.Snapshot
	err := row.Scan(
		&snapshot.ID, &snapshot.Provider, &snapshot.SourceSystem, &snapshot.SourceKey,
		&snapshot.MerchantOrderNo, &snapshot.ProviderTransactionNo,
		&snapshot.PayerCustomerID, &snapshot.BeneficiaryCustomerID,
		&snapshot.Amount.AmountMinor, &snapshot.RefundedMinor, &snapshot.Amount.Currency,
		&snapshot.Status, &snapshot.RecordOrigin, &snapshot.EffectEligible,
		&snapshot.Version, &snapshot.CreatedAt, &snapshot.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	if err != nil {
		return domain.Snapshot{}, mapError(err)
	}
	return snapshot, nil
}

func (r *Repository) Reserve(ctx context.Context, reservation orderapp.Reservation) (orderapp.Receipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderapp.Receipt{}, false, err
	}
	if reservation.Operation == "" || reservation.ActorScope == "" || reservation.CreatedAt.IsZero() {
		return orderapp.Receipt{}, false, ErrInvalid
	}
	var receipt orderapp.Receipt
	err = tx.QueryRow(ctx, `INSERT INTO order_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,created_at)
VALUES($1,$2,$3,$4,'in_progress',$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING
RETURNING id`, reservation.Operation, reservation.ActorScope, reservation.KeyDigest[:], reservation.PayloadDigest[:], reservation.CreatedAt.UTC()).Scan(&receipt.ID)
	if err == nil {
		receipt.Operation, receipt.ActorScope, receipt.KeyDigest, receipt.PayloadDigest, receipt.State = reservation.Operation, reservation.ActorScope, reservation.KeyDigest, reservation.PayloadDigest, "in_progress"
		return receipt, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return orderapp.Receipt{}, false, mapError(err)
	}
	var keyRaw, payloadRaw, result []byte
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_scope,key_digest,payload_digest,state,COALESCE(result_snapshot,'null'::jsonb) FROM order_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3 FOR UPDATE`, reservation.Operation, reservation.ActorScope, reservation.KeyDigest[:]).Scan(
		&receipt.ID, &receipt.Operation, &receipt.ActorScope, &keyRaw, &payloadRaw, &receipt.State, &result,
	)
	if err == nil {
		if len(keyRaw) != 32 || len(payloadRaw) != 32 {
			return orderapp.Receipt{}, false, orderport.ErrUnavailable
		}
		copy(receipt.KeyDigest[:], keyRaw)
		copy(receipt.PayloadDigest[:], payloadRaw)
		if string(result) != "null" {
			receipt.ResultSnapshot = append(json.RawMessage(nil), result...)
		}
	}
	return receipt, false, mapError(err)
}

func (r *Repository) Complete(ctx context.Context, id int64, snapshot json.RawMessage, completedAt time.Time) (orderapp.Receipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderapp.Receipt{}, err
	}
	if id < 1 || !json.Valid(snapshot) || completedAt.IsZero() {
		return orderapp.Receipt{}, ErrInvalid
	}
	var receipt orderapp.Receipt
	var keyRaw, payloadRaw, result []byte
	err = tx.QueryRow(ctx, `UPDATE order_operation_receipts SET state='completed',result_snapshot=$2,completed_at=$3 WHERE id=$1 AND state='in_progress' RETURNING id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot`, id, snapshot, completedAt.UTC()).Scan(
		&receipt.ID, &receipt.Operation, &receipt.ActorScope, &keyRaw, &payloadRaw, &receipt.State, &result,
	)
	if err == nil {
		if len(keyRaw) != 32 || len(payloadRaw) != 32 || !json.Valid(result) {
			return orderapp.Receipt{}, orderport.ErrUnavailable
		}
		copy(receipt.KeyDigest[:], keyRaw)
		copy(receipt.PayloadDigest[:], payloadRaw)
		receipt.ResultSnapshot = append(json.RawMessage(nil), result...)
	}
	return receipt, mapError(err)
}

func (r *Repository) Insert(ctx context.Context, order domain.Order, actor int64, now time.Time) (domain.Order, error) {
	if actor < 1 {
		return domain.Order{}, ErrInvalid
	}
	return r.insert(ctx, order, "admin:"+strconv.FormatInt(actor, 10), now, nil, "order.created")
}

func (r *Repository) InsertScoped(ctx context.Context, order domain.Order, actorScope string, now time.Time) (domain.Order, error) {
	if actorScope == "" || len(actorScope) > 200 || strings.TrimSpace(actorScope) != actorScope {
		return domain.Order{}, ErrInvalid
	}
	return r.insert(ctx, order, actorScope, now, nil, "order.created")
}

func (r *Repository) insert(ctx context.Context, order domain.Order, actorScope string, now time.Time, sourceDigest []byte, eventType string) (domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Order{}, err
	}
	snapshot := order.Snapshot()
	if snapshot.ID != 0 || actorScope == "" || now.IsZero() {
		return domain.Order{}, ErrInvalid
	}
	err = tx.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id`, snapshot.Provider, snapshot.SourceSystem, snapshot.SourceKey, snapshot.MerchantOrderNo, snapshot.ProviderTransactionNo, snapshot.PayerCustomerID, snapshot.BeneficiaryCustomerID, snapshot.Amount.AmountMinor, snapshot.RefundedMinor, snapshot.Amount.Currency, snapshot.Status, snapshot.RecordOrigin, snapshot.EffectEligible, sourceDigest, snapshot.Version, snapshot.CreatedAt, snapshot.UpdatedAt).Scan(&snapshot.ID)
	if err != nil {
		return domain.Order{}, mapError(err)
	}
	for _, item := range snapshot.Items {
		if _, err = tx.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, snapshot.ID, item.LineNo, item.ProductID, item.ProductVersion, item.ProductCode, item.ProductName, item.UnitAmountMinor, item.Quantity, item.LineAmountMinor); err != nil {
			return domain.Order{}, mapError(err)
		}
	}
	if err = r.appendFacts(ctx, tx, snapshot, nil, actorScope, eventType, now); err != nil {
		return domain.Order{}, err
	}
	return domain.Restore(snapshot)
}

func (r *Repository) Get(ctx context.Context, id int64, forUpdate bool) (domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Order{}, err
	}
	if id < 1 {
		return domain.Order{}, orderport.ErrNotFound
	}
	query := `SELECT ` + orderColumns + ` FROM orders WHERE id=$1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	snapshot, err := scanOrder(tx.QueryRow(ctx, query, id))
	if err != nil {
		return domain.Order{}, err
	}
	snapshot.Items, err = loadItems(ctx, tx, id)
	if err != nil {
		return domain.Order{}, err
	}
	order, err := domain.Restore(snapshot)
	if err != nil {
		return domain.Order{}, orderport.ErrUnavailable
	}
	return order, nil
}

func (r *Repository) ReservePaymentWithin(ctx context.Context, id int64) (domain.Snapshot, error) {
	order, err := r.Get(ctx, id, true)
	if err != nil {
		return domain.Snapshot{}, err
	}
	snapshot := order.Snapshot()
	if snapshot.RecordOrigin != domain.RecordOriginNative || !snapshot.EffectEligible || snapshot.Status != domain.StatusPendingPayment {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	return snapshot, nil
}

func (r *Repository) List(ctx context.Context, before *orderapp.Cursor, limit int32, filter orderapp.ListFilter) ([]domain.Order, error) {
	if limit > orderapp.MaximumLimit+1 {
		return nil, ErrInvalid
	}
	return r.queryOrders(ctx, before, limit, filter)
}

func (r *Repository) Export(ctx context.Context, filter orderapp.ListFilter, limit int32) ([]domain.Order, error) {
	if limit < 1 || limit > 10001 {
		return nil, ErrInvalid
	}
	return r.queryOrders(ctx, nil, limit, filter)
}

func (r *Repository) Count(ctx context.Context, filter orderapp.ListFilter) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	query, args := orderFilterSQL(filter)
	var total int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM orders`+query, args...).Scan(&total)
	return total, mapError(err)
}

func (r *Repository) ReadPaidProductOrdersWithin(ctx context.Context, keys []orderport.ProductSalesKey) ([]orderport.ProductOrderFact, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(keys))
	codes := make([]string, 0, len(keys))
	for _, key := range keys {
		if key.ProductID < 1 || key.ProductCode == "" || strings.TrimSpace(key.ProductCode) != key.ProductCode {
			return nil, ErrInvalid
		}
		ids = append(ids, key.ProductID)
		codes = append(codes, key.ProductCode)
	}
	if len(ids) == 0 {
		return []orderport.ProductOrderFact{}, nil
	}
	rows, err := tx.Query(ctx, `
SELECT DISTINCT o.id,oi.product_id,oi.product_code,
       (o.status IN ('partially_refunded','refunded') OR o.refunded_minor>0) AS order_refunded
FROM orders o
JOIN order_items oi ON oi.order_id=o.id
WHERE (oi.product_id=ANY($1::bigint[]) OR (oi.product_id IS NULL AND oi.product_code=ANY($2::text[])))
  AND EXISTS (
    SELECT 1 FROM order_status_history h
    WHERE h.order_id=o.id AND h.to_status IN ('paid','partially_refunded','refunded')
  )
ORDER BY o.id,oi.product_id NULLS LAST,oi.product_code`, ids, codes)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := make([]orderport.ProductOrderFact, 0)
	for rows.Next() {
		var fact orderport.ProductOrderFact
		if err = rows.Scan(&fact.OrderID, &fact.ProductID, &fact.ProductCode, &fact.OrderRefunded); err != nil {
			return nil, mapError(err)
		}
		result = append(result, fact)
	}
	return result, mapError(rows.Err())
}

func (r *Repository) InsertContactSnapshot(ctx context.Context, orderID int64, ciphertext []byte, keyVersion int16, createdAt time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if orderID < 1 || len(ciphertext) < 28 || keyVersion != 1 || createdAt.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO order_contact_snapshots(order_id,phone_ciphertext,key_version,created_at) VALUES($1,$2,$3,$4)`, orderID, ciphertext, keyVersion, createdAt.UTC())
	return mapError(err)
}

func (r *Repository) InsertShippingAddressSnapshot(ctx context.Context, orderID int64, address orderport.ShippingAddress, createdAt time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if orderID < 1 || address.RecipientName == "" || address.ProvinceCode == "" || address.ProvinceName == "" || address.CityCode == "" || address.CityName == "" || address.DistrictCode == "" || address.DistrictName == "" || address.DetailAddress == "" || createdAt.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO order_shipping_address_snapshots(order_id,recipient_name,province_code,province_name,city_code,city_name,district_code,district_name,detail_address,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, orderID, address.RecipientName, address.ProvinceCode, address.ProvinceName, address.CityCode, address.CityName, address.DistrictCode, address.DistrictName, address.DetailAddress, createdAt.UTC())
	return mapError(err)
}

func (r *Repository) ReadShippingAddressSnapshot(ctx context.Context, orderID int64) (orderport.ShippingAddress, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderport.ShippingAddress{}, false, err
	}
	var address orderport.ShippingAddress
	err = tx.QueryRow(ctx, `SELECT recipient_name,province_code,province_name,city_code,city_name,district_code,district_name,detail_address FROM order_shipping_address_snapshots WHERE order_id=$1`, orderID).Scan(&address.RecipientName, &address.ProvinceCode, &address.ProvinceName, &address.CityCode, &address.CityName, &address.DistrictCode, &address.DistrictName, &address.DetailAddress)
	if errors.Is(err, pgx.ErrNoRows) {
		return orderport.ShippingAddress{}, false, nil
	}
	if err != nil {
		return orderport.ShippingAddress{}, false, mapError(err)
	}
	return address, true, nil
}

func orderFilterSQL(filter orderapp.ListFilter) (string, []any) {
	args := []any{}
	conditions := []string{}
	add := func(template string, value any) {
		args = append(args, value)
		conditions = append(conditions, strings.ReplaceAll(template, "?", "$"+strconv.Itoa(len(args))))
	}
	if filter.Provider != "" {
		add(`provider=?`, filter.Provider)
	}
	if filter.Status != "" {
		add(`status=?`, filter.Status)
	}
	if filter.OrderRef != "" {
		args = append(args, filter.OrderRef)
		placeholder := "$" + strconv.Itoa(len(args))
		conditions = append(conditions, `(merchant_order_no=`+placeholder+` OR provider_transaction_no=`+placeholder+` OR source_key=`+placeholder+`)`)
	}
	if filter.CustomerID > 0 {
		args = append(args, filter.CustomerID)
		placeholder := "$" + strconv.Itoa(len(args))
		conditions = append(conditions, `(payer_customer_id=`+placeholder+` OR beneficiary_customer_id=`+placeholder+`)`)
	}
	if filter.NoCustomerMatch {
		conditions = append(conditions, `FALSE`)
	}
	if filter.Product != "" {
		args = append(args, "%"+filter.Product+"%")
		placeholder := "$" + strconv.Itoa(len(args))
		conditions = append(conditions, `EXISTS (SELECT 1 FROM order_items oi WHERE oi.order_id=orders.id AND (oi.product_code ILIKE `+placeholder+` OR oi.product_name ILIKE `+placeholder+`))`)
	}
	if filter.CreatedFrom != nil {
		add(`created_at>=?`, filter.CreatedFrom.UTC())
	}
	if filter.CreatedTo != nil {
		add(`created_at<?`, filter.CreatedTo.UTC())
	}
	if filter.CreatedThrough != nil {
		add(`created_at<=?`, filter.CreatedThrough.UTC())
	}
	if len(conditions) == 0 {
		return "", args
	}
	return ` WHERE ` + strings.Join(conditions, ` AND `), args
}

func (r *Repository) queryOrders(ctx context.Context, before *orderapp.Cursor, limit int32, filter orderapp.ListFilter) ([]domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, ErrInvalid
	}
	query := `SELECT ` + orderColumns + ` FROM orders`
	filterSQL, filterArgs := orderFilterSQL(filter)
	query += filterSQL
	args := filterArgs
	conditions := []string{}
	if before != nil {
		if before.ID < 1 || before.CreatedAt.IsZero() {
			return nil, ErrInvalid
		}
		args = append(args, before.CreatedAt.UTC(), before.ID)
		conditions = append(conditions, `(created_at,id) < ($`+strconv.Itoa(len(args)-1)+`,$`+strconv.Itoa(len(args))+`)`)
	}
	if len(conditions) > 0 {
		if filterSQL == "" {
			query += ` WHERE ` + strings.Join(conditions, ` AND `)
		} else {
			query += ` AND ` + strings.Join(conditions, ` AND `)
		}
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	if before == nil && filter.Offset > 0 {
		query += ` OFFSET $` + strconv.Itoa(len(args)+1)
		args = append(args, filter.Offset)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	snapshots := make([]domain.Snapshot, 0)
	for rows.Next() {
		snapshot, scanErr := scanOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		snapshots = append(snapshots, snapshot)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	orders := make([]domain.Order, 0, len(snapshots))
	for _, snapshot := range snapshots {
		snapshot.Items, err = loadItems(ctx, tx, snapshot.ID)
		if err != nil {
			return nil, err
		}
		order, restoreErr := domain.Restore(snapshot)
		if restoreErr != nil {
			return nil, orderport.ErrUnavailable
		}
		orders = append(orders, order)
	}
	return orders, nil
}

func (r *Repository) FindByReference(ctx context.Context, reference string) ([]domain.Order, error) {
	return r.findByReference(ctx, "", reference)
}

// FindByReferenceForProvider is the exact read path for a list row that
// carries both Payment identity components. Merchant order numbers alone are
// not globally unique across providers.
func (r *Repository) FindByReferenceForProvider(ctx context.Context, provider domain.Provider, reference string) ([]domain.Order, error) {
	if provider == "" {
		return nil, ErrInvalid
	}
	return r.findByReference(ctx, provider, reference)
}

func (r *Repository) findByReference(ctx context.Context, provider domain.Provider, reference string) ([]domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if reference == "" || len(reference) > 200 {
		return nil, ErrInvalid
	}
	query := `SELECT ` + orderColumns + ` FROM orders WHERE (merchant_order_no=$1 OR provider_transaction_no=$1 OR source_key=$1)`
	args := []any{reference}
	if provider != "" {
		query += ` AND provider=$2`
		args = append(args, provider)
	}
	query += ` ORDER BY id LIMIT 2`
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	// pgx does not allow another query on the same transaction while Rows is
	// still open. Materialize the matching order facts first, then load the
	// item snapshots after the result set has been closed.
	snapshots := make([]domain.Snapshot, 0, 2)
	for rows.Next() {
		snapshot, scanErr := scanOrder(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		snapshots = append(snapshots, snapshot)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, mapError(err)
	}
	rows.Close()

	result := make([]domain.Order, 0, len(snapshots))
	for _, snapshot := range snapshots {
		snapshot.Items, err = loadItems(ctx, tx, snapshot.ID)
		if err != nil {
			return nil, err
		}
		order, restoreErr := domain.Restore(snapshot)
		if restoreErr != nil {
			return nil, orderport.ErrUnavailable
		}
		result = append(result, order)
	}
	return result, nil
}

func (r *Repository) RecordHistoricalAttribution(ctx context.Context, command orderport.HistoricalAttributionCommand) (orderport.HistoricalAttributionResult, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderport.HistoricalAttributionResult{}, err
	}
	if command.RunID < 1 || command.SourceKey == "" || len(command.SourceKey) > 200 || strings.TrimSpace(command.SourceKey) != command.SourceKey || command.OrderReference == "" || len(command.OrderReference) > 200 || strings.TrimSpace(command.OrderReference) != command.OrderReference || command.EvidenceDigest == ([32]byte{}) || command.OccurredAt.IsZero() || !validAttributionInput(command) {
		return orderport.HistoricalAttributionResult{}, ErrInvalid
	}
	result, found, err := existingAttribution(ctx, tx, command)
	if err != nil || found {
		return result, err
	}

	result.Outcome = command.Outcome
	if command.Outcome == orderport.AttributionLinked {
		ids, findErr := orderIDsByReference(ctx, tx, command.OrderReference)
		if findErr != nil {
			return orderport.HistoricalAttributionResult{}, findErr
		}
		switch len(ids) {
		case 0:
			result.Outcome = orderport.AttributionOrderNotFound
		case 1:
			result.OrderID = ids[0]
			current, getErr := r.Get(ctx, ids[0], true)
			if getErr != nil {
				return orderport.HistoricalAttributionResult{}, getErr
			}
			if current.PayerCustomerID != nil && *current.PayerCustomerID != command.PayerCustomerID {
				result.Outcome = orderport.AttributionOrderPayerConflict
				break
			}
			updated, changed, attributeErr := current.AttributeHistoricalPayer(current.Version, command.PayerCustomerID, command.OccurredAt.UTC())
			if attributeErr != nil {
				return orderport.HistoricalAttributionResult{}, orderport.ErrConflict
			}
			result.PayerCustomerID, result.PayerIdentityID = command.PayerCustomerID, command.PayerIdentityID
			if !changed {
				result.Outcome = orderport.AttributionAlreadyLinked
				break
			}
			snapshot := updated.Snapshot()
			update, updateErr := tx.Exec(ctx, `UPDATE orders SET payer_customer_id=$2,version=$3,updated_at=$4 WHERE id=$1 AND version=$5 AND payer_customer_id IS NULL AND record_origin='history' AND effect_eligible=FALSE`, snapshot.ID, snapshot.PayerCustomerID, snapshot.Version, snapshot.UpdatedAt, current.Version)
			if updateErr != nil {
				return orderport.HistoricalAttributionResult{}, mapError(updateErr)
			}
			if update.RowsAffected() != 1 {
				return orderport.HistoricalAttributionResult{}, orderport.ErrConflict
			}
			payload, _ := json.Marshal(map[string]any{"order_id": snapshot.ID, "payer_customer_id": command.PayerCustomerID, "payer_identity_id": command.PayerIdentityID, "record_origin": snapshot.RecordOrigin, "version": snapshot.Version})
			actorScope := "migration:history-attribution:" + strconv.FormatInt(command.RunID, 10)
			if _, updateErr = tx.Exec(ctx, `INSERT INTO order_audit_events(event_type,order_id,actor_scope,payload,occurred_at) VALUES('order.payer_attributed',$1,$2,$3,$4)`, snapshot.ID, actorScope, payload, command.OccurredAt.UTC()); updateErr != nil {
				return orderport.HistoricalAttributionResult{}, mapError(updateErr)
			}
			idempotencyKey := "order.payer_attributed:" + strconv.FormatInt(snapshot.ID, 10) + ":" + strconv.FormatInt(snapshot.Version, 10)
			if _, updateErr = tx.Exec(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES('order.payer_attributed',$1,$2,$3,$4)`, idempotencyKey, snapshot.ID, payload, command.OccurredAt.UTC()); updateErr != nil {
				return orderport.HistoricalAttributionResult{}, mapError(updateErr)
			}
		default:
			result.Outcome = orderport.AttributionOrderReferenceConflict
		}
	}
	if result.Outcome != orderport.AttributionLinked && result.Outcome != orderport.AttributionAlreadyLinked {
		result.PayerCustomerID, result.PayerIdentityID = 0, 0
	}
	if err = insertAttributionReceipt(ctx, tx, command, result); err != nil {
		return orderport.HistoricalAttributionResult{}, err
	}
	return result, nil
}

func validAttributionInput(command orderport.HistoricalAttributionCommand) bool {
	switch command.Outcome {
	case orderport.AttributionLinked:
		return command.PayerCustomerID > 0 && command.PayerIdentityID > 0
	case orderport.AttributionSourceIdentityMissing, orderport.AttributionSourceIdentityNotFound, orderport.AttributionSourceIdentityAmbiguous, orderport.AttributionTargetIdentityNotFound, orderport.AttributionTargetIdentityConflict:
		return command.PayerCustomerID == 0 && command.PayerIdentityID == 0
	default:
		return false
	}
}

func existingAttribution(ctx context.Context, tx pgx.Tx, command orderport.HistoricalAttributionCommand) (orderport.HistoricalAttributionResult, bool, error) {
	var result orderport.HistoricalAttributionResult
	var orderID, customerID, identityID *int64
	var digest []byte
	err := tx.QueryRow(ctx, `SELECT outcome,order_id,payer_customer_id,payer_identity_id,evidence_digest FROM order_history_attribution_receipts WHERE run_id=$1 AND source_key=$2`, command.RunID, command.SourceKey).Scan(&result.Outcome, &orderID, &customerID, &identityID, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, mapError(err)
	}
	if len(digest) != 32 || string(digest) != string(command.EvidenceDigest[:]) {
		return orderport.HistoricalAttributionResult{}, false, orderport.ErrConflict
	}
	if orderID != nil {
		result.OrderID = *orderID
	}
	if customerID != nil {
		result.PayerCustomerID = *customerID
	}
	if identityID != nil {
		result.PayerIdentityID = *identityID
	}
	result.Replayed = true
	return result, true, nil
}

func orderIDsByReference(ctx context.Context, tx pgx.Tx, reference string) ([]int64, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM orders WHERE merchant_order_no=$1 OR provider_transaction_no=$1 OR source_key=$1 ORDER BY id LIMIT 2 FOR UPDATE`, reference)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	ids := make([]int64, 0, 2)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}
	return ids, mapError(rows.Err())
}

func insertAttributionReceipt(ctx context.Context, tx pgx.Tx, command orderport.HistoricalAttributionCommand, result orderport.HistoricalAttributionResult) error {
	var orderID, customerID, identityID any
	if result.OrderID > 0 {
		orderID = result.OrderID
	}
	if result.PayerCustomerID > 0 {
		customerID = result.PayerCustomerID
	}
	if result.PayerIdentityID > 0 {
		identityID = result.PayerIdentityID
	}
	_, err := tx.Exec(ctx, `INSERT INTO order_history_attribution_receipts(run_id,source_key,order_reference,evidence_digest,outcome,order_id,payer_customer_id,payer_identity_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, command.RunID, command.SourceKey, command.OrderReference, command.EvidenceDigest[:], result.Outcome, orderID, customerID, identityID)
	return mapError(err)
}

func (r *Repository) RecordExport(ctx context.Context, receipt orderapp.ExportReceipt) (orderapp.ExportReceipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderapp.ExportReceipt{}, false, err
	}
	if receipt.Actor < 1 || receipt.RowCount < 0 || receipt.RowCount > 10000 || receipt.ByteCount < 0 || receipt.ByteCount > 5<<20 || receipt.KeyDigest == ([32]byte{}) || receipt.FilterDigest == ([32]byte{}) || receipt.ContentDigest == ([32]byte{}) || receipt.CreatedAt.IsZero() {
		return orderapp.ExportReceipt{}, false, ErrInvalid
	}
	err = tx.QueryRow(ctx, `INSERT INTO order_export_receipts(actor_admin_user_id,key_digest,filter_digest,row_count,byte_count,content_digest,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(actor_admin_user_id,key_digest) DO NOTHING RETURNING id`, receipt.Actor, receipt.KeyDigest[:], receipt.FilterDigest[:], receipt.RowCount, receipt.ByteCount, receipt.ContentDigest[:], receipt.CreatedAt.UTC()).Scan(&receipt.ID)
	if err == nil {
		return receipt, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return orderapp.ExportReceipt{}, false, mapError(err)
	}
	var key, filter, content []byte
	stored := orderapp.ExportReceipt{}
	err = tx.QueryRow(ctx, `SELECT id,actor_admin_user_id,key_digest,filter_digest,row_count,byte_count,content_digest,created_at FROM order_export_receipts WHERE actor_admin_user_id=$1 AND key_digest=$2 FOR UPDATE`, receipt.Actor, receipt.KeyDigest[:]).Scan(&stored.ID, &stored.Actor, &key, &filter, &stored.RowCount, &stored.ByteCount, &content, &stored.CreatedAt)
	if err != nil || len(key) != 32 || len(filter) != 32 || len(content) != 32 {
		return orderapp.ExportReceipt{}, false, mapError(err)
	}
	copy(stored.KeyDigest[:], key)
	copy(stored.FilterDigest[:], filter)
	copy(stored.ContentDigest[:], content)
	return stored, false, nil
}

// AppendPaidEvent persists the first native paid event next to the Order state
// transition. It is idempotent by the immutable order/version pair and writes
// the Order outbox fact before returning the event to the composition consumer.
func (r *Repository) AppendPaidEvent(ctx context.Context, snapshot domain.Snapshot) (orderport.PaidEvent, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderport.PaidEvent{}, false, err
	}
	if snapshot.ID < 1 || snapshot.Version < 2 || snapshot.Status != domain.StatusPaid ||
		snapshot.RecordOrigin != domain.RecordOriginNative || !snapshot.EffectEligible || snapshot.UpdatedAt.IsZero() {
		return orderport.PaidEvent{}, false, ErrInvalid
	}
	source := orderport.NewPaidEventSourceDigest(snapshot.ID, snapshot.Version)
	var event orderport.PaidEvent
	var returnedSource []byte
	err = tx.QueryRow(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at)
VALUES($1,$2,$3,$4)
ON CONFLICT(order_id) DO NOTHING
RETURNING id,order_id,order_version,source_digest,occurred_at`, snapshot.ID, snapshot.Version, source[:], snapshot.UpdatedAt.UTC()).Scan(
		&event.ID, &event.OrderID, &event.OrderVersion, &returnedSource, &event.OccurredAt,
	)
	created := err == nil
	if created {
		if len(returnedSource) != 32 || string(returnedSource) != string(source[:]) {
			return orderport.PaidEvent{}, false, ErrInvalid
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var stored []byte
		err = tx.QueryRow(ctx, `SELECT id,order_id,order_version,source_digest,occurred_at FROM order_paid_events WHERE order_id=$1 FOR UPDATE`, snapshot.ID).Scan(
			&event.ID, &event.OrderID, &event.OrderVersion, &stored, &event.OccurredAt,
		)
		if err == nil {
			if len(stored) != 32 || event.OrderVersion != snapshot.Version || string(stored) != string(source[:]) {
				return orderport.PaidEvent{}, false, orderport.ErrConflict
			}
			copy(source[:], stored)
		}
	}
	if err != nil {
		return orderport.PaidEvent{}, false, mapError(err)
	}
	event.SourceDigest, event.Order = source, snapshot
	if !event.ValidOrderFact() {
		return orderport.PaidEvent{}, false, ErrInvalid
	}
	checkout, checkoutErr := r.ReadCheckoutSnapshot(ctx, event.OrderID)
	if checkoutErr == nil {
		event.CheckoutProductID, event.CheckoutGrossAmountMinor = checkout.ProductID, checkout.GrossAmountMinor
		event.CheckoutProductType = checkout.ProductType
		event.CheckoutPayableAmountMinor = checkout.PayableAmountMinor
		event.ReferralActivityContextDigest = checkout.ReferralActivityContextDigest
		event.PromotionContextDigest = checkout.PromotionContextDigest
	} else if !errors.Is(checkoutErr, orderport.ErrNotFound) {
		return orderport.PaidEvent{}, false, checkoutErr
	}
	key := "order.paid.v1:" + strconv.FormatInt(event.ID, 10)
	if created {
		payload, marshalErr := json.Marshal(map[string]any{"order_id": event.OrderID, "order_version": event.OrderVersion, "paid_event_id": event.ID})
		if marshalErr != nil {
			return orderport.PaidEvent{}, false, ErrInvalid
		}
		if err = tx.QueryRow(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at)
VALUES('order.paid.v1',$1,$2,$3::jsonb,$4) RETURNING id`, key, event.OrderID, payload, event.OccurredAt.UTC()).Scan(&event.DomainEventOutboxID); err != nil {
			return orderport.PaidEvent{}, false, mapError(err)
		}
	} else if err = tx.QueryRow(ctx, `SELECT id FROM order_outbox
WHERE event_type='order.paid.v1' AND idempotency_key=$1 AND aggregate_id=$2 FOR KEY SHARE`, key, event.OrderID).Scan(&event.DomainEventOutboxID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return orderport.PaidEvent{}, false, orderport.ErrConflict
		}
		return orderport.PaidEvent{}, false, mapError(err)
	}
	if !event.Valid() {
		return orderport.PaidEvent{}, false, ErrInvalid
	}
	return event, created, nil
}

// PaymentConfirmationOccurredAtWithin exposes only the immutable timestamp
// of an existing native paid event to Order app. It is intentionally not an
// insert/update path for Payment reconciliation.
func (r *Repository) PaymentConfirmationOccurredAtWithin(ctx context.Context, orderID int64) (time.Time, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if orderID < 1 {
		return time.Time{}, ErrInvalid
	}
	var occurredAt time.Time
	if err = tx.QueryRow(ctx, `SELECT occurred_at FROM order_paid_events WHERE order_id=$1 FOR KEY SHARE`, orderID).Scan(&occurredAt); err != nil {
		return time.Time{}, mapError(err)
	}
	if occurredAt.IsZero() {
		return time.Time{}, ErrInvalid
	}
	return occurredAt.UTC(), nil
}

func (r *Repository) UpdateSettlement(ctx context.Context, order domain.Order, event domain.StatusEvent, actorScope string) (domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Order{}, err
	}
	snapshot := order.Snapshot()
	if snapshot.ID < 1 || snapshot.Version < 2 || actorScope == "" || event.Version != snapshot.Version {
		return domain.Order{}, ErrInvalid
	}
	command, err := tx.Exec(ctx, `UPDATE orders SET status=$2,refunded_minor=$3,provider_transaction_no=$4,version=$5,updated_at=$6 WHERE id=$1 AND version=$7`, snapshot.ID, snapshot.Status, snapshot.RefundedMinor, snapshot.ProviderTransactionNo, snapshot.Version, snapshot.UpdatedAt, snapshot.Version-1)
	if err != nil {
		return domain.Order{}, mapError(err)
	}
	if command.RowsAffected() != 1 {
		return domain.Order{}, orderport.ErrConflict
	}
	if err = r.appendFacts(ctx, tx, snapshot, &event.From, actorScope, "order.status_changed", event.OccurredAt); err != nil {
		return domain.Order{}, err
	}
	return domain.Restore(snapshot)
}

func (r *Repository) Import(ctx context.Context, runKey string, digest [32]byte, order domain.Order) (domain.Order, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Order{}, false, err
	}
	if runKey == "" || digest == ([32]byte{}) || order.RecordOrigin != domain.RecordOriginHistory || order.EffectEligible {
		return domain.Order{}, false, ErrInvalid
	}
	var runID int64
	if err = tx.QueryRow(ctx, `SELECT id FROM order_import_runs WHERE run_key=$1 AND status='applying' FOR UPDATE`, runKey).Scan(&runID); err != nil {
		return domain.Order{}, false, mapError(err)
	}
	var receiptDigest []byte
	var receiptOrderID int64
	err = tx.QueryRow(ctx, `SELECT source_row_digest,order_id FROM order_import_receipts WHERE run_id=$1 AND source_system=$2 AND source_key=$3`, runID, order.SourceSystem, order.SourceKey).Scan(&receiptDigest, &receiptOrderID)
	if err == nil {
		if string(receiptDigest) != string(digest[:]) {
			return domain.Order{}, false, orderport.ErrConflict
		}
		persisted, getErr := r.Get(ctx, receiptOrderID, false)
		return persisted, false, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, false, mapError(err)
	}
	var existingID int64
	var existingDigest []byte
	err = tx.QueryRow(ctx, `SELECT id,source_row_digest FROM orders WHERE source_system=$1 AND source_key=$2`, order.SourceSystem, order.SourceKey).Scan(&existingID, &existingDigest)
	if err == nil {
		if string(existingDigest) != string(digest[:]) {
			return domain.Order{}, false, orderport.ErrConflict
		}
		if _, err = tx.Exec(ctx, `INSERT INTO order_import_receipts(run_id,source_system,source_key,source_row_digest,outcome,order_id) VALUES($1,$2,$3,$4,'replayed',$5)`, runID, order.SourceSystem, order.SourceKey, digest[:], existingID); err != nil {
			return domain.Order{}, false, mapError(err)
		}
		persisted, getErr := r.Get(ctx, existingID, false)
		return persisted, false, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, false, mapError(err)
	}
	persisted, err := r.insert(ctx, order, "migration:"+runKey, order.CreatedAt, digest[:], "order.history_imported")
	if err != nil {
		return domain.Order{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_import_receipts(run_id,source_system,source_key,source_row_digest,outcome,order_id) VALUES($1,$2,$3,$4,'imported',$5)`, runID, order.SourceSystem, order.SourceKey, digest[:], persisted.ID); err != nil {
		return domain.Order{}, false, mapError(err)
	}
	return persisted, true, nil
}

func (r *Repository) appendFacts(ctx context.Context, tx pgx.Tx, snapshot domain.Snapshot, from *domain.Status, actorScope, eventType string, occurredAt time.Time) error {
	var fromValue any
	if from != nil {
		fromValue = string(*from)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, snapshot.ID, fromValue, snapshot.Status, snapshot.RefundedMinor, snapshot.Version, actorScope, occurredAt.UTC()); err != nil {
		return mapError(err)
	}
	payload, _ := json.Marshal(map[string]any{"order_id": snapshot.ID, "status": snapshot.Status, "version": snapshot.Version, "record_origin": snapshot.RecordOrigin})
	if _, err := tx.Exec(ctx, `INSERT INTO order_audit_events(event_type,order_id,actor_scope,payload,occurred_at) VALUES($1,$2,$3,$4,$5)`, eventType, snapshot.ID, actorScope, payload, occurredAt.UTC()); err != nil {
		return mapError(err)
	}
	idempotencyKey := eventType + ":" + strconv.FormatInt(snapshot.ID, 10) + ":" + strconv.FormatInt(snapshot.Version, 10)
	_, err := tx.Exec(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES($1,$2,$3,$4,$5)`, eventType, idempotencyKey, snapshot.ID, payload, occurredAt.UTC())
	return mapError(err)
}

func loadItems(ctx context.Context, tx pgx.Tx, orderID int64) ([]domain.ItemSnapshot, error) {
	rows, err := tx.Query(ctx, `SELECT line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor FROM order_items WHERE order_id=$1 ORDER BY line_no`, orderID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	items := make([]domain.ItemSnapshot, 0)
	for rows.Next() {
		var item domain.ItemSnapshot
		if err = rows.Scan(&item.LineNo, &item.ProductID, &item.ProductVersion, &item.ProductCode, &item.ProductName, &item.UnitAmountMinor, &item.Quantity, &item.LineAmountMinor); err != nil {
			return nil, mapError(err)
		}
		items = append(items, item)
	}
	return items, mapError(rows.Err())
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return orderport.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23514" || pgErr.Code == "23503") {
		return orderport.ErrConflict
	}
	return err
}

// CommercePushDeliveryReference resolves the compatibility route using only
// Order-owned rows. The returned historical coordinates are never derived
// from orders.id; a V2 numeric order id can be visible only when it is the
// exact imported Order source kind/scope/key under the commerce-history
// scope.
func (r *Repository) CommercePushDeliveryReference(ctx context.Context, provider domain.Provider, reference string) (orderport.CommercePushDeliveryReference, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderport.CommercePushDeliveryReference{}, err
	}
	if provider == "" || reference == "" || len(reference) > 200 || strings.TrimSpace(reference) != reference {
		return orderport.CommercePushDeliveryReference{}, orderport.ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT o.id,o.record_origin,o.effect_eligible,o.source_system,o.source_key,COALESCE(p.id,0)
FROM orders o
LEFT JOIN order_paid_events p ON p.order_id=o.id
WHERE o.provider=$1 AND (o.merchant_order_no=$2 OR o.provider_transaction_no=$2 OR o.source_key=$2)
ORDER BY o.id LIMIT 2`, provider, reference)
	if err != nil {
		return orderport.CommercePushDeliveryReference{}, mapError(err)
	}
	defer rows.Close()
	type candidate struct {
		id, paidEventID         int64
		recordOrigin            domain.RecordOrigin
		effectEligible          bool
		sourceSystem, sourceKey string
	}
	var matches []candidate
	for rows.Next() {
		var candidate candidate
		if err = rows.Scan(&candidate.id, &candidate.recordOrigin, &candidate.effectEligible, &candidate.sourceSystem, &candidate.sourceKey, &candidate.paidEventID); err != nil {
			return orderport.CommercePushDeliveryReference{}, mapError(err)
		}
		matches = append(matches, candidate)
	}
	if err = rows.Err(); err != nil {
		return orderport.CommercePushDeliveryReference{}, mapError(err)
	}
	if len(matches) == 0 {
		return orderport.CommercePushDeliveryReference{}, orderport.ErrNotFound
	}
	if len(matches) != 1 {
		return orderport.CommercePushDeliveryReference{}, orderport.ErrConflict
	}
	match := matches[0]
	out := orderport.CommercePushDeliveryReference{OrderID: match.id}
	switch match.recordOrigin {
	case domain.RecordOriginNative:
		if !match.effectEligible {
			return orderport.CommercePushDeliveryReference{}, orderport.ErrConflict
		}
		out.PaidEventID, out.HistoricalMappingState = match.paidEventID, "current"
	case domain.RecordOriginHistory:
		if match.sourceSystem == "commerce-history" && match.sourceKey != "" {
			out.HistoricalSourceKind, out.HistoricalSourceSystem, out.HistoricalSourceKey, out.HistoricalMappingState = "wechat_pay_order", match.sourceSystem, match.sourceKey, "mapped"
		} else {
			out.HistoricalMappingState = "pending"
		}
	default:
		return orderport.CommercePushDeliveryReference{}, orderport.ErrConflict
	}
	return out, nil
}

func (r *Repository) ReadContactSnapshot(ctx context.Context, orderID int64) ([]byte, int16, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	var ciphertext []byte
	var version int16
	err = tx.QueryRow(ctx, `SELECT phone_ciphertext,key_version FROM order_contact_snapshots WHERE order_id=$1`, orderID).Scan(&ciphertext, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, mapError(err)
	}
	return ciphertext, version, true, nil
}
