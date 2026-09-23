package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

func TestPostgreSQLCoreOperations(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, e := platformpostgres.Wrap(pool, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(wrapped)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := NewPostgreSQL(pool, uow)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	var packages [2]int64
	for i := range packages {
		e = uow.Within(ctx, func(tx context.Context) error {
			p, _ := segmentdomain.NewPackage([]string{"core-a", "core-b"}[i], "核心产品", nil, 7, now)
			p, e = repo.CreatePackage(tx, p)
			if e != nil {
				return e
			}
			packages[i] = p.ID
			cfg, _ := segmentdomain.NewConfigurationVersion(p.ID, 1, []byte(`{"schema_version":1,"template_key":"core_ai_product","parameters":{"core_product_id":1}}`), "", "manual", 7, now)
			cfg, e = repo.CreateConfigurationVersion(tx, cfg)
			if e != nil {
				return e
			}
			if _, e = repo.SetCurrentConfiguration(tx, p.ID, cfg.ID, p.Version, 7, now); e != nil {
				return e
			}
			_, e = repo.PutCoreProduct(tx, segmentport.CoreProduct{ID: int64(i + 1), PackageID: p.ID, Name: "核心产品", Description: "描述", Enabled: true, UpdatedAt: now}, 0)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	var first segmentport.CoreAssignment
	e = uow.Within(ctx, func(tx context.Context) error {
		first, e = repo.ChangeCoreAssignment(tx, segmentport.CoreAssignment{CustomerID: 100, CoreProductID: 1, Source: "manual", Reason: "匹配", EnteredAt: now}, 0, false)
		if e != nil {
			return e
		}
		return repo.PublishCoreAssignments(tx, 7, "initial-publish", now)
	})
	if e != nil {
		t.Fatal(e)
	}
	push := segmentport.CorePush{Source: "machine:supervisor", PushID: "business-push-1", CustomerID: 100, PackageID: packages[0], Materials: []segmentport.CoreMaterialRef{{Kind: "image", ID: 2}, {Kind: "miniprogram", ID: 3}}, OccurredAt: now.Add(time.Second), Status: "reported", StatusVersion: 1}
	for i := 0; i < 3; i++ {
		e = uow.Within(ctx, func(tx context.Context) error {
			_, e := repo.RecordCorePush(tx, push, now.Add(2*time.Second))
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	push.Status = "success"
	push.StatusVersion = 2
	e = uow.Within(ctx, func(tx context.Context) error {
		_, e := repo.RecordCorePush(tx, push, now.Add(3*time.Second))
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	e = uow.Within(ctx, func(tx context.Context) error {
		d, e := repo.CoreMemberDetail(tx, packages[0], 100, 0, 10)
		if e != nil {
			return e
		}
		if d.Stats.PushCount != 1 || len(d.Pushes) != 1 || d.Pushes[0].Status != "success" || d.Pushes[0].AssignmentID != first.ID || d.Stats.VisitCount != nil {
			t.Fatalf("unexpected detail: %+v", d)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// Same logical push cannot be moved to another package by a retry.
	changed := push
	changed.PackageID = packages[1]
	e = uow.Within(ctx, func(tx context.Context) error { _, e := repo.RecordCorePush(tx, changed, now); return e })
	if !errors.Is(e, ErrConflict) {
		t.Fatalf("want conflict, got %v", e)
	}
	// Transfer updates both ordinary snapshots in one transaction.
	e = uow.Within(ctx, func(tx context.Context) error {
		_, e := repo.ChangeCoreAssignment(tx, segmentport.CoreAssignment{CustomerID: 100, CoreProductID: 2, Source: "manual", Reason: "转包", EnteredAt: now.Add(time.Minute)}, first.ID, false)
		if e != nil {
			return e
		}
		return repo.PublishCoreAssignments(tx, 7, "transfer-publish", now.Add(time.Minute))
	})
	if e != nil {
		t.Fatal(e)
	}
	e = uow.Within(ctx, func(tx context.Context) error {
		for i, id := range packages {
			s, found, e := repo.PublishedSnapshot(tx, segmentport.PackageID(id))
			if e != nil {
				return e
			}
			if !found || s.MemberCount != int64(i) {
				t.Fatalf("snapshot %d: %+v", id, s)
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	e = uow.Within(ctx, func(tx context.Context) error {
		_, e := repo.ChangeCoreAssignment(tx, segmentport.CoreAssignment{CustomerID: 100, CoreProductID: 1, Source: "ai", Reason: "stale", EnteredAt: now.Add(2 * time.Minute)}, 0, false)
		return e
	})
	if !errors.Is(e, ErrConflict) {
		t.Fatalf("stale AI must conflict: %v", e)
	}
	// Two writers racing on an unassigned customer must not publish two directions.
	outcomes := make(chan error, 2)
	for product := int64(1); product <= 2; product++ {
		go func(product int64) {
			outcomes <- uow.Within(ctx, func(tx context.Context) error {
				_, e := repo.ChangeCoreAssignment(tx, segmentport.CoreAssignment{CustomerID: 101, CoreProductID: product, Source: "manual", Reason: "并发调整", EnteredAt: now.Add(3 * time.Minute)}, 0, false)
				if e != nil {
					return e
				}
				return repo.PublishCoreAssignments(tx, 7, fmt.Sprintf("concurrent-product-%d", product), now.Add(3*time.Minute))
			})
		}(product)
	}
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-outcomes
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", successes, conflicts)
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM segment_audience_packages p JOIN segment_audience_snapshot_members m ON m.snapshot_id=p.published_snapshot_id WHERE m.customer_id=101`).Scan(&count); e != nil || count != 1 {
		t.Fatalf("visible directions=%d err=%v", count, e)
	}

}
