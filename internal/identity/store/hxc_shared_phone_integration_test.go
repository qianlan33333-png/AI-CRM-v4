package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"testing"

	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestHXCSharedPhoneAccountsIntegration(t *testing.T) {
	pool, cleanup := identityPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Native().Exec(ctx, `CREATE TABLE survey_submissions(id BIGINT PRIMARY KEY); CREATE TABLE survey_submission_answers(id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0038_survey_oauth_phone_vault.sql", "0063_identity_hxc_source_observations.sql"} {
		raw, err := os.ReadFile(identityMigrationNamed(t, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Native().Exec(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	phone, _ := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32)))
	observations, _ := identitysecure.NewObservationVault(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32)))
	repository := NewPostgresStoreWithObservation(phone, observations)
	uow, _ := platformpostgres.NewUnitOfWork(pool)
	one := identityapp.OneIDService{Store: repository}
	source := identityapp.HXCSourceService{Inspector: identityquery.NewPostgreSQL(phone), Store: repository, OneID: one, VerifiedIdentity: identityadapter.HXCVerifiedUnionIDFactory{Enabled: true}}
	within := func(fn func(context.Context) error) {
		t.Helper()
		if err := uow.Within(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}
	var unionRoot, phoneRoot identityport.ProvisionResult
	within(func(tx context.Context) error {
		var err error
		unionRoot, err = one.ProvisionCustomerFromVerifiedIdentity(tx, testFact(t, identitydomain.KindUnionID, "wechat-open-platform:hxc", "shared-union"))
		if err != nil {
			return err
		}
		phoneRoot, err = one.ProvisionCustomerFromVerifiedIdentity(tx, testFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:hxc", "other-phone-account"))
		if err != nil {
			return err
		}
		_, err = one.AttachDeclaredPhoneToCustomer(tx, identityport.DeclaredPhoneCommand{CustomerID: phoneRoot.CustomerID, Phone: "13800138000", Source: "phone_import", SourceEventID: "shared-phone", IdempotencyKey: "shared-phone"})
		return err
	})
	apply := func(subject identityport.HXCSubject) identityport.HXCSubjectResult {
		t.Helper()
		var result identityport.HXCSubjectResult
		within(func(tx context.Context) error {
			var err error
			result, err = source.ApplyHXCSubject(tx, subject)
			return err
		})
		return result
	}
	old := hxcIntegrationSubject("old-shared", "unchanged-payload", "shared-union", "13800138000")
	old.ConflictReason = identityport.HXCReasonDuplicatePhone
	within(func(tx context.Context) error {
		_, err := repository.PersistHXCResolution(tx, old, identityport.HXCSubjectResult{Disposition: identityport.HXCConflict, MatchedBy: identityport.HXCMatchNone, Reason: identityport.HXCReasonDuplicatePhone})
		return err
	})
	old.ConflictReason = "" // the other source account can disappear from a later batch
	var preview []identityport.HXCSubjectResult
	within(func(tx context.Context) error {
		var err error
		preview, err = source.InspectHXCSubjects(tx, []identityport.HXCSubject{old})
		return err
	})
	if len(preview) != 1 || preview[0].CustomerID != unionRoot.CustomerID || preview[0].MatchedBy != identityport.HXCMatchUnionID {
		t.Fatalf("preview=%+v", preview)
	}
	first := apply(old)
	if first.CustomerID != unionRoot.CustomerID || first.MatchedBy != identityport.HXCMatchUnionID || first.Replayed {
		t.Fatalf("corrected=%+v", first)
	}
	if replay := apply(old); !replay.Replayed || replay.CustomerID != unionRoot.CustomerID {
		t.Fatalf("replay=%+v", replay)
	}
	old.PayloadDigest = sha256.Sum256([]byte("changed-source-payload"))
	if changed := apply(old); changed.CustomerID != unionRoot.CustomerID || changed.MatchedBy != identityport.HXCMatchUnionID {
		t.Fatalf("changed=%+v", changed)
	}
	var historical, open, oldReceipts, newReceipts, phoneObserved int
	if err := pool.Native().QueryRow(ctx, `SELECT
 (SELECT count(*) FROM identity_source_conflicts WHERE reason_code='duplicate_hxc_phone' AND status='resolved'),
 (SELECT count(*) FROM identity_source_conflicts WHERE status='open'),
 (SELECT count(*) FROM identity_source_resolution_receipts WHERE disposition='conflict'),
 (SELECT count(*) FROM identity_source_resolution_receipts WHERE disposition='matched'),
 (SELECT count(*) FROM identity_source_observations WHERE kind='phone' AND status='active')`).Scan(&historical, &open, &oldReceipts, &newReceipts, &phoneObserved); err != nil {
		t.Fatal(err)
	}
	if historical != 1 || open != 0 || oldReceipts != 1 || newReceipts != 2 || phoneObserved != 1 {
		t.Fatalf("history=%d open=%d old=%d new=%d phone=%d", historical, open, oldReceipts, newReceipts, phoneObserved)
	}
	var cipher []byte
	if err := pool.Native().QueryRow(ctx, `SELECT ciphertext FROM identity_source_observations WHERE kind='phone' AND status='active'`).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if plaintext, err := phone.Decrypt(cipher); err != nil || plaintext != "13800138000" {
		t.Fatal("shared phone observation lost")
	}
	legacy := hxcIntegrationSubject("legacy-phone-match", "legacy-unchanged", "shared-union", "13800138000")
	within(func(tx context.Context) error {
		_, err := repository.PersistHXCResolution(tx, legacy, identityport.HXCSubjectResult{Disposition: identityport.HXCMatched, MatchedBy: identityport.HXCMatchPhone, Reason: identityport.HXCReasonMatchedPhone, CustomerID: phoneRoot.CustomerID})
		return err
	})
	legacy.ConflictReason = identityport.HXCReasonDuplicatePhone
	within(func(tx context.Context) error {
		_, err := repository.PersistHXCResolution(tx, legacy, identityport.HXCSubjectResult{Disposition: identityport.HXCConflict, MatchedBy: identityport.HXCMatchNone, Reason: identityport.HXCReasonDuplicatePhone})
		return err
	})
	legacy.ConflictReason = ""
	if got := apply(legacy); got.CustomerID != unionRoot.CustomerID || got.MatchedBy != identityport.HXCMatchUnionID || got.Replayed {
		t.Fatalf("legacy phone receipt overrode trustworthy UnionID: %+v", got)
	}
	if got := apply(legacy); !got.Replayed {
		t.Fatal("corrected legacy receipt was not idempotent")
	}

	cases := []struct {
		name, union, scope string
		verified           bool
		reason             identityport.HXCReason
		disposition        identityport.HXCDisposition
	}{
		{"new-shared", "shared-union", "wechat-open-platform:hxc", true, identityport.HXCReasonMatchedUnionID, identityport.HXCMatched},
		{"unknown-union", "later-union", "wechat-open-platform:hxc", true, identityport.HXCReasonNoMatch, identityport.HXCUnmatched},
		{"missing-union", "", "wechat-open-platform:hxc", true, identityport.HXCReasonMissingIdentity, identityport.HXCUnmatched},
		{"declared-union", "shared-union", "wechat-open-platform:hxc", false, identityport.HXCReasonNoMatch, identityport.HXCUnmatched},
		{"different-scope", "shared-union", "wechat-open-platform:other", true, identityport.HXCReasonNoMatch, identityport.HXCUnmatched},
		{"invalid-scope", "shared-union", "invalid", true, identityport.HXCReasonInvalidUnionID, identityport.HXCInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := hxcIntegrationSubject(tc.name, tc.name, tc.union, "13800138000")
			subject.UnionIDScope = tc.scope
			subject.UnionIDVerified = tc.verified
			subject.ConflictReason = identityport.HXCReasonDuplicatePhone
			got := apply(subject)
			if got.Reason != tc.reason || got.Disposition != tc.disposition {
				t.Fatalf("result=%+v", got)
			}
			if tc.disposition != identityport.HXCMatched && got.CustomerID != 0 {
				t.Fatal("ambiguous phone supplied customer")
			}
			// Receipt marker, rather than an open conflict, protects newly observed accounts.
			subject.ConflictReason = ""
			subject.PayloadDigest = sha256.Sum256([]byte(tc.name + "-changed"))
			got = apply(subject)
			if got.Reason != tc.reason || got.Disposition != tc.disposition {
				t.Fatalf("without duplicate flag=%+v", got)
			}
		})
	}
	later := hxcIntegrationSubject("unknown-union", "unknown-union", "later-union", "13800138000")
	within(func(tx context.Context) error {
		_, err := one.ProvisionCustomerFromVerifiedIdentity(tx, testFact(t, identitydomain.KindUnionID, "wechat-open-platform:hxc", "later-union"))
		return err
	})
	if got := apply(later); got.MatchedBy != identityport.HXCMatchUnionID || got.CustomerID == 0 || got.Replayed {
		t.Fatalf("new trusted root=%+v", got)
	}
	if got := apply(later); !got.Replayed {
		t.Fatalf("new root retry=%+v", got)
	}
	old.PayloadDigest = sha256.Sum256([]byte("unchanged-payload"))
	old.ConflictReason = identityport.HXCReasonDuplicateUnionID
	if got := apply(old); got.Disposition != identityport.HXCConflict || got.Reason != identityport.HXCReasonDuplicateUnionID {
		t.Fatalf("strong conflict bypassed=%+v", got)
	}
	if got := apply(old); !got.Replayed || got.Reason != identityport.HXCReasonDuplicateUnionID {
		t.Fatalf("batch conflict change did not append an idempotent receipt: %+v", got)
	}
	var roots, phoneBindings, merges, identityConflicts int
	if err := pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM customers),(SELECT count(*) FROM customer_identities WHERE kind='phone'),(SELECT count(*) FROM customer_merge_candidates),(SELECT count(*) FROM customer_identity_conflicts)`).Scan(&roots, &phoneBindings, &merges, &identityConflicts); err != nil {
		t.Fatal(err)
	}
	if roots != 3 || phoneBindings != 1 || merges != 0 || identityConflicts != 0 {
		t.Fatalf("roots=%d phone bindings=%d merges=%d identity conflicts=%d", roots, phoneBindings, merges, identityConflicts)
	}
}
