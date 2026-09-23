package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

// DirectPushEligibility exposes one immutable Segment decision without
// leaking Segment tables to Automation. It deliberately does not inspect a
// customer/staff friendship; Provider rejection remains an Outbound fact.
func (r *Repository) DirectPushEligibility(ctx context.Context, packageID segmentport.PackageID, customerID customerdomain.CustomerID, staffID int64) (segmentport.DirectPushEligibility, string, error) {
	if r == nil || packageID < 1 || customerID < 1 || staffID < 1 {
		return segmentport.DirectPushEligibility{}, "", ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return segmentport.DirectPushEligibility{}, "", err
	}
	var out segmentport.DirectPushEligibility
	var active, member, sender bool
	err = t.QueryRow(ctx, `SELECT COALESCE(p.published_snapshot_id,0),COALESCE(s.version,0),
		p.lifecycle='active' AND snap.state='published',m.customer_id IS NOT NULL,sm.staff_id IS NOT NULL
		FROM segment_audience_packages p
		LEFT JOIN segment_audience_sender_sets s ON s.id=p.current_sender_set_id AND s.package_id=p.id
		LEFT JOIN segment_audience_sender_set_members sm ON sm.sender_set_id=s.id AND sm.staff_id=$3
		LEFT JOIN segment_audience_snapshot_members m ON m.snapshot_id=p.published_snapshot_id AND m.customer_id=$2
		LEFT JOIN segment_audience_snapshots snap ON snap.id=p.published_snapshot_id AND snap.package_id=p.id
		WHERE p.id=$1`, packageID, customerID, staffID).Scan(&out.SnapshotID, &out.SenderSetVersion, &active, &member, &sender)
	if errors.Is(err, pgx.ErrNoRows) {
		return segmentport.DirectPushEligibility{}, "package_not_found", nil
	}
	if err != nil {
		return out, "", err
	}
	if !active {
		return segmentport.DirectPushEligibility{}, "package_not_active", nil
	}
	if !member {
		return segmentport.DirectPushEligibility{}, "customer_not_in_package", nil
	}
	if !sender {
		return segmentport.DirectPushEligibility{}, "sender_not_allowed", nil
	}
	return out, "", nil
}

func (r *Repository) DirectPushPackageExists(ctx context.Context, packageID segmentport.PackageID) (bool, error) {
	if r == nil || packageID < 1 {
		return false, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var exists bool
	err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM segment_audience_packages WHERE id=$1)`, packageID).Scan(&exists)
	return exists, err
}

var _ segmentport.DirectPushEligibilityReader = (*Repository)(nil)
