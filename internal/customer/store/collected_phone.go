package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// ProjectCollectedPhone only writes Customer's own presentation. All numbers
// remain in Identity even when an existing verified/primary display is kept.
// Historical repair fills an empty display; it cannot replace current data.
func (PostgreSQL) ProjectCollectedPhone(ctx context.Context, id customerdomain.CustomerID, masked, source string, at time.Time, historical bool) error {
	if id < 1 || masked == "" || at.IsZero() || source != "order" && source != "survey" {
		return customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,oneid_label,phone_masked,phone_assurance,activation_status,source,source_version,last_synced_at,updated_at)
 VALUES($1,'active',$2,$3,'declared','active',$4,1,$5,$5)
 ON CONFLICT(customer_id) DO UPDATE SET phone_masked=EXCLUDED.phone_masked,phone_assurance='declared',source_version=customer_directory_projection.source_version+1,updated_at=GREATEST(customer_directory_projection.updated_at,$5)
 WHERE customer_directory_projection.phone_masked='' OR (NOT $6 AND COALESCE(customer_directory_projection.phone_assurance,'')<>'verified')`, id, "CID-"+strconv.FormatInt(int64(id), 10), masked, source, at.UTC(), historical)
	return err
}

func (PostgreSQL) PhoneDisplayEmptyWithin(ctx context.Context, id customerdomain.CustomerID) (bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	var value string
	err = tx.QueryRow(ctx, `SELECT phone_masked FROM customer_directory_projection WHERE customer_id=$1 FOR UPDATE`, id).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	return value == "", err
}

// A historical contact fill is not new customer activity. Preserve existing
// activity/sync state; a previously absent directory stays stale until its
// normal authoritative sync supplies evidence. Neither path enqueues automation.
func (PostgreSQL) ProjectHistoricalPhoneWithoutActivity(ctx context.Context, id customerdomain.CustomerID, masked, source string, at time.Time) error {
	if id < 1 || masked == "" || at.IsZero() || source != "order" {
		return customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,oneid_label,phone_masked,phone_assurance,activation_status,source,source_version,updated_at)
 VALUES($1,'active',$2,$3,'declared','stale',$4,1,$5)
 ON CONFLICT(customer_id) DO UPDATE SET phone_masked=EXCLUDED.phone_masked,phone_assurance='declared',source_version=customer_directory_projection.source_version+1
 WHERE customer_directory_projection.phone_masked=''`, id, "CID-"+strconv.FormatInt(int64(id), 10), masked, source, at.UTC())
	return err
}
