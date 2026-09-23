package store

import (
	"bytes"
	"context"
	"encoding/base64"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"os"
	"testing"
	"time"
)

func TestHXCRegistrationCoverageFullIndexesIntegration(t *testing.T) {
	pool, cleanup := identityPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, e := pool.Native().Exec(ctx, `CREATE TABLE survey_submissions(id BIGINT PRIMARY KEY);CREATE TABLE survey_submission_answers(id BIGINT PRIMARY KEY)`); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(identityMigrationNamed(t, "0038_survey_oauth_phone_vault.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Native().Exec(ctx, string(raw)); e != nil {
		t.Fatal(e)
	}
	vault, e := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	q := identityquery.NewPostgreSQL(vault)
	uow, e := platformpostgres.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	var known, absent, declared customerdomain.CustomerID
	for _, id := range []*customerdomain.CustomerID{&known, &absent, &declared} {
		if e = pool.Native().QueryRow(ctx, `INSERT INTO customers(status)VALUES('active')RETURNING id`).Scan(id); e != nil {
			t.Fatal(e)
		}
	}
	for _, v := range []struct {
		id                            customerdomain.CustomerID
		kind, scope, value, assurance string
	}{{known, "phone", "phone:e164", "+8613800138000", "verified"}, {absent, "phone", "phone:e164", "+8613900139000", "verified"}, {absent, "unionid", "wechat-open-platform:test", "union-absent", "verified"}, {declared, "phone", "phone:e164", "+8613700137000", "declared"}} {
		if _, e = pool.Native().Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)VALUES($1,$2,$3,$4,$5,'test',1,CASE WHEN $5='verified' THEN now() ELSE NULL END)`, v.id, v.kind, v.scope, v.value, v.assurance); e != nil {
			t.Fatal(e)
		}
	}
	base := identityport.HXCSubject{Position: 0, Phone: "13800138000", UnionID: "union-present", UnionIDScope: "wechat-open-platform:test", UnionIDVerified: true, SourceUpdatedAt: time.Now()}
	for _, c := range []struct {
		name         string
		complete     bool
		missingUnion bool
		want         identityport.HXCRegistrationState
	}{{"full", true, false, identityport.HXCUnregistered}, {"partial", false, false, identityport.HXCRegistrationUnknown}, {"legal-empty-union", true, true, identityport.HXCUnregistered}} {
		t.Run(c.name, func(t *testing.T) {
			v := base
			if c.missingUnion {
				v.UnionID = ""
			}
			if e := uow.Within(ctx, func(tx context.Context) error {
				out, e := q.InspectHXCRegistrationCoverage(tx, []identityport.HXCSubject{v}, c.complete)
				if e != nil {
					return e
				}
				if out[known] != identityport.HXCRegistered || out[absent] != c.want || out[declared] != identityport.HXCRegistrationUnknown {
					t.Fatalf("incorrect registration states known=%s absent=%s declared=%s", out[known], out[absent], out[declared])
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
	// Duplicate source keys invalidate negative coverage, even when raw source
	// rows can individually resolve positively.
	if e := uow.Within(ctx, func(tx context.Context) error {
		duplicate := base
		duplicate.Position = 1
		out, e := q.InspectHXCRegistrationCoverage(tx, []identityport.HXCSubject{base, duplicate}, true)
		if e != nil {
			return e
		}
		if out[absent] != identityport.HXCRegistrationUnknown {
			t.Fatal("duplicate index produced negative")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}

	for _, c := range []struct {
		name, phone, union string
		verified           bool
		want               identityport.HXCRegistrationState
	}{
		{"empty-phone", "", "union-present", true, identityport.HXCUnregistered},
		{"both-empty", "", "", true, identityport.HXCUnregistered},
		{"invalid-nonempty-phone", "bad-phone", "union-present", true, identityport.HXCRegistrationUnknown},
		{"unverified-nonempty-union", "13800138000", "union-present", false, identityport.HXCRegistrationUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := base
			v.Phone = c.phone
			v.UnionID = c.union
			v.UnionIDVerified = c.verified
			if e := uow.Within(ctx, func(tx context.Context) error {
				out, e := q.InspectHXCRegistrationCoverage(tx, []identityport.HXCSubject{v}, true)
				if e != nil {
					return e
				}
				if out[absent] != c.want {
					t.Fatalf("state=%s want=%s", out[absent], c.want)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}

}

func TestPostgresStoreResolveDoesNotUseEmptyPhoneFallbackForInternationalNumber(t *testing.T) {
	pool, cleanup := identityPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Native().Exec(ctx, `CREATE TABLE survey_submissions(id BIGINT PRIMARY KEY); CREATE TABLE survey_submission_answers(id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(identityMigrationNamed(t, "0038_survey_oauth_phone_vault.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Native().Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	vault, err := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var customerID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	// This emulates a malformed pre-vault legacy row. It must never be found
	// through the phone fallback when an unrelated international number has no
	// valid CN11 lookup key.
	if _, err = pool.Native().Exec(ctx, `ALTER TABLE customer_identities DROP CONSTRAINT ck_customer_identities_nonempty`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Native().Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version) VALUES($1,'phone','phone:e164','','declared','legacy',1)`, customerID); err != nil {
		t.Fatal(err)
	}
	service := identityapp.OneIDService{Store: NewPostgresStore(vault)}
	var resolved identityport.ResolveResult
	if err = unit.Within(ctx, func(txctx context.Context) error {
		var resolveErr error
		resolved, resolveErr = service.Resolve(txctx, identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:e164", Value: "+1 650 555 0123", Assurance: identitydomain.AssuranceDeclared, Source: "integration"})
		return resolveErr
	}); err != nil {
		t.Fatal(err)
	}
	if resolved.Status != identityport.ResolveNotFound || resolved.CustomerID != 0 {
		t.Fatalf("international phone resolved malformed empty fallback: %+v", resolved)
	}
}
