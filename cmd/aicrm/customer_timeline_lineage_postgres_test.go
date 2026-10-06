package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLCustomerTimelineRetainsMergedHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	loserFact := evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:timeline-lineage", "synthetic-loser")
	survivorFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:timeline-lineage", "synthetic-survivor")
	var loserID, survivorID, outsiderID customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		for _, fixture := range []struct {
			fact identitydomain.VerifiedFact
			id   *customerdomain.CustomerID
		}{{loserFact, &loserID}, {survivorFact, &survivorID}, {evidenceMergeFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:timeline-lineage", "synthetic-outsider"), &outsiderID}} {
			root, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, fixture.fact)
			if e != nil {
				return e
			}
			*fixture.id = root.CustomerID
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	store := customerstore.NewPostgreSQL()
	if err = uow.Within(ctx, func(tx context.Context) error {
		for index, id := range []customerdomain.CustomerID{loserID, survivorID, outsiderID} {
			if e := store.AppendTimeline(tx, customerport.TimelineEvent{CustomerID: id, SourceDomain: "survey", SourceEventID: []string{"timeline:loser", "timeline:survivor", "timeline:outsider"}[index], EventType: "customer.survey_submitted", Title: "问卷已提交", OccurredAt: stamp}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var mergeID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		candidate, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loserID, Target: survivorFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "customer.timeline.test", EventID: "synthetic-lineage", Digest: "synthetic-lineage-evidence", PolicyVersion: "test-v1"}})
		if e != nil {
			return e
		}
		if candidate.Candidate == nil {
			t.Fatal("expected merge candidate")
		}
		merged, e := oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivorID, Operator: "synthetic-lineage"})
		if merged.Merge != nil {
			mergeID = merged.Merge.ID
		}
		return e
	}); err != nil || mergeID < 1 {
		t.Fatalf("merge: id=%d error=%v", mergeID, err)
	}
	adapter := customerTimelineAdapter{uow: uow, reader: store, lineage: identityquery.NewPostgreSQL()}
	query := customerport.PageQuery{Limit: 10, Watermark: stamp}
	page, err := adapter.CustomerTimeline(ctx, survivorID, query)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("merged canonical timeline lost history: items=%+v error=%v expected=2", page.Items, err)
	}
	if page.Items[0].ID <= page.Items[1].ID {
		t.Fatal("equal timestamp timeline must use global descending ID order")
	}
	query.Limit = 1
	first, err := adapter.CustomerTimeline(ctx, loserID, query)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != page.Items[0].ID {
		t.Fatalf("historical alias differs from root: items=%+v error=%v", first.Items, err)
	}
	query.AfterAt, query.AfterID = first.Items[0].OccurredAt, first.Items[0].ID
	second, err := adapter.CustomerTimeline(ctx, survivorID, query)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != page.Items[1].ID {
		t.Fatalf("global cursor missed older fact: items=%+v error=%v", second.Items, err)
	}
	query.AfterAt, query.AfterID = second.Items[0].OccurredAt, second.Items[0].ID
	end, err := adapter.CustomerTimeline(ctx, survivorID, query)
	if err != nil || len(end.Items) != 0 {
		t.Fatalf("cursor must terminate without outsider: items=%+v error=%v", end.Items, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, mergeID); return e }); err != nil {
		t.Fatal(err)
	}
	query = customerport.PageQuery{Limit: 10, Watermark: stamp}
	for _, id := range []customerdomain.CustomerID{loserID, survivorID} {
		detached, e := adapter.CustomerTimeline(ctx, id, query)
		if e != nil || len(detached.Items) != 1 {
			t.Fatalf("detach must restore each original history: customer=%d items=%+v error=%v", id, detached.Items, e)
		}
	}
	var unchanged int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM customer_timeline_projection WHERE customer_id=ANY($1::bigint[])`, []int64{int64(loserID), int64(survivorID), int64(outsiderID)}).Scan(&unchanged); err != nil || unchanged != 3 {
		t.Fatalf("historical storage changed: rows=%d error=%v", unchanged, err)
	}
}
