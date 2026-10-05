package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

func TestPostgreSQLDirectPushEligibilityRejectsRetiredSurveyConfigurationHistory(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	var packageID int64
	var retiredConfigurationID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		group, createErr := segmentdomain.NewGroup("Survey direct push", 0, 7, now)
		if createErr != nil {
			return createErr
		}
		group, createErr = repo.CreateGroup(tx, group)
		if createErr != nil {
			return createErr
		}
		pkg, createErr := segmentdomain.NewPackage("survey-direct-push", "Survey direct push", &group.ID, 7, now)
		if createErr != nil {
			return createErr
		}
		pkg, createErr = repo.CreatePackage(tx, pkg)
		if createErr != nil {
			return createErr
		}
		packageID = pkg.ID
		retired, createErr := segmentdomain.NewConfigurationVersion(pkg.ID, 1, json.RawMessage(`{"schema_version":1,"template_key":"questionnaire_submissions","parameters":{"questionnaire_ids":["16"],"owner_scope":"specified","owner_staff_ids":["17"],"require_wecom_identity":false}}`), "", "manual", 7, now)
		if createErr != nil {
			return createErr
		}
		retired, createErr = repo.CreateConfigurationVersion(tx, retired)
		if createErr != nil {
			return createErr
		}
		retiredConfigurationID = retired.ID
		pkg, createErr = repo.SetCurrentConfiguration(tx, pkg.ID, retired.ID, pkg.Version, 7, now)
		if createErr != nil {
			return createErr
		}
		senders, createErr := repo.CreateSenderSet(tx, segmentdomain.SenderSet{
			PackageID: pkg.ID, CreatedBy: 7, CreatedActorKind: "admin", CreatedActorRef: "admin:7", CreatedAt: now,
			Members: []segmentdomain.Sender{{StaffID: accessport.StaffID(17), EligibilityVersion: 1, EligibilityRefreshedAt: now}},
		})
		if createErr != nil {
			return createErr
		}
		pkg, createErr = repo.SetCurrentSenderSet(tx, pkg.ID, senders.ID, pkg.Version, 7, now)
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	firstSnapshot := directPushEligibilityTestPublish(t, ctx, uow, repo, packageID, retiredConfigurationID, customerdomain.CustomerID(91), now, "retired")
	directPushEligibilityTestLifecycle(t, ctx, uow, native, packageID, "active", now)
	assertDirectPushEligibilityCode(t, ctx, uow, repo, packageID, customerdomain.CustomerID(91), 17, "definition_unsupported")

	// Reconfiguration must not make the old, retired Survey snapshot usable.
	var compatibleConfigurationID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		if _, updateErr := native.Exec(ctx, `UPDATE segment_audience_packages SET lifecycle='paused' WHERE id=$1`, packageID); updateErr != nil {
			return updateErr
		}
		pkg, getErr := repo.GetPackage(tx, packageID)
		if getErr != nil {
			return getErr
		}
		compatible, createErr := segmentdomain.NewConfigurationVersion(pkg.ID, 2, json.RawMessage(`{"schema_version":1,"template_key":"questionnaire_submissions","parameters":{"questionnaire_ids":["16"],"owner_scope":"all","owner_staff_ids":[],"require_wecom_identity":false}}`), "", "manual", 7, now.Add(time.Minute))
		if createErr != nil {
			return createErr
		}
		compatible, createErr = repo.CreateConfigurationVersion(tx, compatible)
		if createErr != nil {
			return createErr
		}
		compatibleConfigurationID = compatible.ID
		_, createErr = repo.SetCurrentConfiguration(tx, pkg.ID, compatible.ID, pkg.Version, 7, now.Add(time.Minute))
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	directPushEligibilityTestLifecycle(t, ctx, uow, native, packageID, "active", now.Add(time.Minute))
	assertDirectPushEligibilityCode(t, ctx, uow, repo, packageID, customerdomain.CustomerID(91), 17, "definition_unsupported")

	if firstSnapshot.ConfigurationVersionID != retiredConfigurationID {
		t.Fatalf("first published snapshot captured config %d, want %d", firstSnapshot.ConfigurationVersionID, retiredConfigurationID)
	}
	secondSnapshot := directPushEligibilityTestPublish(t, ctx, uow, repo, packageID, compatibleConfigurationID, customerdomain.CustomerID(91), now.Add(2*time.Minute), "compatible")
	directPushEligibilityTestLifecycle(t, ctx, uow, native, packageID, "active", now.Add(2*time.Minute))
	if secondSnapshot.ConfigurationVersionID != compatibleConfigurationID {
		t.Fatalf("second published snapshot captured config %d, want %d", secondSnapshot.ConfigurationVersionID, compatibleConfigurationID)
	}
	assertDirectPushEligibilityCode(t, ctx, uow, repo, packageID, customerdomain.CustomerID(91), 17, "")
}

func directPushEligibilityTestPublish(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repo *Repository, packageID, configurationID int64, customerID customerdomain.CustomerID, now time.Time, key string) segmentdomain.Snapshot {
	t.Helper()
	ids := []customerdomain.CustomerID{customerID}
	memberDigest := segmentdomain.DigestMembers(ids)
	var snapshot segmentdomain.Snapshot
	err := uow.Within(ctx, func(tx context.Context) error {
		run, owned, reserveErr := repo.ReserveRefresh(tx, segmentdomain.RefreshRun{
			PackageID: packageID, ConfigurationVersionID: configurationID, SourceKeyDigest: sha256.Sum256([]byte("direct-push-test:" + key)),
			ReferenceTime: now, RefreshKind: segmentdomain.RefreshDaily, CreatedAt: now, UpdatedAt: now,
		})
		if reserveErr != nil || !owned {
			if reserveErr != nil {
				return reserveErr
			}
			return fmt.Errorf("refresh reservation unexpectedly replayed")
		}
		if _, reserveErr = repo.AttachRefreshJob(tx, run.ID, run.ID, now); reserveErr != nil {
			return reserveErr
		}
		if _, _, reserveErr = repo.BeginRefresh(tx, run.ID, now); reserveErr != nil {
			return reserveErr
		}
		if reserveErr = repo.StageRefreshBatch(tx, run.ID, 0, ids, memberDigest, now); reserveErr != nil {
			return reserveErr
		}
		published, publishErr := repo.PublishRefresh(tx, run.ID, int64(len(ids)), memberDigest, sha256.Sum256([]byte("watermark:"+key)), 7, now)
		if publishErr == nil {
			snapshot = published.Snapshot
		}
		return publishErr
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func directPushEligibilityTestLifecycle(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, native *pgxpool.Pool, packageID int64, lifecycle string, now time.Time) {
	t.Helper()
	if err := uow.Within(ctx, func(context.Context) error {
		_, err := native.Exec(ctx, `UPDATE segment_audience_packages SET lifecycle=$2,version=version+1,updated_at=$3 WHERE id=$1`, packageID, lifecycle, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func assertDirectPushEligibilityCode(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repo *Repository, packageID int64, customerID customerdomain.CustomerID, staffID int64, expected string) {
	t.Helper()
	var code string
	if err := uow.Within(ctx, func(tx context.Context) error {
		_, value, err := repo.DirectPushEligibility(tx, segmentport.PackageID(packageID), customerID, staffID)
		code = value
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if code != expected {
		t.Fatalf("direct push eligibility code=%q, want %q", code, expected)
	}
}
