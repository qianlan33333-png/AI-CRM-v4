package channel

import (
	"context"
	"errors"
	"net/url"

	"github.com/jackc/pgx/v5"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var errPublicLeadQRCodeUnavailable = errors.New("public lead channel unavailable")

// PostgreSQLPublicLeadQRCodeReader exposes only an already-persisted active QR
// asset. It never generates, refreshes, or contacts the Provider.
type PostgreSQLPublicLeadQRCodeReader struct{ uow platformport.UnitOfWork }

func NewPostgreSQLPublicLeadQRCodeReader(uow platformport.UnitOfWork) *PostgreSQLPublicLeadQRCodeReader {
	return &PostgreSQLPublicLeadQRCodeReader{uow: uow}
}

func (reader *PostgreSQLPublicLeadQRCodeReader) ReadPublicLeadQRCode(ctx context.Context, channelID int64) (channelport.PublicLeadQRCode, error) {
	if reader == nil || reader.uow == nil || channelID < 1 {
		return channelport.PublicLeadQRCode{}, errPublicLeadQRCodeUnavailable
	}
	var result string
	err := reader.uow.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		return tx.QueryRow(txctx, `SELECT COALESCE(NULLIF(v.qrcode_url,''),runtime.result_url,legacy.result_url,'')
			FROM channels c
			JOIN channel_config_versions v ON v.channel_id=c.id AND v.config_version=c.current_config_version
			LEFT JOIN LATERAL (
				SELECT a.result_url FROM channel_acquisition_assets a
				WHERE a.channel_id=c.id AND a.kind='contact_way_qrcode' AND a.operation<>'delete'
				  AND a.retired_at IS NULL AND a.state IN ('executed','reconciled') AND a.result_url<>''
				ORDER BY a.asset_version DESC,a.id DESC LIMIT 1
			) runtime ON true
			LEFT JOIN LATERAL (
				SELECT a.result_url FROM channel_legacy_acquisition_assets a
				WHERE a.channel_id=c.id AND a.kind='contact_way_qrcode' AND a.retired_at IS NULL
				  AND a.verification_status='legacy_verified_active' AND a.result_url<>''
				ORDER BY a.asset_version DESC,a.id DESC LIMIT 1
			) legacy ON true
			WHERE c.id=$1 AND c.status='active'`, channelID).Scan(&result)
	})
	if errors.Is(err, pgx.ErrNoRows) || err != nil || !validPublicLeadQRCodeURL(result) {
		return channelport.PublicLeadQRCode{}, errPublicLeadQRCodeUnavailable
	}
	return channelport.PublicLeadQRCode{URL: result}, nil
}

func validPublicLeadQRCodeURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

var _ channelport.PublicLeadQRCodeReader = (*PostgreSQLPublicLeadQRCodeReader)(nil)
