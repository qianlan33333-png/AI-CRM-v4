package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcapp "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/app"
	hxcdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	hxcworker "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/worker"
	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

// Real Create/Refresh publication, durable River insertion, OneID merge/revert,
// and stable HXC read ports. Source snapshot is synthetic; no external HXC
// reader or provider writer is contacted. Worker executes once synchronously.
func TestPostgreSQLHXCReadPortsFollowCurrentOneIDLineage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	phoneVault, err := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	if err != nil {
		t.Fatal(err)
	}
	observationVault, err := identitysecure.NewObservationVault(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("o", 32))))
	if err != nil {
		t.Fatal(err)
	}
	identityRepository := identitystore.NewPostgresStoreWithObservation(phoneVault, observationVault)
	oneID := identityapp.OneIDService{Store: identityRepository}
	aFact := evidenceMergeFact(t, identitydomain.KindUnionID, "wechat-open-platform:hxc-lineage", "hxc-lineage-union")
	bFact := evidenceMergeFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:hxc-lineage", "hxc-lineage-contact")
	var a, b customerdomain.CustomerID
	if err = uow.Within(ctx, func(tx context.Context) error {
		x, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, aFact)
		if e != nil {
			return e
		}
		a = x.CustomerID
		x, e = oneID.ProvisionCustomerFromVerifiedIdentity(tx, bFact)
		b = x.CustomerID
		return e
	}); err != nil {
		t.Fatal(err)
	}
	store := hxcstore.NewPostgreSQL(native)
	if err = store.BindCanonicalLineage(identityquery.NewPostgreSQL()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	expires := now.Add(time.Hour)
	projection := hxcdomain.Projection{AsOf: now, SourceDigest: [32]byte{1}, ProjectionDigest: [32]byte{1}, SharedFactsAvailable: true,
		Counts: hxcdomain.Counts{Total: 1, ActiveUsed: 1, Matched: 1, MatchedByUnionID: 1}, RegistrationCoverage: map[int64]string{int64(a): "registered"},
		Rows: []hxcdomain.ProjectionRow{{SubjectDigest: [32]byte{1}, UserRef: "HXC-000000000001", Stage: hxcdomain.ActiveUsed, CustomerID: a, IdentityState: hxcdomain.Matched, MatchedBy: "unionid", IdentityReasonCode: "matched_unionid",
			SourceRow: hxcdomain.SourceRow{HXCUserID: "fixture-hxc-1", UnionID: "hxc-lineage-union", SourceUpdatedAt: now, MembershipAttribution: "none", CapabilityUsage: []byte(`{}`), FocusTopics: []byte(`[]`), FormallyLoggedIn: true, HasTokenUsage: true, MembershipRecordFound: true, IsMember: true, MembershipSource: "user_id", MembershipStatus: "active", MembershipExpiresAt: &expires, LastUsedAt: &now}}}}
	queries := identityquery.NewPostgreSQL(phoneVault)
	workers := river.NewWorkers()
	worker := &hxcworker.Worker{}
	river.AddWorker(workers, worker)
	client, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer, err := hxcworker.NewEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(&platformaudit.PostgreSQLStore{})
	if err != nil {
		t.Fatal(err)
	}
	source := &hxcLineageSnapshot{snapshot: hxcport.Snapshot{Complete: true, AsOf: now, Digest: [32]byte{1}, Rows: []hxcdomain.SourceRow{projection.Rows[0].SourceRow}}}
	service := hxcapp.Service{Enabled: true, Scope: "wechat-open-platform:hxc-lineage", SubjectKey: []byte(strings.Repeat("s", 32)), Source: source, Identity: identityapp.HXCSourceService{Inspector: queries, Store: identityRepository, OneID: oneID, VerifiedIdentity: identityadapter.HXCVerifiedUnionIDFactory{Enabled: true}}, RegistrationCoverage: queries, UnionIDVerified: true, Store: store, Enqueuer: enqueuer, Audit: audit, UOW: uow, Now: func() time.Time { return now }}
	worker.Service = &service
	run, replay, err := service.Create(ctx, "hxc-lineage-publication", "initial", 0)
	if err != nil || replay {
		t.Fatalf("create: %+v replay=%t %v", run, replay, err)
	}
	replayRun, replay, err := service.Create(ctx, "hxc-lineage-publication", "initial", 0)
	if err != nil || !replay || replayRun.ID != run.ID {
		t.Fatalf("create replay: %+v replay=%t %v", replayRun, replay, err)
	}
	if err = worker.Work(ctx, &river.Job[hxcworker.Args]{Args: hxcworker.Args{RunID: run.ID}}); err != nil {
		t.Fatal(err)
	}
	published, err := service.Get(ctx, run.ID)
	if err != nil || published.Status != hxcdomain.RefreshSucceeded || published.ProjectionID == nil {
		t.Fatalf("published: %+v %v", published, err)
	}
	version := *published.ProjectionID
	if err = worker.Work(ctx, &river.Job[hxcworker.Args]{Args: hxcworker.Args{RunID: run.ID}}); err != nil {
		t.Fatal(err)
	}
	var jobs, versions, audits int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM river_job WHERE kind='hxc.dashboard-refresh.v1'),(SELECT count(*) FROM hxc_dashboard_versions),(SELECT count(*) FROM audit_events WHERE action LIKE 'hxc.dashboard_%')`).Scan(&jobs, &versions, &audits); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || versions != 1 || audits != 2 || source.reads != 1 {
		t.Fatalf("replays changed durable facts: jobs=%d versions=%d audits=%d source reads=%d", jobs, versions, audits, source.reads)
	}
	before, err := store.SharedFactsAtVersion(ctx, version, []customerdomain.CustomerID{a})
	if err != nil || !before[a].IsMember {
		t.Fatalf("pre-merge source fact missing: %v", err)
	}
	var link identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		link, e = oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: a, Target: bFact, Evidence: identitydomain.LinkEvidence{Type: "test", Strength: identitydomain.EvidenceStrong, Source: "hxc.lineage.test", EventID: "hxc-lineage", Digest: "synthetic-hxc-lineage", PolicyVersion: "test-v1"}})
		return e
	}); err != nil || link.Candidate == nil {
		t.Fatalf("candidate: %v", err)
	}
	var merged identityapp.LinkResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		merged, e = oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: link.Candidate.ID, SurvivorCustomerID: b, Operator: "hxc-lineage-test"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	facts, err := store.SharedFactsAtVersion(ctx, version, []customerdomain.CustomerID{b})
	if err != nil || facts[b].Availability != hxcport.SharedFactsAvailable || !facts[b].IsMember || !facts[b].HasTokenUsage || facts[b].CustomerID != b {
		t.Fatalf("canonical read lost published alias fact: found=%t availability=%s member=%t error=%v", len(facts) > 0, facts[b].Availability, facts[b].IsMember, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		registered, e := store.RegistrationFacts(tx, []customerdomain.CustomerID{b}, now)
		if e != nil {
			return e
		}
		if registered[b].State != "registered" || registered[b].Version != version {
			t.Errorf("canonical registration lost: %+v", registered[b])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var sourceCustomer int64
	if err = native.QueryRow(ctx, `SELECT customer_id FROM hxc_dashboard_rows WHERE projection_id=$1`, version).Scan(&sourceCustomer); err != nil || sourceCustomer != int64(a) {
		t.Fatalf("immutable HXC generation must retain source customer: %d %v", sourceCustomer, err)
	}
	// Both stale aliases and the current root read the same fact; duplicated
	// input cannot turn a unique observation into an ambiguous one.
	aliases, err := store.SharedFactsAtVersion(ctx, version, []customerdomain.CustomerID{a, b, b})
	if err != nil || len(aliases) != 2 || aliases[a].CustomerID != b || !aliases[a].IsMember || aliases[b].Availability != hxcport.SharedFactsAvailable {
		t.Fatalf("alias/duplicate read: %+v %v", aliases, err)
	}
	// Two distinct legacy accounts now in one lineage remain ambiguous;
	// never arbitrarily add membership/quota or choose a conflicting negative.
	conflicting := projection
	conflicting.AsOf = now.Add(time.Second)
	other := projection.Rows[0]
	other.CustomerID = b
	other.SubjectDigest = [32]byte{2}
	other.UserRef = "HXC-000000000002"
	conflicting.Rows = []hxcdomain.ProjectionRow{projection.Rows[0], other}
	conflicting.Counts.Total = 2
	conflicting.Counts.Matched = 2
	conflicting.Counts.MatchedByUnionID = 2
	conflicting.Counts.ActiveUsed = 2
	conflicting.RegistrationCoverage = map[int64]string{int64(a): "registered", int64(b): "unregistered"}
	var legacyRun, conflictingVersion int64
	if err = native.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(run_key,request_digest,trigger,identity_mode,status,source_count,processed_count) VALUES('hxc-legacy-ambiguity',$1,'initial','inspect','publishing',2,2) RETURNING id`, make([]byte, 32)).Scan(&legacyRun); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		conflictingVersion, e = store.Publish(tx, legacyRun, conflicting)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := store.SharedFactsAtVersion(ctx, conflictingVersion, []customerdomain.CustomerID{b})
	if err != nil || ambiguous[b].Availability != hxcport.SharedFactsAmbiguous || ambiguous[b].IsMember {
		t.Fatalf("ambiguous aliases presented membership: %+v %v", ambiguous, err)
	}
	pinned, err := store.SharedFactsAtVersion(ctx, version, []customerdomain.CustomerID{b})
	if err != nil || pinned[b].Availability != hxcport.SharedFactsAvailable || !pinned[b].IsMember {
		t.Fatalf("new publication changed pinned old version: %+v %v", pinned, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		facts, e := store.RegistrationFacts(tx, []customerdomain.CustomerID{b}, conflicting.AsOf)
		if e != nil {
			return e
		}
		if facts[b].State != "unknown" || facts[b].Version != conflictingVersion {
			t.Errorf("conflicting coverage must stay unknown: %+v", facts)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if merged.Merge == nil {
		t.Fatal("merge receipt missing")
	}
	if err = uow.Within(ctx, func(tx context.Context) error { _, e := oneID.RevertConfirmedMerge(tx, merged.Merge.ID); return e }); err != nil {
		t.Fatal(err)
	}
	restored, err := store.SharedFactsAtVersion(ctx, version, []customerdomain.CustomerID{a, b})
	if err != nil || !restored[a].IsMember || restored[a].CustomerID != a {
		t.Fatalf("reverse lost original source: %+v %v", restored, err)
	}
	if _, exists := restored[b]; exists {
		t.Fatal("reversed root borrowed another customer's HXC facts")
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		facts, e := store.RegistrationFacts(tx, []customerdomain.CustomerID{a, b}, now)
		if e != nil {
			return e
		}
		if facts[a].State != "registered" || facts[b].State != "unknown" {
			t.Errorf("reversal coverage: %+v", facts)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

}

type hxcLineageSnapshot struct {
	snapshot hxcport.Snapshot
	reads    int
}

func (*hxcLineageSnapshot) Ready() bool                     { return true }
func (*hxcLineageSnapshot) Preflight(context.Context) error { return nil }
func (s *hxcLineageSnapshot) ReadSnapshot(context.Context, time.Time) (hxcport.Snapshot, error) {
	s.reads++
	return s.snapshot, nil
}
