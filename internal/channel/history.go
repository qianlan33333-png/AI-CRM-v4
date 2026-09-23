package channel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type HistoryContact struct {
	ID, ChannelID, SourceContactID int64
	CustomerID                     *int64
	OwnerReference                 string
	FirstEnteredAt, LastEnteredAt  time.Time
	EnterCount                     int64
	CreatedAt, UpdatedAt           time.Time
}

type HistoryAssignee struct {
	ID, ChannelID, SourceAssigneeID     int64
	StaffReference, DisplayNameSnapshot string
	Priority                            int
	RatioPercent, MaxScans24h           *int
	Status                              string
	SourceCreatedAt, SourceUpdatedAt    time.Time
}

type RecentEntrant struct {
	CustomerID    *int64
	AddedAt       time.Time
	LastEnteredAt time.Time
	EnterCount    int64
	Resolution    string
	Source        string
}

type HistoryStore interface {
	ListHistory(context.Context, int64, int, int) ([]HistoryContact, int64, []HistoryAssignee, error)
	ListRecent(context.Context, int64, int, int64) ([]RecentEntrant, error)
}

func (store *PostgreSQLStore) ListHistory(ctx context.Context, channelID int64, limit, offset int) ([]HistoryContact, int64, []HistoryAssignee, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(DISTINCT source_contact_id) FROM channel_history_contacts WHERE channel_id=$1`, channelID).Scan(&total); err != nil {
		return nil, 0, nil, err
	}
	rows, err := tx.Query(ctx, `WITH latest AS (
		SELECT DISTINCT ON (h.channel_id,h.source_contact_id) h.* FROM channel_history_contacts h
		JOIN channel_history_import_runs ir ON ir.id=h.import_run_id
		WHERE h.channel_id=$1 ORDER BY h.channel_id,h.source_contact_id,ir.snapshot_timestamp DESC,h.import_run_id DESC
	)
		SELECT h.id,h.channel_id,h.source_contact_id,COALESCE(r.customer_id,h.customer_id),h.owner_reference,h.first_entered_at,h.last_entered_at,h.enter_count,h.created_at,h.updated_at
		FROM latest h
		LEFT JOIN LATERAL (SELECT customer_id FROM channel_history_contact_reconciliations WHERE history_contact_id=h.id ORDER BY reconciled_at DESC,id DESC LIMIT 1) r ON true
		ORDER BY h.source_contact_id LIMIT $2 OFFSET $3`, channelID, limit, offset)
	if err != nil {
		return nil, 0, nil, err
	}
	contacts := make([]HistoryContact, 0, limit)
	for rows.Next() {
		var item HistoryContact
		if err = rows.Scan(&item.ID, &item.ChannelID, &item.SourceContactID, &item.CustomerID, &item.OwnerReference, &item.FirstEnteredAt, &item.LastEnteredAt, &item.EnterCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, 0, nil, err
		}
		contacts = append(contacts, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, 0, nil, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `WITH latest AS (
		SELECT DISTINCT ON (a.channel_id,a.source_assignee_id) a.* FROM channel_history_assignees a
		JOIN channel_history_import_runs ir ON ir.id=a.import_run_id
		WHERE a.channel_id=$1 ORDER BY a.channel_id,a.source_assignee_id,ir.snapshot_timestamp DESC,a.import_run_id DESC
	)
		SELECT id,channel_id,source_assignee_id,staff_reference,display_name_snapshot,priority,ratio_percent,max_scans_24h,status,source_created_at,source_updated_at FROM latest ORDER BY source_assignee_id LIMIT 200`, channelID)
	if err != nil {
		return nil, 0, nil, err
	}
	assignees := make([]HistoryAssignee, 0)
	for rows.Next() {
		var item HistoryAssignee
		if err = rows.Scan(&item.ID, &item.ChannelID, &item.SourceAssigneeID, &item.StaffReference, &item.DisplayNameSnapshot, &item.Priority, &item.RatioPercent, &item.MaxScans24h, &item.Status, &item.SourceCreatedAt, &item.SourceUpdatedAt); err != nil {
			rows.Close()
			return nil, 0, nil, err
		}
		assignees = append(assignees, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, 0, nil, err
	}
	rows.Close()
	return contacts, total, assignees, nil
}

func (store *PostgreSQLStore) ListRecent(ctx context.Context, channelID int64, limit int, offset int64) ([]RecentEntrant, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH latest_history AS (
		SELECT DISTINCT ON (h.channel_id,h.source_contact_id) h.* FROM channel_history_contacts h
		JOIN channel_history_import_runs ir ON ir.id=h.import_run_id
		WHERE h.channel_id=$1 ORDER BY h.channel_id,h.source_contact_id,ir.snapshot_timestamp DESC,h.import_run_id DESC
	), history_cutoff AS (
		SELECT max(ir.snapshot_timestamp) snapshot_timestamp
		FROM channel_history_contacts h JOIN channel_history_import_runs ir ON ir.id=h.import_run_id WHERE h.channel_id=$1
	), history AS (
		SELECT CASE WHEN COALESCE(r.customer_id,h.customer_id) IS NULL THEN 'history:'||h.source_contact_id::text ELSE 'customer:'||COALESCE(r.customer_id,h.customer_id)::text END user_key,
		       COALESCE(r.customer_id,h.customer_id) customer_id,h.first_entered_at,h.last_entered_at,h.enter_count,true historical,false runtime
		FROM latest_history h
		LEFT JOIN LATERAL (SELECT customer_id FROM channel_history_contact_reconciliations WHERE history_contact_id=h.id ORDER BY reconciled_at DESC,id DESC LIMIT 1) r ON true
	), runtime_events AS (
		SELECT DISTINCT r.id receipt_id,r.customer_id,r.occurred_at
		FROM channel_acquisition_entrant_receipts r JOIN channel_acquisition_state_bindings b ON b.id=r.binding_id
		CROSS JOIN history_cutoff cutoff
		WHERE r.status='channel_attributed' AND b.channel_id=$1 AND (cutoff.snapshot_timestamp IS NULL OR r.occurred_at>cutoff.snapshot_timestamp)
		UNION
		SELECT DISTINCT r.id,rr.customer_id,r.occurred_at
		FROM channel_acquisition_entrant_reconciliation_receipts rr
		JOIN channel_acquisition_entrant_receipts r ON r.id=rr.entrant_receipt_id
		JOIN channel_acquisition_state_bindings b ON b.id=rr.binding_id CROSS JOIN history_cutoff cutoff
		WHERE b.channel_id=$1 AND (cutoff.snapshot_timestamp IS NULL OR r.occurred_at>cutoff.snapshot_timestamp)
	), runtime AS (
		SELECT 'customer:'||customer_id::text user_key,customer_id,min(occurred_at) first_entered_at,max(occurred_at) last_entered_at,count(*) enter_count,false historical,true runtime
		FROM runtime_events GROUP BY customer_id
	), combined AS (
		SELECT * FROM history UNION ALL SELECT * FROM runtime
	), merged AS (
		SELECT user_key,max(customer_id) customer_id,min(first_entered_at) first_entered_at,max(last_entered_at) last_entered_at,
		       sum(enter_count) enter_count,bool_or(historical) historical,bool_or(runtime) runtime
		FROM combined GROUP BY user_key
	)
	SELECT customer_id,first_entered_at,last_entered_at,enter_count,
	       CASE WHEN customer_id IS NULL THEN 'unresolved' ELSE 'resolved' END,
	       CASE WHEN historical AND runtime THEN 'history+runtime' WHEN historical THEN 'history' ELSE 'runtime' END
	FROM merged ORDER BY last_entered_at DESC,user_key LIMIT $2 OFFSET $3`, channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RecentEntrant, 0, limit)
	for rows.Next() {
		var item RecentEntrant
		if err = rows.Scan(&item.CustomerID, &item.AddedAt, &item.LastEnteredAt, &item.EnterCount, &item.Resolution, &item.Source); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type HistoryService struct {
	uow     platformport.UnitOfWork
	catalog *CatalogService
	store   HistoryStore
}

func NewHistoryService(uow platformport.UnitOfWork, catalog *CatalogService, store HistoryStore) *HistoryService {
	return &HistoryService{uow: uow, catalog: catalog, store: store}
}
func (service *HistoryService) History(ctx context.Context, channelID int64, limit, offset int) ([]HistoryContact, int64, []HistoryAssignee, error) {
	if service == nil || service.uow == nil || service.catalog == nil || service.store == nil || limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, nil, ErrInvalidCatalogCommand
	}
	if _, err := service.catalog.Get(ctx, channelID); err != nil {
		return nil, 0, nil, err
	}
	var contacts []HistoryContact
	var total int64
	var assignees []HistoryAssignee
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		contacts, total, assignees, readErr = service.store.ListHistory(tx, channelID, limit, offset)
		return readErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, nil, ErrCatalogNotFound
		}
		return nil, 0, nil, errors.Join(ErrCatalogUnavailable, err)
	}
	return contacts, total, assignees, nil
}
func (service *HistoryService) Recent(ctx context.Context, channelID int64, limit int, offset int64) ([]RecentEntrant, error) {
	if service == nil || service.uow == nil || service.catalog == nil || service.store == nil || limit < 1 || limit > 51 || offset < 0 {
		return nil, ErrInvalidCatalogCommand
	}
	if _, err := service.catalog.Get(ctx, channelID); err != nil {
		return nil, err
	}
	var items []RecentEntrant
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		items, readErr = service.store.ListRecent(tx, channelID, limit, offset)
		return readErr
	})
	if err != nil {
		return nil, errors.Join(ErrCatalogUnavailable, err)
	}
	return items, nil
}
func safeCustomerLabel(id int64) string { return fmt.Sprintf("CID-%d", id) }
