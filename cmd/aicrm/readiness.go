package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// requiredCurrentReleaseMigrations deliberately names runtime-required schema
// migrations; it is not a directory inventory. One-time migration/import-only
// assets stay outside this gate unless the composed release still reads their
// structure. Optional Provider-read projections are appended only when
// Composition enables the feature that owns them.
func requiredCurrentReleaseMigrations(cfg platformconfig.Runtime) []string {
	required := []string{
		"0001", "0002", "0003", "0004", "0005", "0006", "0007", "0008", "0009", "0010",
		"0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020",
		"0021", "0022", "0023", "0024", "0025", "0026", "0027", "0028", "0029", "0030",
		"0031", "0032", "0033", "0034", "0035", "0036", "0037", "0038", "0039", "0040",
		"0041", "0042", "0043", "0044", "0045", "0046", "0047", "0048", "0049", "0050",
		"0051", "0052", "0053", "0054", "0055", "0056", "0057", "0058", "0059", "0060",
		"0061", "0062", "0063", "0064", "0068", "0069", "0070", "0076", "0077", "0079",
		"0083", "0084", "0085", "0086", "0087", "0088", "0089", "0092", "0093", "0094",
		"0095", "0096", "0097", "0098", "0099", "0100", "0101", "0102", "0103", "0104",
		"0105", "0106", "0107", "0108", "0109", "0115", "0116", "0117", "0118", "0119",
		"0120", "0121", "0122", "0123", "0124", "0125", "0126", "0127", "0128", "0129",
		"0130", "0131", "0132", "0133", "0134", "0135", "0140", "0141", "0142", "0143",
		"0144", "0145", "0146", "0147", "0148", "0149", "0150", "0151", "0152", "0153", "0155",
		"0156", "0157", "0158", "0159", "0160", "0161", "0164", "0170", "0171", "0172", "0173", "0174", "0175", "0176", "0177", "0178", "0181", "0183", "0185", "0186", "0187", "0188", "0189", "0190", "0191", "0192", "0193",
	}
	if cfg.WeCom.ChannelProviderReadEnabled {
		required = append(required, "0136", "0137", "0139")
	}
	if cfg.HXCDashboard.Enabled {
		required = append(required, "0138")
	}
	sort.Strings(required)
	return required
}

// checkCurrentReleaseSchema verifies the migration ledger and global columns
// whose owner has no module registration. Domain tables and columns remain
// checked by their owning ModuleRegistration.
func checkCurrentReleaseSchema(ctx context.Context, pool *pgxpool.Pool, cfg platformconfig.Runtime) error {
	if pool == nil {
		return errors.New("database schema is not ready: pool is unavailable")
	}
	var missing []string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(array_agg(required.version ORDER BY required.version), ARRAY[]::text[])
		FROM unnest($1::text[]) AS required(version)
		WHERE NOT EXISTS (
			SELECT 1 FROM platform_schema_migrations applied WHERE applied.version=required.version
		)`, requiredCurrentReleaseMigrations(cfg)).Scan(&missing); err != nil {
		return fmt.Errorf("database schema is not ready: read migration ledger: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("database schema is not ready: missing applied migrations %s", strings.Join(missing, ","))
	}
	var hasAllianceColumn bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='order_service_entitlements' AND column_name='alliance'
	)`).Scan(&hasAllianceColumn); err != nil {
		return fmt.Errorf("database schema is not ready: inspect order entitlement columns: %w", err)
	}
	if !hasAllianceColumn {
		return errors.New("database schema is not ready: order_service_entitlements.alliance is missing")
	}
	var hasPostPurchaseActionColumn bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='order_checkout_snapshots' AND column_name='post_purchase_action'
	)`).Scan(&hasPostPurchaseActionColumn); err != nil {
		return fmt.Errorf("database schema is not ready: inspect checkout snapshot columns: %w", err)
	}
	if !hasPostPurchaseActionColumn {
		return errors.New("database schema is not ready: order_checkout_snapshots.post_purchase_action is missing")
	}
	var missingMinimumCustomer bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM customers customer
		LEFT JOIN customer_directory_projection directory ON directory.customer_id=customer.id
		WHERE customer.status='active' AND directory.customer_id IS NULL
	)`).Scan(&missingMinimumCustomer); err != nil {
		return fmt.Errorf("database schema is not ready: inspect minimum customer directory: %w", err)
	}
	if missingMinimumCustomer {
		return errors.New("database schema is not ready: active customer is missing minimum directory projection")
	}
	return nil
}
