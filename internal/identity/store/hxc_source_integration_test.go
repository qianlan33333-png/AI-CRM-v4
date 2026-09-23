package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestHXCSourceDualKeyPersistenceIntegration(t *testing.T) {
	pool, cleanup := identityPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Native().Exec(ctx, `CREATE TABLE survey_submissions(id BIGINT PRIMARY KEY); CREATE TABLE survey_submission_answers(id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0038_survey_oauth_phone_vault.sql", "0063_identity_hxc_source_observations.sql"} {
		contents, err := os.ReadFile(identityMigrationNamed(t, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Native().Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	phoneKey := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	observationKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32))
	phoneVault, err := identitysecure.NewPhoneVault(phoneKey)
	if err != nil {
		t.Fatal(err)
	}
	observationVault, err := identitysecure.NewObservationVault(observationKey)
	if err != nil {
		t.Fatal(err)
	}
	repository := NewPostgresStoreWithObservation(phoneVault, observationVault)
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	oneID := identityapp.OneIDService{Store: repository}
	coordinator := identityapp.HXCSourceService{Inspector: identityquery.NewPostgreSQL(phoneVault), Store: repository, OneID: oneID, VerifiedIdentity: identityadapter.HXCVerifiedUnionIDFactory{Enabled: true}}

	unionFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:hxc", Value: "union-existing", Source: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	var unionRoot identityport.ProvisionResult
	if err = uow.Within(ctx, func(txctx context.Context) error {
		unionRoot, err = oneID.ProvisionCustomerFromVerifiedIdentity(txctx, unionFact)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	subject := hxcIntegrationSubject("subject-union", "payload-union", "union-existing", "13800138000")
	var first identityport.HXCSubjectResult
	if err = uow.Within(ctx, func(txctx context.Context) error {
		first, err = coordinator.ApplyHXCSubject(txctx, subject)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if first.Disposition != identityport.HXCMatched || first.MatchedBy != identityport.HXCMatchUnionID || first.CustomerID != unionRoot.CustomerID {
		t.Fatalf("union match=%+v", first)
	}
	var replay identityport.HXCSubjectResult
	if err = uow.Within(ctx, func(txctx context.Context) error {
		replay, err = coordinator.ApplyHXCSubject(txctx, subject)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.CustomerID != unionRoot.CustomerID {
		t.Fatalf("replay=%+v", replay)
	}

	var customers, subjects, observations, receipts int64
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM customers),(SELECT count(*) FROM identity_source_subjects),(SELECT count(*) FROM identity_source_observations),(SELECT count(*) FROM identity_source_resolution_receipts)`).Scan(&customers, &subjects, &observations, &receipts); err != nil {
		t.Fatal(err)
	}
	if customers != 1 || subjects != 1 || observations != 2 || receipts != 1 {
		t.Fatalf("after replay customers=%d subjects=%d observations=%d receipts=%d", customers, subjects, observations, receipts)
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE identity_source_resolution_receipts SET reason_code='changed'`); err == nil {
		t.Fatal("immutable HXC resolution receipt accepted mutation")
	}
	var unionCipher, phoneCipher []byte
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT ciphertext FROM identity_source_observations WHERE kind='unionid'),(SELECT ciphertext FROM identity_source_observations WHERE kind='phone')`).Scan(&unionCipher, &phoneCipher); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(unionCipher, []byte("union-existing")) || bytes.Contains(phoneCipher, []byte("13800138000")) {
		t.Fatal("identity observation ciphertext contains plaintext")
	}
	if decoded, decryptErr := observationVault.Decrypt("unionid", "wechat-open-platform:hxc", unionCipher); decryptErr != nil || decoded != "union-existing" {
		t.Fatalf("union observation decrypt failed")
	}
	if decoded, decryptErr := phoneVault.Decrypt(phoneCipher); decryptErr != nil || decoded != "13800138000" {
		t.Fatalf("phone observation decrypt failed")
	}

	pending := hxcIntegrationSubject("subject-pending", "payload-pending", "union-new", "13900139000")
	var pendingResult identityport.HXCSubjectResult
	if err = uow.Within(ctx, func(txctx context.Context) error {
		pendingResult, err = coordinator.ApplyHXCSubject(txctx, pending)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if pendingResult.Disposition != identityport.HXCUnmatched || pendingResult.CustomerID != 0 {
		t.Fatalf("pending=%+v", pendingResult)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&customers); err != nil || customers != 1 {
		t.Fatalf("unmatched HXC subject provisioned a customer: count=%d err=%v", customers, err)
	}

	phoneRootFact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:hxc", Value: "phone-root", Source: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	var phoneRoot identityport.ProvisionResult
	if err = uow.Within(ctx, func(txctx context.Context) error {
		phoneRoot, err = oneID.ProvisionCustomerFromVerifiedIdentity(txctx, phoneRootFact)
		if err != nil {
			return err
		}
		_, err = oneID.AttachDeclaredPhoneToCustomer(txctx, identityport.DeclaredPhoneCommand{CustomerID: phoneRoot.CustomerID, Phone: "13700137000", Source: "hxc", SourceEventID: "integration-phone", IdempotencyKey: "integration-phone-key"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	phoneSubject := hxcIntegrationSubject("subject-phone", "payload-phone", "union-attached-from-phone", "13700137000")
	var phoneResult identityport.HXCSubjectResult
	if err = uow.Within(ctx, func(txctx context.Context) error {
		phoneResult, err = coordinator.ApplyHXCSubject(txctx, phoneSubject)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if phoneResult.Disposition != identityport.HXCMatched || phoneResult.MatchedBy != identityport.HXCMatchPhone || phoneResult.CustomerID != phoneRoot.CustomerID {
		t.Fatalf("phone match=%+v", phoneResult)
	}
	var attachedOwner customerdomain.CustomerID
	if err = pool.Native().QueryRow(ctx, `SELECT customer_id FROM customer_identities WHERE kind='unionid' AND scope_key='wechat-open-platform:hxc' AND normalized_value='union-attached-from-phone' AND status='active'`).Scan(&attachedOwner); err != nil || attachedOwner != phoneRoot.CustomerID {
		t.Fatalf("union identity was not attached to phone root: owner=%d err=%v", attachedOwner, err)
	}
}

func hxcIntegrationSubject(subjectValue, payloadValue, unionID, phone string) identityport.HXCSubject {
	return identityport.HXCSubject{
		SubjectDigest: sha256.Sum256([]byte(subjectValue)), PayloadDigest: sha256.Sum256([]byte(payloadValue)),
		UnionIDScope: "wechat-open-platform:hxc", UnionID: unionID, UnionIDVerified: true, Phone: phone,
		SourceUpdatedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), RuleVersion: "hxc-current-v2",
	}
}

func identityMigrationNamed(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate HXC integration test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
}
