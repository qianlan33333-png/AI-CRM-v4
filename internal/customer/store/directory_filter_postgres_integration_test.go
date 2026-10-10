package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestDirectoryFilterPredicatesKeepPageAndTotalInLockstepPostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	native, cleanup := directoryFilterPool(t, ctx, url)
	defer cleanup()
	if _, err = native.Exec(ctx, `
		INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,avatar_url,oneid_label,phone_masked,phone_assurance,activation_status,last_synced_at,updated_at) VALUES
		(1,'active','owner-nine-first','','CID-1','','','active',NULL,'2026-09-08T10:00:00Z'),
		(2,'active','owner-nine-tagged','','CID-2','','','active',NULL,'2026-09-08T09:00:00Z'),
		(3,'active','other-owner-tagged','','CID-3','','','active',NULL,'2026-09-08T08:00:00Z');
		INSERT INTO customer_local_owners(customer_id,staff_id) VALUES(1,9),(2,9),(3,8)`); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository := PostgreSQL{}
	var ownerIDs []customerdomain.CustomerID
	var page customerapp.PageData
	err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		// IDs come from the composition-owned WeCom read Port. The Customer store applies only the supplied intersection.
		ownerIDs = []customerdomain.CustomerID{1, 2}
		page, readErr = repository.List(tx, customerapp.Query{
			Limit:     2,
			Watermark: time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
			Filters: customerapp.Filters{
				OwnerCustomerIDs: ownerIDs,
				TagCustomerIDs:   []customerdomain.CustomerID{2, 3},
			},
		})
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerIDs) != 2 || ownerIDs[0] != 1 || ownerIDs[1] != 2 {
		t.Fatalf("ownerIDs=%v", ownerIDs)
	}
	if page.Count != 1 || page.TotalIsEstimate || len(page.Items) != 1 || page.Items[0].CustomerID != 2 || page.Items[0].OwnerStaffID != nil {
		t.Fatalf("page=%+v", page)
	}
}

func TestSearchRadarVisitorCustomersEscapesWildcardCharactersPostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	native, cleanup := directoryFilterPool(t, ctx, url)
	defer cleanup()
	if _, err = native.Exec(ctx, `
		INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,avatar_url,oneid_label,phone_masked,phone_assurance,activation_status,last_synced_at,updated_at) VALUES
		(1,'active','literal%percent','','CID-1','','','active',NULL,CURRENT_TIMESTAMP),
		(2,'active','literal_under','','CID-2','','','active',NULL,CURRENT_TIMESTAMP),
		(3,'active','literal\\slash','','CID-3','','','active',NULL,CURRENT_TIMESTAMP),
		(4,'active','ordinary','','CID-4','','','active',NULL,CURRENT_TIMESTAMP),
		(5,'active','canonical-dirty','','WRONG-CACHED-LABEL','','','active',NULL,CURRENT_TIMESTAMP),
		(6,'active','canonical-missing','','','','','active',NULL,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		search string
		want   customerdomain.CustomerID
	}{
		{search: "%", want: 1},
		{search: "_", want: 2},
		{search: `\`, want: 3},
	} {
		t.Run(testCase.search, func(t *testing.T) {
			var ids []customerdomain.CustomerID
			if err := uow.Within(ctx, func(tx context.Context) error {
				var readErr error
				ids, readErr = PostgreSQL{}.SearchRadarVisitorCustomers(tx, testCase.search, 10)
				return readErr
			}); err != nil {
				t.Fatal(err)
			}
			if len(ids) != 1 || ids[0] != testCase.want {
				t.Fatalf("search %q ids=%v want [%d]", testCase.search, ids, testCase.want)
			}
		})
	}
	for _, testCase := range []struct {
		search string
		want   customerdomain.CustomerID
	}{
		{search: "CID-5", want: 5},
		{search: "CID-6", want: 6},
	} {
		t.Run("canonical_"+testCase.search, func(t *testing.T) {
			var ids []customerdomain.CustomerID
			if err := uow.Within(ctx, func(tx context.Context) error {
				var readErr error
				ids, readErr = PostgreSQL{}.SearchRadarVisitorCustomers(tx, testCase.search, 10)
				return readErr
			}); err != nil {
				t.Fatal(err)
			}
			if len(ids) != 1 || ids[0] != testCase.want {
				t.Fatalf("canonical search %q ids=%v want [%d]", testCase.search, ids, testCase.want)
			}
		})
	}
	var stale []customerdomain.CustomerID
	if err := uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		stale, readErr = PostgreSQL{}.SearchRadarVisitorCustomers(tx, "WRONG-CACHED-LABEL", 10)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("mutable directory oneid label participated in search: %v", stale)
	}
}

func TestProviderProfileCreatesMinimumAndPreservesHigherPriorityNamePostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	native, cleanup := directoryFilterPool(t, ctx, url)
	defer cleanup()
	applyCustomerVerifiedWeChatNicknameMigration(t, ctx, native)
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository := PostgreSQL{}
	observedAt := time.Date(2026, 9, 17, 1, 2, 3, 0, time.UTC)
	if err = uow.Within(ctx, func(tx context.Context) error {
		if activateErr := repository.ActivateDirectoryCustomer(tx, 7, "identity_provision", observedAt.Add(-time.Minute)); activateErr != nil {
			return activateErr
		}
		return repository.ObserveProviderProfile(tx, 7, customerport.ProviderProfileObservation{DisplayName: "微信昵称", AvatarURL: "https://thirdwx.qlogo.cn/avatar", Source: "wechat.payment.h5_oauth.userinfo", ObservedAt: observedAt})
	}); err != nil {
		t.Fatal(err)
	}
	var name, avatar, oneID, source string
	if err = native.QueryRow(ctx, `SELECT display_name,avatar_url,oneid_label,source FROM customer_directory_projection WHERE customer_id=7`).Scan(&name, &avatar, &oneID, &source); err != nil {
		t.Fatal(err)
	}
	if name != "微信昵称" || avatar != "https://thirdwx.qlogo.cn/avatar" || oneID != "CID-7" || source != "wechat.payment.h5_oauth.userinfo" {
		t.Fatalf("name=%q avatar=%q oneID=%q source=%q", name, avatar, oneID, source)
	}
	var nickname string
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		nickname, readErr = repository.ReadWeChatNickname(tx, 7)
		return readErr
	}); err != nil || nickname != "微信昵称" {
		t.Fatalf("trusted WeChat nickname=%q err=%v", nickname, err)
	}
	if _, err = native.Exec(ctx, `UPDATE customer_directory_projection SET display_name='后台手工备注' WHERE customer_id=7`); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repository.ObserveProviderProfile(tx, 7, customerport.ProviderProfileObservation{DisplayName: "更新微信昵称", Source: "wechat.payment.h5_oauth.userinfo", ObservedAt: observedAt.Add(time.Minute)})
	}); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT display_name,source,wechat_nickname FROM customer_directory_projection WHERE customer_id=7`).Scan(&name, &source, &nickname); err != nil {
		t.Fatal(err)
	}
	if name != "更新微信昵称" || source != "wechat.payment.h5_oauth.userinfo" || nickname != "更新微信昵称" {
		t.Fatalf("directory name=%q source=%q trusted nickname=%q", name, source, nickname)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repository.ObserveProviderProfile(tx, 7, customerport.ProviderProfileObservation{DisplayName: "企微同步姓名", Source: "wecom_directory_sync", ObservedAt: observedAt.Add(2 * time.Minute)})
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		nickname, readErr = repository.ReadWeChatNickname(tx, 7)
		return readErr
	}); err != nil || nickname != "更新微信昵称" {
		t.Fatalf("WeCom observation replaced trusted nickname=%q err=%v", nickname, err)
	}
}

func TestWeChatNicknameMigrationDoesNotBackfillOrReadMixedNamePostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	native, cleanup := directoryFilterPool(t, ctx, url)
	defer cleanup()
	if _, err = native.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,source,source_version,updated_at) VALUES(7,'active','旧混合目录名','wechat.payment.h5_oauth.userinfo',1,clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	applyCustomerVerifiedWeChatNicknameMigration(t, ctx, native)
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository := PostgreSQL{}
	var nickname string
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		nickname, readErr = repository.ReadWeChatNickname(tx, 7)
		return readErr
	}); err != nil || nickname != "" {
		t.Fatalf("legacy mixed display name was treated as a trusted nickname: %q %v", nickname, err)
	}
	if _, err = native.Exec(ctx, `UPDATE customer_directory_projection SET display_name='旧目录中的人工改名' WHERE customer_id=7`); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repository.ObserveProviderProfile(tx, 7, customerport.ProviderProfileObservation{DisplayName: "新的微信昵称", Source: "wechat.payment.h5_oauth.userinfo", ObservedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		nickname, readErr = repository.ReadWeChatNickname(tx, 7)
		return readErr
	}); err != nil || nickname != "新的微信昵称" {
		t.Fatalf("trusted OAuth nickname not isolated from mixed name: %q %v", nickname, err)
	}
	profile, err := func() (customerport.SidebarProfile, error) {
		var got customerport.SidebarProfile
		err := uow.Within(ctx, func(tx context.Context) error {
			var readErr error
			got, readErr = repository.ReadSidebarProfile(tx, 7)
			return readErr
		})
		return got, err
	}()
	if err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, updateErr := repository.UpdateSidebarProfile(tx, customerport.SidebarProfileUpdate{CustomerID: 7, DisplayName: "管理员内备注", ExpectedVersion: profile.Version}, [32]byte{}, [32]byte{}, time.Now().UTC())
		return updateErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		nickname, readErr = repository.ReadWeChatNickname(tx, 7)
		return readErr
	}); err != nil || nickname != "新的微信昵称" {
		t.Fatalf("sidebar display edit replaced trusted nickname: %q %v", nickname, err)
	}
}

func applyCustomerVerifiedWeChatNicknameMigration(t *testing.T, ctx context.Context, native *pgxpool.Pool) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Customer store test source")
	}
	path := filepath.Join(filepath.Dir(file), "../../../migrations/0234_customer_verified_wechat_nickname.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(raw)); err != nil {
		t.Fatalf("apply Customer WeChat nickname migration: %v", err)
	}
}

func directoryFilterPool(t *testing.T, ctx context.Context, url string) (*pgxpool.Pool, func()) {
	t.Helper()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 6)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "directory_filter_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `
			CREATE TABLE customer_directory_projection (
				customer_id BIGINT PRIMARY KEY, customer_status TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '',
				avatar_url TEXT NOT NULL DEFAULT '', oneid_label TEXT NOT NULL DEFAULT '', phone_masked TEXT NOT NULL DEFAULT '',
				gender SMALLINT NOT NULL DEFAULT 0, contact_type SMALLINT NOT NULL DEFAULT 0, corp_name TEXT NOT NULL DEFAULT '',
				phone_assurance TEXT NULL, activation_status TEXT NOT NULL DEFAULT 'active', last_synced_at TIMESTAMPTZ NULL,
				source TEXT NOT NULL DEFAULT 'wecom_directory_sync', source_version BIGINT NOT NULL DEFAULT 1,
				updated_at TIMESTAMPTZ NOT NULL
			);
		CREATE TABLE customer_local_owners (customer_id BIGINT PRIMARY KEY, staff_id BIGINT NOT NULL);
		CREATE TABLE customer_sidebar_profiles (
			customer_id BIGINT PRIMARY KEY, profile_source TEXT NOT NULL DEFAULT '', industry TEXT NOT NULL DEFAULT '',
			industry_description TEXT NOT NULL DEFAULT '', needs_blockers_followup TEXT NOT NULL DEFAULT '',
			version BIGINT NOT NULL DEFAULT 1, updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
		);
	`); err != nil {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	return native, func() {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}
