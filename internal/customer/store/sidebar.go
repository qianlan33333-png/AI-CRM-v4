package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (PostgreSQL) ReadSidebarProfile(ctx context.Context, customerID customerdomain.CustomerID) (customerport.SidebarProfile, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.SidebarProfile{}, err
	}
	var p customerport.SidebarProfile
	err = tx.QueryRow(ctx, `SELECT d.customer_id,d.display_name,d.avatar_url,d.phone_masked,COALESCE(d.phone_assurance,''),d.customer_status,d.activation_status,d.gender,d.contact_type,d.corp_name,d.source,COALESCE(s.profile_source,''),COALESCE(s.version,0),COALESCE(s.industry,''),COALESCE(s.industry_description,''),COALESCE(s.needs_blockers_followup,''),d.source_version,d.last_synced_at,GREATEST(d.updated_at,COALESCE(s.updated_at,d.updated_at)) FROM customer_directory_projection d LEFT JOIN customer_sidebar_profiles s ON s.customer_id=d.customer_id WHERE d.customer_id=$1`, customerID).Scan(&p.CustomerID, &p.DisplayName, &p.AvatarURL, &p.PhoneMasked, &p.PhoneAssurance, &p.Status, &p.ActivationState, &p.Gender, &p.ContactType, &p.CorpName, &p.Source, &p.ProfileSource, &p.ProfileVersion, &p.Industry, &p.IndustryDescription, &p.NeedsBlockersFollowup, &p.Version, &p.LastSyncedAt, &p.UpdatedAt)
	if err == nil {
		// Receipt snapshots round-trip through JSON, which decodes RFC3339 Z
		// timestamps with time.UTC. Canonicalize PostgreSQL timestamptz values at
		// this store boundary so the first result and its replay are identical in
		// every process timezone, including UTC where time.Local != time.UTC.
		p.UpdatedAt = p.UpdatedAt.UTC()
		if p.LastSyncedAt != nil {
			lastSyncedAt := p.LastSyncedAt.UTC()
			p.LastSyncedAt = &lastSyncedAt
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.SidebarProfile{}, customerapp.ErrNotFound
	}
	return p, err
}

func (PostgreSQL) FindSidebarProfileReceipt(ctx context.Context, key [32]byte) (customerapp.SidebarProfileReceipt, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerapp.SidebarProfileReceipt{}, false, err
	}
	var receipt customerapp.SidebarProfileReceipt
	var digest []byte
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT payload_digest,outcome,result_snapshot FROM customer_sidebar_profile_receipts WHERE key_digest=$1`, key[:]).Scan(&digest, &receipt.Outcome, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return receipt, false, nil
	}
	if err != nil || len(digest) != 32 || json.Unmarshal(raw, &receipt.Profile) != nil {
		return receipt, false, err
	}
	copy(receipt.PayloadDigest[:], digest)
	return receipt, true, nil
}

func (PostgreSQL) UpdateSidebarProfile(ctx context.Context, command customerport.SidebarProfileUpdate, _, _ [32]byte, at time.Time) (customerport.SidebarProfile, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.SidebarProfile{}, err
	}
	var id int64
	annotationUpdate := command.SourceSet || command.IndustrySet || command.IndustryDescriptionSet || command.NeedsBlockersFollowupSet
	if annotationUpdate {
		if command.ExpectedProfileVersion == 0 {
			// The first profile write is an INSERT-only CAS. A concurrent opener
			// gets no row after the unique-key conflict and is reported as a
			// version conflict; it must never overwrite the winner's annotations.
			err = tx.QueryRow(ctx, `INSERT INTO customer_sidebar_profiles(customer_id,profile_source,industry,industry_description,needs_blockers_followup,version,updated_at) VALUES($1,$2,$3,$4,$5,1,$6) ON CONFLICT (customer_id) DO NOTHING RETURNING customer_id`, command.CustomerID, command.ProfileSource, command.Industry, command.IndustryDescription, command.NeedsBlockersFollowup, at).Scan(&id)
		} else {
			// Later profile edits update the existing row directly. Do not route
			// this through an INSERT ... SELECT predicate: that predicate would
			// suppress the row before ON CONFLICT can perform the CAS update.
			err = tx.QueryRow(ctx, `UPDATE customer_sidebar_profiles SET profile_source=CASE WHEN $3 THEN $4 ELSE profile_source END,industry=CASE WHEN $5 THEN $6 ELSE industry END,industry_description=CASE WHEN $7 THEN $8 ELSE industry_description END,needs_blockers_followup=CASE WHEN $9 THEN $10 ELSE needs_blockers_followup END,version=version+1,updated_at=$11 WHERE customer_id=$1 AND version=$2 RETURNING customer_id`, command.CustomerID, command.ExpectedProfileVersion, command.SourceSet, command.ProfileSource, command.IndustrySet, command.Industry, command.IndustryDescriptionSet, command.IndustryDescription, command.NeedsBlockersFollowupSet, command.NeedsBlockersFollowup, at).Scan(&id)
		}
	} else {
		err = tx.QueryRow(ctx, `UPDATE customer_directory_projection SET display_name=$3,gender=$4,corp_name=$5,source_version=source_version+1,updated_at=$6 WHERE customer_id=$1 AND source_version=$2 RETURNING customer_id`, command.CustomerID, command.ExpectedVersion, command.DisplayName, command.Gender, command.CorpName, at).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.SidebarProfile{}, customerapp.ErrSidebarProfileConflict
	}
	if err != nil {
		return customerport.SidebarProfile{}, err
	}
	return PostgreSQL{}.ReadSidebarProfile(ctx, command.CustomerID)
}

func (PostgreSQL) RecordSidebarProfileReceipt(ctx context.Context, key, payload [32]byte, command customerport.SidebarProfileUpdate, outcome string, profile customerport.SidebarProfile) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(profile)
	employee := sha256Digest(command.EmployeeID)
	_, err = tx.Exec(ctx, `INSERT INTO customer_sidebar_profile_receipts(key_digest,payload_digest,customer_id,employee_digest,outcome,result_snapshot) VALUES($1,$2,$3,$4,$5,$6::jsonb)`, key[:], payload[:], command.CustomerID, employee[:], outcome, raw)
	return err
}

func sha256Digest(value string) [32]byte {
	return sha256.Sum256([]byte(value))
}

var _ customerapp.SidebarProfileStore = PostgreSQL{}
