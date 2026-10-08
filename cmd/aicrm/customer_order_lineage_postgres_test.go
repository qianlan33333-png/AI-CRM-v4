package main

import (
	"context"
	"crypto/sha256"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLCustomerOrdersRetainMergedHistoryAndRestoreAfterRevert(t *testing.T) {
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
	loserFact := evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:order-lineage", "synthetic-loser")
	survivorFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:order-lineage", "synthetic-survivor")
	var loserID, survivorID, outsiderID customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		for _, fixture := range []struct {
			fact identitydomain.VerifiedFact
			id   *customerdomain.CustomerID
		}{{loserFact, &loserID}, {survivorFact, &survivorID}, {evidenceMergeFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:order-lineage", "synthetic-outsider"), &outsiderID}} {
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
	repository, err := orderstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repository)
	if err = service.SetLockedCanonicalLineageReader(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	manifest, schema := sha256.Sum256([]byte("synthetic-order-lineage-manifest")), sha256.Sum256([]byte("synthetic-order-lineage-schema"))
	if _, err = native.Exec(ctx, `INSERT INTO order_import_runs(run_key,source_manifest_digest,source_schema_digest,status) VALUES('order-lineage-test',$1,$2,'applying')`, manifest[:], schema[:]); err != nil {
		t.Fatal(err)
	}
	ptr := func(id customerdomain.CustomerID) *int64 { value := int64(id); return &value }
	original := make(map[string]orderdomain.Snapshot)
	for _, fixture := range []struct {
		key                string
		payer, beneficiary *int64
		status             orderdomain.Status
		refund             int64
		at                 time.Time
	}{
		{"old-payer", ptr(loserID), ptr(outsiderID), orderdomain.StatusPaid, 0, stamp},
		{"root-beneficiary", ptr(outsiderID), ptr(survivorID), orderdomain.StatusPaymentFailed, 0, stamp},
		{"both-roots", ptr(loserID), ptr(survivorID), orderdomain.StatusRefunded, 2500, stamp},
		{"old-beneficiary", ptr(outsiderID), ptr(loserID), orderdomain.StatusPartiallyRefunded, 100, stamp},
		{"old-payer-only", ptr(loserID), nil, orderdomain.StatusPaid, 0, stamp},
		{"outsider", ptr(outsiderID), ptr(outsiderID), orderdomain.StatusPaid, 0, stamp},
		{"future", ptr(survivorID), ptr(outsiderID), orderdomain.StatusClosed, 0, stamp.Add(time.Minute)},
	} {
		snapshot := orderdomain.Snapshot{Provider: orderdomain.ProviderWeChatPay, SourceSystem: "order-lineage-test", SourceKey: fixture.key, MerchantOrderNo: "order-lineage-" + fixture.key,
			PayerCustomerID: fixture.payer, BeneficiaryCustomerID: fixture.beneficiary, Amount: orderdomain.Money{AmountMinor: 2500, Currency: "CNY"}, RefundedMinor: fixture.refund, Status: fixture.status,
			Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: "historical", ProductName: "原历史课程", UnitAmountMinor: 2500, Quantity: 1, LineAmountMinor: 2500}}, RecordOrigin: orderdomain.RecordOriginHistory, Version: 1, CreatedAt: fixture.at, UpdatedAt: fixture.at}
		created, e := service.ImportHistorical(ctx, orderport.HistoricalImportCommand{RunID: "order-lineage-test", SourceDigest: sha256.Sum256([]byte(fixture.key)), Order: snapshot})
		if e != nil {
			t.Fatal(e)
		}
		original[fixture.key] = created
	}
	var mergeID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		candidate, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: loserID, Target: survivorFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "customer.order.test", EventID: "synthetic-order-lineage", Digest: "synthetic-order-lineage-evidence", PolicyVersion: "test-v1"}})
		if e != nil {
			return e
		}
		if candidate.Candidate == nil {
			t.Fatal("expected merge candidate")
		}
		merged, e := oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: survivorID, Operator: "synthetic-order-lineage"})
		if merged.Merge != nil {
			mergeID = merged.Merge.ID
		}
		return e
	}); err != nil || mergeID < 1 {
		t.Fatalf("merge id=%d error=%v", mergeID, err)
	}
	var rootSummary orderport.CustomerOrderSummary
	for _, id := range []customerdomain.CustomerID{survivorID, loserID} {
		summary, e := service.CustomerOrderSummary(ctx, int64(id), 10)
		if e != nil || summary.Total != 6 || summary.Paid != 2 || summary.Failed != 1 || summary.Refunded != 2 || len(summary.Recent) != 6 {
			t.Fatalf("merged summary must count each order once: %+v error=%v", summary, e)
		}
		if summary.Recent[0].ID != original["future"].ID {
			t.Fatal("recent order list lost global creation ordering")
		}
		if rootSummary.Total == 0 {
			rootSummary = summary
		} else if !reflect.DeepEqual(rootSummary, summary) {
			t.Fatal("old alias and canonical root returned different order summaries")
		}
	}
	expected := []struct{ key, relationship string }{{"old-payer-only", "payer"}, {"old-beneficiary", "beneficiary"}, {"both-roots", "payer_and_beneficiary"}, {"root-beneficiary", "beneficiary"}, {"old-payer", "payer"}}
	query := orderport.CustomerActivityQuery{CustomerID: int64(loserID), Limit: 1, Watermark: stamp}
	for _, item := range expected {
		page, e := service.CustomerActivities(ctx, query)
		if e != nil || len(page.Items) != 1 || page.Items[0].OrderID != original[item.key].ID || page.Items[0].Relationship != item.relationship {
			t.Fatalf("merged keyset item=%+v want=%+v error=%v", page.Items, item, e)
		}
		query.CustomerID = int64(survivorID)
		query.AfterAt, query.AfterID = page.Items[0].OccurredAt, page.Items[0].OrderID
	}
	if page, e := service.CustomerActivities(ctx, query); e != nil || len(page.Items) != 0 {
		t.Fatalf("watermark/cursor leaked outsider or future order: %+v error=%v", page, e)
	}
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, mergeID); return e }); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		id    customerdomain.CustomerID
		total int64
		keys  []string
	}{{loserID, 4, []string{"old-payer-only", "old-beneficiary", "both-roots", "old-payer"}}, {survivorID, 3, []string{"both-roots", "root-beneficiary"}}} {
		summary, e := service.CustomerOrderSummary(ctx, int64(fixture.id), 10)
		if e != nil || summary.Total != fixture.total {
			t.Fatalf("reverted summary retained other root: %+v want=%d error=%v", summary, fixture.total, e)
		}
		page, e := service.CustomerActivities(ctx, orderport.CustomerActivityQuery{CustomerID: int64(fixture.id), Limit: 10, Watermark: stamp})
		if e != nil || len(page.Items) != len(fixture.keys) {
			t.Fatalf("reverted activities=%+v error=%v", page, e)
		}
		for i, key := range fixture.keys {
			if page.Items[i].OrderID != original[key].ID {
				t.Fatalf("reverted history leaked order: %+v want=%s", page.Items, key)
			}
		}
	}
	for key, before := range original {
		after, e := service.Get(ctx, before.ID)
		if e != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("historical order %s changed: before=%+v after=%+v error=%v", key, before, after, e)
		}
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		count, e := repository.Count(tx, orderapp.ListFilter{CustomerIDs: []int64{}})
		if e == nil && count != 0 {
			t.Fatalf("empty explicit customer scope returned %d orders", count)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
