package channel

import (
	"context"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// CatalogSummary is the channel-owned aggregate used by every catalog surface.
// A historical unresolved contact is a real channel user, but never exposes its
// source identity. Resolved history and runtime receipts share the customer key.
type CatalogSummary struct {
	UniqueUsers     int64
	EnterCount      int64
	LatestEnteredAt *time.Time
	QRCodeAssetID   int64
	QRCodeStatus    string
	QRDownloadURL   string
	QRCodeOrigin    string
}

type CatalogSummaryReader interface {
	Summaries(context.Context, []int64) (map[int64]CatalogSummary, error)
}

type PostgreSQLCatalogSummaryReader struct{ uow platformport.UnitOfWork }

func NewPostgreSQLCatalogSummaryReader(uow platformport.UnitOfWork) *PostgreSQLCatalogSummaryReader {
	return &PostgreSQLCatalogSummaryReader{uow: uow}
}

func (reader *PostgreSQLCatalogSummaryReader) Summaries(ctx context.Context, channelIDs []int64) (map[int64]CatalogSummary, error) {
	result := make(map[int64]CatalogSummary, len(channelIDs))
	if len(channelIDs) == 0 {
		return result, nil
	}
	err := reader.uow.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(txctx, `WITH requested AS (
			SELECT unnest($1::bigint[]) channel_id
		), latest_history AS (
			SELECT DISTINCT ON (h.channel_id,h.source_contact_id) h.* FROM channel_history_contacts h
			JOIN channel_history_import_runs ir ON ir.id=h.import_run_id
			WHERE h.channel_id=ANY($1)
			ORDER BY h.channel_id,h.source_contact_id,ir.snapshot_timestamp DESC,h.import_run_id DESC
		), history_cutoff AS (
			SELECT h.channel_id,max(ir.snapshot_timestamp) snapshot_timestamp
			FROM channel_history_contacts h JOIN channel_history_import_runs ir ON ir.id=h.import_run_id
			WHERE h.channel_id=ANY($1) GROUP BY h.channel_id
		), history_users AS (
			SELECT channel_id,
			       CASE WHEN COALESCE(r.customer_id,h.customer_id) IS NULL THEN 'history:'||h.source_contact_id::text ELSE 'customer:'||COALESCE(r.customer_id,h.customer_id)::text END user_key,
			       h.enter_count,h.last_entered_at
			FROM latest_history h
			LEFT JOIN LATERAL (SELECT customer_id FROM channel_history_contact_reconciliations WHERE history_contact_id=h.id ORDER BY reconciled_at DESC,id DESC LIMIT 1) r ON true
		), runtime_events AS (
			SELECT DISTINCT r.id receipt_id, b.channel_id, r.customer_id, r.occurred_at
			FROM channel_acquisition_entrant_receipts r
			JOIN channel_acquisition_state_bindings b ON b.id=r.binding_id
			LEFT JOIN history_cutoff cutoff ON cutoff.channel_id=b.channel_id
			WHERE r.status='channel_attributed' AND b.channel_id=ANY($1) AND (cutoff.snapshot_timestamp IS NULL OR r.occurred_at>cutoff.snapshot_timestamp)
			UNION
			SELECT DISTINCT r.id, b.channel_id, rr.customer_id, r.occurred_at
			FROM channel_acquisition_entrant_reconciliation_receipts rr
			JOIN channel_acquisition_entrant_receipts r ON r.id=rr.entrant_receipt_id
			JOIN channel_acquisition_state_bindings b ON b.id=rr.binding_id
			LEFT JOIN history_cutoff cutoff ON cutoff.channel_id=b.channel_id
			WHERE b.channel_id=ANY($1) AND (cutoff.snapshot_timestamp IS NULL OR r.occurred_at>cutoff.snapshot_timestamp)
		), user_keys AS (
			SELECT channel_id,user_key FROM history_users
			UNION
			SELECT channel_id,'customer:'||customer_id::text FROM runtime_events
		), stats AS (
			SELECT q.channel_id,
			       (SELECT count(*) FROM user_keys u WHERE u.channel_id=q.channel_id) unique_users,
			       COALESCE((SELECT sum(enter_count) FROM history_users h WHERE h.channel_id=q.channel_id),0)+
			       COALESCE((SELECT count(*) FROM runtime_events e WHERE e.channel_id=q.channel_id),0) enter_count,
			       GREATEST((SELECT max(last_entered_at) FROM history_users h WHERE h.channel_id=q.channel_id),
			                (SELECT max(occurred_at) FROM runtime_events e WHERE e.channel_id=q.channel_id)) latest_entered_at
			FROM requested q
		), runtime_asset AS (
			SELECT DISTINCT ON (channel_id) channel_id,id,state
			FROM channel_acquisition_assets
			WHERE channel_id=ANY($1) AND kind='contact_way_qrcode' AND operation<>'delete' AND retired_at IS NULL
			  AND state IN ('executed','reconciled') AND result_url<>''
			ORDER BY channel_id,asset_version DESC,id DESC
		), legacy_asset AS (
			SELECT DISTINCT ON (channel_id) channel_id,id,verification_status
			FROM channel_legacy_acquisition_assets
			WHERE channel_id=ANY($1) AND kind='contact_way_qrcode' AND retired_at IS NULL
			ORDER BY channel_id,(verification_status='legacy_verified_active') DESC,asset_version DESC,id DESC
		)
		SELECT s.channel_id,s.unique_users,s.enter_count,s.latest_entered_at,
		       COALESCE(a.id,l.id,0),
		       CASE WHEN a.id IS NOT NULL THEN a.state WHEN l.id IS NOT NULL THEN l.verification_status ELSE 'not_generated' END,
		       CASE WHEN a.id IS NOT NULL OR l.verification_status='legacy_verified_active' THEN '/api/admin/channels/'||s.channel_id::text||'/qrcode/download' ELSE '' END,
		       CASE WHEN a.id IS NOT NULL THEN 'runtime' WHEN l.channel_id IS NOT NULL THEN 'legacy' ELSE '' END
		FROM stats s LEFT JOIN runtime_asset a ON a.channel_id=s.channel_id LEFT JOIN legacy_asset l ON l.channel_id=s.channel_id`, channelIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var summary CatalogSummary
			if err = rows.Scan(&id, &summary.UniqueUsers, &summary.EnterCount, &summary.LatestEnteredAt, &summary.QRCodeAssetID, &summary.QRCodeStatus, &summary.QRDownloadURL, &summary.QRCodeOrigin); err != nil {
				return err
			}
			result[id] = summary
		}
		return rows.Err()
	})
	return result, err
}
