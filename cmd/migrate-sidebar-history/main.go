package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	productionSourceSystem = "ai-crm-production:150.158.82.186/openclaw_wecom"
	snapshotMarker         = "__AICRM_SIDEBAR_SNAPSHOT__|"
	entitlementMarker      = "__AICRM_SIDEBAR_ENTITLEMENT__|"
	couponMarker           = "__AICRM_SIDEBAR_COUPON__|"
	legacySchemaVersion    = 1
	currentSchemaVersion   = 2
)

type options struct {
	mode, snapshot, sourceStream, unionIDScope, digest string
	confirm                                            bool
	opaqueSource                                       bool
	proofPath, proofKey, proofDigest, corp, output     string
	oaProof, oaKey, oaDigest                           string
	matchedCorp                                        bool
}
type manifest struct {
	SchemaVersion  int                 `json:"schema_version"`
	RunKey         string              `json:"run_key"`
	SourceSystem   string              `json:"source_system"`
	UnionIDScope   string              `json:"unionid_scope"`
	CapturedAt     time.Time           `json:"captured_at"`
	Entitlements   []sourceEntitlement `json:"entitlements"`
	Coupons        []sourceCoupon      `json:"coupons"`
	ResolutionMode string              `json:"resolution_mode,omitempty"`
	OAProofSHA256  string              `json:"oa_proof_sha256,omitempty"`
	ProofSHA256    string              `json:"proof_sha256,omitempty"`
	SourceSHA256   string              `json:"source_sha256,omitempty"`
	CorpID         string              `json:"corp_id,omitempty"`
	rawDigest      [32]byte
}
type sourceEntitlement struct {
	SourceID         int64     `json:"source_id"`
	UnionID          string    `json:"unionid"`
	ServiceProductID int64     `json:"service_product_id"`
	ProductName      string    `json:"product_name"`
	Status           string    `json:"status"`
	StartAt          time.Time `json:"start_at"`
	EndAt            time.Time `json:"end_at"`
	Remark           string    `json:"remark"`
	// Alliance is nil when the source did not collect it or collected a null
	// value. alliancePresent preserves whether a protected manifest actually
	// contained the key: older frozen snapshots had no key and their already
	// imported per-row digest must serialize byte-for-byte as before. New
	// snapshots always carry the key, so null and explicit "" remain distinct
	// source facts.
	Alliance        *string `json:"-"`
	alliancePresent bool
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// MarshalJSON is deliberately version-compatible for historical receipt
// digests. Before alliance existed, the exact normalized row did not contain
// that field; adding a null key would make an existing import unreplayable.
func (row sourceEntitlement) MarshalJSON() ([]byte, error) {
	if !row.alliancePresent {
		return json.Marshal(struct {
			SourceID         int64     `json:"source_id"`
			UnionID          string    `json:"unionid"`
			ServiceProductID int64     `json:"service_product_id"`
			ProductName      string    `json:"product_name"`
			Status           string    `json:"status"`
			StartAt          time.Time `json:"start_at"`
			EndAt            time.Time `json:"end_at"`
			Remark           string    `json:"remark"`
			CreatedAt        time.Time `json:"created_at"`
			UpdatedAt        time.Time `json:"updated_at"`
		}{row.SourceID, row.UnionID, row.ServiceProductID, row.ProductName, row.Status, row.StartAt, row.EndAt, row.Remark, row.CreatedAt, row.UpdatedAt})
	}
	return json.Marshal(struct {
		SourceID         int64     `json:"source_id"`
		UnionID          string    `json:"unionid"`
		ServiceProductID int64     `json:"service_product_id"`
		ProductName      string    `json:"product_name"`
		Status           string    `json:"status"`
		StartAt          time.Time `json:"start_at"`
		EndAt            time.Time `json:"end_at"`
		Remark           string    `json:"remark"`
		Alliance         *string   `json:"alliance"`
		CreatedAt        time.Time `json:"created_at"`
		UpdatedAt        time.Time `json:"updated_at"`
	}{row.SourceID, row.UnionID, row.ServiceProductID, row.ProductName, row.Status, row.StartAt, row.EndAt, row.Remark, row.Alliance, row.CreatedAt, row.UpdatedAt})
}

func (row *sourceEntitlement) UnmarshalJSON(data []byte) error {
	var raw struct {
		SourceID         int64           `json:"source_id"`
		UnionID          string          `json:"unionid"`
		ServiceProductID int64           `json:"service_product_id"`
		ProductName      string          `json:"product_name"`
		Status           string          `json:"status"`
		StartAt          time.Time       `json:"start_at"`
		EndAt            time.Time       `json:"end_at"`
		Remark           string          `json:"remark"`
		Alliance         json.RawMessage `json:"alliance"`
		CreatedAt        time.Time       `json:"created_at"`
		UpdatedAt        time.Time       `json:"updated_at"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	row.SourceID, row.UnionID, row.ServiceProductID, row.ProductName, row.Status = raw.SourceID, raw.UnionID, raw.ServiceProductID, raw.ProductName, raw.Status
	row.StartAt, row.EndAt, row.Remark, row.CreatedAt, row.UpdatedAt = raw.StartAt, raw.EndAt, raw.Remark, raw.CreatedAt, raw.UpdatedAt
	row.Alliance = nil
	row.alliancePresent = len(raw.Alliance) != 0
	if !row.alliancePresent || bytes.Equal(bytes.TrimSpace(raw.Alliance), []byte("null")) {
		return nil
	}
	var alliance string
	if err := json.Unmarshal(raw.Alliance, &alliance); err != nil {
		return err
	}
	row.Alliance = &alliance
	return nil
}

type sourceCoupon struct {
	SourceID   int64      `json:"source_id"`
	UnionID    string     `json:"unionid"`
	CouponID   int64      `json:"coupon_id"`
	Status     string     `json:"status"`
	ClaimedAt  time.Time  `json:"claimed_at"`
	ValidFrom  *time.Time `json:"valid_from"`
	ValidUntil *time.Time `json:"valid_until"`
	RedeemedAt *time.Time `json:"redeemed_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
type result struct {
	Input       int64 `json:"input"`
	Imported    int64 `json:"imported"`
	Replayed    int64 `json:"replayed"`
	Quarantined int64 `json:"quarantined"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sidebar history migration failed:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-sidebar-history", flag.ContinueOnError)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "inspect-stream|inspect|dry-run|preflight|apply|reconcile")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "protected normalized JSON snapshot")
	flags.StringVar(&cfg.sourceStream, "source-stream", "", "read-only psql source stream")
	flags.BoolVar(&cfg.opaqueSource, "opaque-source", false, "capture opaque source references only; external proof binding required before import")
	flags.StringVar(&cfg.unionIDScope, "unionid-scope", "", "verified WeChat Open Platform scope")
	flags.StringVar(&cfg.digest, "manifest-sha256", "", "exact snapshot SHA-256")
	flags.BoolVar(&cfg.confirm, "confirm-apply", false, "confirm exact apply")
	flags.StringVar(&cfg.oaProof, "oa-proof", "", "protected live OA supplemental proof")
	flags.StringVar(&cfg.oaKey, "oa-proof-key-file", "", "OA proof key")
	flags.StringVar(&cfg.oaDigest, "oa-proof-sha256", "", "OA proof exact digest")
	flags.StringVar(&cfg.proofPath, "external-proof", "", "protected same-Corp proof")
	flags.StringVar(&cfg.proofKey, "external-proof-key-file", "", "protected proof key")
	flags.StringVar(&cfg.proofDigest, "external-proof-sha256", "", "exact proof digest")
	flags.StringVar(&cfg.corp, "corp-id", "", "confirmed shared Corp")
	flags.StringVar(&cfg.output, "output-snapshot", "", "exclusive derived manifest destination")
	flags.BoolVar(&cfg.matchedCorp, "confirm-matched-corp", false, "source and target Corp verified equal")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if cfg.opaqueSource && cfg.mode != "inspect-stream" {
		return errors.New("opaque-source is only valid for inspect-stream")
	}
	if cfg.mode == "inspect-stream" {
		if cfg.snapshot == "" || cfg.sourceStream == "" || (cfg.opaqueSource && cfg.unionIDScope != "") || (!cfg.opaqueSource && !strings.HasPrefix(cfg.unionIDScope, "wechat-open-platform:")) {
			return errors.New("inspect-stream requires snapshot, source-stream and either confirmed unionid-scope or exclusive opaque-source")
		}
		m, err := extractStreamMode(cfg.sourceStream, cfg.unionIDScope, cfg.opaqueSource)
		if err != nil {
			return err
		}
		if err = save(cfg.snapshot, m); err != nil {
			return err
		}
		m, err = load(cfg.snapshot)
		if err != nil {
			return err
		}
		return printSummary("inspect-stream", m, !cfg.opaqueSource)
	}
	if cfg.snapshot == "" {
		return errors.New("snapshot is required")
	}
	m, err := load(cfg.snapshot)
	if err != nil {
		return err
	}
	if cfg.mode == "inspect" || cfg.mode == "dry-run" {
		return printSummary(cfg.mode, m, cfg.mode == "dry-run" && !isOpaqueSource(m))
	}
	want, err := hex.DecodeString(cfg.digest)
	if err != nil || len(want) != 32 || string(want) != string(m.rawDigest[:]) {
		return errors.New("manifest digest confirmation mismatch")
	}
	if cfg.mode == "bind-external-proof" {
		return bindExternalProof(cfg, m)
	}
	if isOpaqueSource(m) {
		return errors.New("opaque source requires bind-external-proof before import or reconciliation")
	}
	if m.SchemaVersion == 3 {
		ctx, err = withExternalProof(ctx, cfg, m)
		if err != nil {
			return err
		}
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 8, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.mode == "preflight" {
		return preflight(ctx, pool, m)
	}
	if cfg.mode == "reconcile" {
		return reconcile(ctx, pool, m)
	}
	if cfg.mode != "apply" || !cfg.confirm {
		return errors.New("apply requires --confirm-apply")
	}
	return apply(ctx, pool, m)
}

func printSummary(mode string, m manifest, eligible bool) error {
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"mode": mode, "manifest_sha256": hex.EncodeToString(m.rawDigest[:]), "run_key": m.RunKey,
		"input": len(m.Entitlements) + len(m.Coupons), "entitlements": len(m.Entitlements),
		"coupons": len(m.Coupons), "eligible": eligible, "provider_calls": 0, "oneid_links_created": 0,
	})
}

func preflight(ctx context.Context, pool *platformpostgres.Pool, m manifest) error {
	if isOpaqueSource(m) {
		return errors.New("opaque source must be bound to external proof")
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	blockers := map[string]int64{"identity_not_found": 0, "identity_conflict": 0, "definition_not_mapped": 0}
	var ready int64
	check := func(kind, sourceKind, unionID string, sourceID int64) error {
		_, reason, resolveErr := resolve(ctx, uow, oneID, m.UnionIDScope, unionID)
		if resolveErr != nil {
			return resolveErr
		}
		if reason != "" {
			blockers[reason]++
			return nil
		}
		_, found, mapErr := definitionMap(ctx, pool, m.SourceSystem, kind, sourceKind, sourceID)
		if mapErr != nil {
			return mapErr
		}
		if !found {
			blockers["definition_not_mapped"]++
			return nil
		}
		ready++
		return nil
	}
	for _, row := range m.Entitlements {
		if err = check("product", "service_period_products", row.UnionID, row.ServiceProductID); err != nil {
			return err
		}
	}
	for _, row := range m.Coupons {
		if err = check("coupon", "commerce_coupons", row.UnionID, row.CouponID); err != nil {
			return err
		}
	}
	input := int64(len(m.Entitlements) + len(m.Coupons))
	blocked := input - ready
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"mode": "preflight", "run_key": m.RunKey, "input": input, "ready": ready,
		"blocked": blocked, "blockers": blockers, "provider_calls": 0, "oneid_links_created": 0,
	})
}

func extractStream(path, scope string) (manifest, error) {
	return extractStreamMode(path, scope, false)
}
func extractStreamMode(path, scope string, opaque bool) (manifest, error) {
	if opaque && scope != "" {
		return manifest{}, errors.New("opaque source cannot assert UnionID scope")
	}
	file, err := os.Open(path)
	if err != nil {
		return manifest{}, err
	}
	defer file.Close()
	// Version 2 is emitted only by this capture path. It retains the source
	// presence/type of admin_alliance and normalizes a string using the frozen
	// Python str(...).strip() contract. Existing v1 snapshots are loaded as-is
	// so their protected manifest and row receipts remain byte compatible.
	m := manifest{SchemaVersion: currentSchemaVersion, SourceSystem: productionSourceSystem, UnionIDScope: scope, Entitlements: []sourceEntitlement{}, Coupons: []sourceCoupon{}}
	if opaque {
		m.SchemaVersion = 4
		m.ResolutionMode = "opaque_source_only"
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	markers := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, snapshotMarker):
			if markers != 0 {
				return manifest{}, errors.New("duplicate sidebar snapshot marker")
			}
			captured, parseErr := time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, snapshotMarker))
			if parseErr != nil {
				return manifest{}, errors.New("invalid sidebar snapshot marker")
			}
			m.CapturedAt = captured.UTC()
			markers++
		case strings.HasPrefix(line, entitlementMarker):
			var row sourceEntitlement
			if err = decodeHexJSON(strings.TrimPrefix(line, entitlementMarker), &row); err != nil {
				return manifest{}, err
			}
			normalizeCapturedAlliance(&row)
			m.Entitlements = append(m.Entitlements, row)
		case strings.HasPrefix(line, couponMarker):
			var row sourceCoupon
			if err = decodeHexJSON(strings.TrimPrefix(line, couponMarker), &row); err != nil {
				return manifest{}, err
			}
			m.Coupons = append(m.Coupons, row)
		}
	}
	if err = scanner.Err(); err != nil {
		return manifest{}, err
	}
	if markers != 1 {
		return manifest{}, errors.New("sidebar snapshot marker unavailable")
	}
	digest := sha256.Sum256([]byte(m.CapturedAt.Format(time.RFC3339Nano) + "\x00" + scope))
	m.RunKey = "sidebar-history-" + hex.EncodeToString(digest[:12])
	if err = validate(m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

// normalizeCapturedAlliance implements the old service-period display/write
// rule: Python str(value or "").strip(). The SQL extractor preserves whether
// the JSON key exists and its JSON type; a null stays unknown, an absent key
// stays absent, and a non-string is rejected instead of being guessed as text.
// Python treats U+001C through U+001F as whitespace in addition to Go's
// unicode.IsSpace set, so TrimSpace alone is not an exact implementation.
func normalizeCapturedAlliance(row *sourceEntitlement) {
	if row == nil || !row.alliancePresent || row.Alliance == nil {
		return
	}
	value := strings.TrimFunc(*row.Alliance, func(r rune) bool {
		return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
	})
	row.Alliance = &value
}

func decodeHexJSON(encoded string, target any) error {
	raw, err := hex.DecodeString(encoded)
	if err != nil || len(raw) == 0 || !json.Valid(raw) {
		return errors.New("invalid sidebar source row")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return errors.New("invalid sidebar source row")
	}
	return nil
}

func save(path string, m manifest) error {
	if filepath.Clean(path) != path {
		return errors.New("snapshot path must be clean")
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func load(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	if len(data) == 0 || len(data) > 512<<20 {
		return manifest{}, errors.New("invalid snapshot size")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var m manifest
	if decoder.Decode(&m) != nil {
		return m, errors.New("invalid snapshot JSON")
	}
	m.rawDigest = sha256.Sum256(data)
	if err = validate(m); err != nil {
		return m, err
	}
	return m, nil
}

func validate(m manifest) error {
	if !validSubjectMode(m) || !regexp.MustCompile(`^[A-Za-z0-9._:-]{8,200}$`).MatchString(m.RunKey) || m.SourceSystem != productionSourceSystem || m.CapturedAt.IsZero() || len(m.Entitlements)+len(m.Coupons) > 2_000_000 {
		return errors.New("invalid snapshot manifest")
	}
	seen := map[string]bool{}
	for _, row := range m.Entitlements {
		key := "e:" + strconv.FormatInt(row.SourceID, 10)
		if seen[key] || row.SourceID < 1 || row.UnionID == "" || row.ServiceProductID < 1 || row.ProductName == "" || (row.Status != "active" && row.Status != "expired" && row.Status != "refunded") || row.StartAt.IsZero() || row.EndAt.Before(row.StartAt) || (row.Alliance != nil && utf8.RuneCountInString(*row.Alliance) > 500) {
			return errors.New("invalid entitlement row")
		}
		seen[key] = true
	}
	for _, row := range m.Coupons {
		key := "c:" + strconv.FormatInt(row.SourceID, 10)
		if seen[key] || row.SourceID < 1 || row.UnionID == "" || row.CouponID < 1 || row.ClaimedAt.IsZero() || (row.Status != "available" && row.Status != "reserved" && row.Status != "consumed" && row.Status != "expired") {
			return errors.New("invalid coupon row")
		}
		seen[key] = true
	}
	return nil
}

func apply(ctx context.Context, pool *platformpostgres.Pool, m manifest) error {
	if isOpaqueSource(m) {
		return errors.New("opaque source must be bound to external proof")
	}
	lease, err := acquireSidebarHistoryApplyLease(ctx, pool.Native(), m)
	if err != nil {
		return err
	}
	defer lease.Release()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	orders, err := orderstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	entitlementApp, _ := orderapp.NewEntitlementApplication(uow, orders)
	coupons, err := couponstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	couponApp, _ := couponapp.NewCustomerCouponApplication(uow, coupons)
	batchID, err := beginBatch(ctx, pool, m)
	if err != nil {
		return err
	}
	out := result{Input: int64(len(m.Entitlements) + len(m.Coupons))}
	for _, row := range m.Entitlements {
		raw, _ := json.Marshal(row)
		digest := sha256.Sum256(raw)
		customer, reason, err := resolve(ctx, uow, oneID, m.UnionIDScope, row.UnionID)
		if err != nil {
			return err
		}
		sourceKey := strconv.FormatInt(row.SourceID, 10)
		if reason != "" {
			if err = quarantine(ctx, pool, batchID, "service_period_entitlement", sourceKey, digest, row.UnionID, reason); err != nil {
				return err
			}
			out.Quarantined++
			continue
		}
		productID, found, err := definitionMap(ctx, pool, m.SourceSystem, "product", "service_period_products", row.ServiceProductID)
		if err != nil {
			return err
		}
		if !found {
			if err = quarantine(ctx, pool, batchID, "service_period_entitlement", sourceKey, digest, row.UnionID, "definition_not_mapped"); err != nil {
				return err
			}
			out.Quarantined++
			continue
		}
		item, created, err := entitlementApp.ImportHistoricalEntitlement(ctx, orderport.HistoricalEntitlement{SourceSystem: m.SourceSystem, SourceKey: sourceKey, CustomerID: customer, ServiceProductID: productID, ProductName: row.ProductName, Status: row.Status, StartAt: row.StartAt, EndAt: row.EndAt, Remark: row.Remark, Alliance: row.Alliance, SourceDigest: digest, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
		if err != nil {
			return err
		}
		if err = mapSource(ctx, pool, batchID, "service_period_entitlement", sourceKey, digest, customer, "order_service_entitlements", item.ID, created); err != nil {
			return err
		}
		if created {
			out.Imported++
		} else {
			out.Replayed++
		}
	}
	for _, row := range m.Coupons {
		raw, _ := json.Marshal(row)
		digest := sha256.Sum256(raw)
		customer, reason, err := resolve(ctx, uow, oneID, m.UnionIDScope, row.UnionID)
		if err != nil {
			return err
		}
		sourceKey := strconv.FormatInt(row.SourceID, 10)
		if reason != "" {
			if err = quarantine(ctx, pool, batchID, "coupon_claim", sourceKey, digest, row.UnionID, reason); err != nil {
				return err
			}
			out.Quarantined++
			continue
		}
		couponID, found, err := definitionMap(ctx, pool, m.SourceSystem, "coupon", "commerce_coupons", row.CouponID)
		if err != nil {
			return err
		}
		if !found {
			if err = quarantine(ctx, pool, batchID, "coupon_claim", sourceKey, digest, row.UnionID, "definition_not_mapped"); err != nil {
				return err
			}
			out.Quarantined++
			continue
		}
		status := mapCouponStatus(row.Status)
		item, created, err := couponApp.ImportHistoricalCustomerCoupon(ctx, couponport.HistoricalCustomerCoupon{SourceSystem: m.SourceSystem, SourceKey: sourceKey, CustomerID: customer, CouponID: couponID, Status: status, ClaimNoMasked: historicalCouponClaimNoMasked(row), ClaimedAt: row.ClaimedAt, ValidFrom: row.ValidFrom, ValidUntil: row.ValidUntil, RedeemedAt: row.RedeemedAt, SourceDigest: digest, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
		if err != nil {
			return err
		}
		if err = mapSource(ctx, pool, batchID, "coupon_claim", sourceKey, digest, customer, "coupon_customer_claims", item.ClaimID, created); err != nil {
			return err
		}
		if created {
			out.Imported++
		} else {
			out.Replayed++
		}
	}
	if out.Input != out.Imported+out.Replayed+out.Quarantined {
		return errors.New("migration conservation mismatch")
	}
	tag, err := pool.Native().Exec(ctx, `UPDATE sidebar_history_migration_batches SET imported_count=$2,replayed_count=$3,quarantined_count=$4,status='applied',completed_at=clock_timestamp() WHERE id=$1 AND status='applying'`, batchID, out.Imported, out.Replayed, out.Quarantined)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("migration batch apply outcome was concurrently changed")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "apply", "run_key": m.RunKey, "result": out})
}

func resolve(ctx context.Context, uow platformport.UnitOfWork, oneID identityapp.OneIDService, scope, value string) (int64, string, error) {
	var out identityport.ResolveResult
	err := uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = resolveSidebarSubject(tx, oneID, scope, value)
		return e
	})
	if err != nil {
		return 0, "", err
	}
	if out.Status == identityport.ResolveFound {
		return int64(out.CustomerID), "", nil
	}
	if out.Status == identityport.ResolveConflict {
		return 0, "identity_conflict", nil
	}
	return 0, "identity_not_found", nil
}
func beginBatch(ctx context.Context, pool *platformpostgres.Pool, m manifest) (int64, error) {
	var id int64
	var digest []byte
	var status string
	err := pool.Native().QueryRow(ctx, `INSERT INTO sidebar_history_migration_batches(run_key,manifest_digest,source_system,input_count,status) VALUES($1,$2,$3,$4,'applying') ON CONFLICT(run_key) DO NOTHING RETURNING id`, m.RunKey, m.rawDigest[:], m.SourceSystem, len(m.Entitlements)+len(m.Coupons)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = pool.Native().QueryRow(ctx, `SELECT id,manifest_digest,status FROM sidebar_history_migration_batches WHERE run_key=$1`, m.RunKey).Scan(&id, &digest, &status)
		if err == nil && string(digest) != string(m.rawDigest[:]) {
			return 0, errors.New("batch replay mismatch")
		}
		if err != nil {
			return 0, err
		}
		if status != "applying" && status != "applied" && status != "reconciled" {
			return 0, fmt.Errorf("migration batch cannot be applied from status %q", status)
		}
		tag, updateErr := pool.Native().Exec(ctx, `UPDATE sidebar_history_migration_batches SET status='applying',completed_at=NULL WHERE id=$1 AND status=$2`, id, status)
		if updateErr != nil {
			return 0, updateErr
		}
		if tag.RowsAffected() != 1 {
			return 0, errors.New("migration batch apply status was concurrently changed")
		}
	}
	return id, err
}

// sidebarHistoryApplyLease is deliberately held on one PostgreSQL session for
// the complete command. Batch status records progress but cannot identify a
// live process: an interrupted command must be replayable from its immutable
// receipts. PostgreSQL releases this lease when that process/connection dies.
type sidebarHistoryApplyLease struct {
	connection *pgxpool.Conn
	firstKey   int32
	secondKey  int32
}

func acquireSidebarHistoryApplyLease(ctx context.Context, pool *pgxpool.Pool, m manifest) (*sidebarHistoryApplyLease, error) {
	if pool == nil {
		return nil, errors.New("migration batch apply pool is required")
	}
	digest := sha256.Sum256([]byte("aicrm:sidebar-history:apply:" + m.RunKey))
	firstKey := int32(binary.BigEndian.Uint32(digest[:4]))
	secondKey := int32(binary.BigEndian.Uint32(digest[4:8]))
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var acquired bool
	if err = connection.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::integer,$2::integer)`, firstKey, secondKey).Scan(&acquired); err != nil {
		connection.Release()
		return nil, err
	}
	if !acquired {
		connection.Release()
		return nil, errors.New("migration batch apply is already in progress")
	}
	return &sidebarHistoryApplyLease{connection: connection, firstKey: firstKey, secondKey: secondKey}, nil
}

func (lease *sidebarHistoryApplyLease) Release() {
	if lease == nil || lease.connection == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = lease.connection.Exec(ctx, `SELECT pg_advisory_unlock($1::integer,$2::integer)`, lease.firstKey, lease.secondKey)
	lease.connection.Release()
	lease.connection = nil
}
func definitionMap(ctx context.Context, pool *platformpostgres.Pool, source, domain, kind string, id int64) (int64, bool, error) {
	var target int64
	err := pool.Native().QueryRow(ctx, `SELECT target_id FROM config_definition_import_source_maps WHERE source_system=$1 AND domain=$2 AND source_kind=$3 AND source_key=$4`, source, domain, kind, strconv.FormatInt(id, 10)).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return target, err == nil, err
}
func quarantine(ctx context.Context, pool *platformpostgres.Pool, batch int64, kind, key string, digest [32]byte, subject, reason string) error {
	subjectDigest := sha256.Sum256([]byte(subject))
	tx, err := pool.Native().BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockMigrationSourceOutcomes(ctx, tx, batch); err != nil {
		return err
	}
	var mapped bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sidebar_history_migration_source_map WHERE batch_id=$1 AND source_kind=$2 AND source_key=$3)`, batch, kind, key).Scan(&mapped); err != nil {
		return err
	}
	if mapped {
		// A successfully imported source may not be silently converted into a
		// quarantine just because a later replay has lost its current identity
		// proof. Reconcile must expose that evidence drift instead.
		return errors.New("source outcome is already mapped")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sidebar_history_migration_quarantine(batch_id,source_kind,source_key,source_digest,subject_digest,reason) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(batch_id,source_kind,source_key) DO NOTHING`, batch, kind, key, digest[:], subjectDigest[:], reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// lockMigrationSourceOutcomes serializes the two mutually exclusive receipt
// types for one batch. There is no cross-table unique constraint, so both the
// map and quarantine writers lock their common batch parent before deciding
// the source outcome.
func lockMigrationSourceOutcomes(ctx context.Context, tx pgx.Tx, batch int64) error {
	var locked int64
	if err := tx.QueryRow(ctx, `SELECT id FROM sidebar_history_migration_batches WHERE id=$1 FOR UPDATE`, batch).Scan(&locked); err != nil {
		return err
	}
	return nil
}

func mapSource(ctx context.Context, pool *platformpostgres.Pool, batch int64, kind, key string, digest [32]byte, customer int64, table string, target int64, created bool) error {
	disposition := "replayed"
	if created {
		disposition = "imported"
	}
	tx, err := pool.Native().BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockMigrationSourceOutcomes(ctx, tx, batch); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO sidebar_history_migration_source_map(batch_id,source_kind,source_key,source_digest,customer_id,target_table,target_id,disposition) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(batch_id,source_kind,source_key) DO NOTHING`, batch, kind, key, digest[:], customer, table, target, disposition)
	if err != nil {
		return err
	}
	// A source row can become resolvable after a prior identity or definition
	// quarantine. The new immutable map replaces that outcome atomically, so a
	// replay cannot leave both receipts and falsely fail conservation. Do not
	// delete a quarantine when the map already existed: that is evidence drift
	// for Reconcile rather than a recovery to repair implicitly.
	if tag.RowsAffected() == 1 {
		if _, err = tx.Exec(ctx, `DELETE FROM sidebar_history_migration_quarantine WHERE batch_id=$1 AND source_kind=$2 AND source_key=$3`, batch, kind, key); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type reconciliationMapping struct {
	targetTable      string
	targetID         int64
	customerID       int64
	quarantineReason string
}

type reconciliationQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// reconciliationSourceOutcome establishes one immutable outcome for every
// protected source row. A mapped row must retain its current manifest digest;
// an unresolved row has no target and instead retains the quarantine receipt's
// digest, subject proof, and reason. This prevents a legitimate quarantine
// from being mistaken for a missing map while still rejecting receipt drift.
func reconciliationSourceOutcome(ctx context.Context, db reconciliationQueryer, batchID int64, kind, key string, sourceDigest [32]byte, subject string) (reconciliationMapping, bool, error) {
	var mapping reconciliationMapping
	var mappedDigest []byte
	err := db.QueryRow(ctx, `SELECT target_table,target_id,customer_id,source_digest
		FROM sidebar_history_migration_source_map
		WHERE batch_id=$1 AND source_kind=$2 AND source_key=$3`, batchID, kind, key).Scan(&mapping.targetTable, &mapping.targetID, &mapping.customerID, &mappedDigest)
	if err == nil {
		if len(mappedDigest) != len(sourceDigest) || !bytes.Equal(mappedDigest, sourceDigest[:]) {
			return reconciliationMapping{}, false, errors.New("reconciliation source map digest mismatch")
		}
		return mapping, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return reconciliationMapping{}, false, err
	}
	var quarantinedDigest, subjectDigest []byte
	var reason string
	err = db.QueryRow(ctx, `SELECT source_digest,subject_digest,reason
		FROM sidebar_history_migration_quarantine
		WHERE batch_id=$1 AND source_kind=$2 AND source_key=$3`, batchID, kind, key).Scan(&quarantinedDigest, &subjectDigest, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return reconciliationMapping{}, false, errors.New("reconciliation source outcome missing")
	}
	if err != nil {
		return reconciliationMapping{}, false, err
	}
	wantSubject := sha256.Sum256([]byte(subject))
	if len(quarantinedDigest) != len(sourceDigest) || !bytes.Equal(quarantinedDigest, sourceDigest[:]) || len(subjectDigest) != len(wantSubject) || !bytes.Equal(subjectDigest, wantSubject[:]) || (reason != "identity_not_found" && reason != "identity_conflict" && reason != "definition_not_mapped" && reason != "invalid_source_row") {
		return reconciliationMapping{}, false, errors.New("reconciliation quarantine receipt mismatch")
	}
	return reconciliationMapping{quarantineReason: reason}, true, nil
}

func mapCouponStatus(value string) string {
	switch value {
	case "available":
		return "claimed"
	case "consumed":
		return "redeemed"
	default:
		return value
	}
}

// historicalCouponClaimNoMasked is the explicit legacy conversion for the
// target's mandatory masked claim-number column. The frozen source stream has
// no claim-number field, so history import stores the safe empty projection;
// it never fabricates a number from an ID or an identity value.
func historicalCouponClaimNoMasked(sourceCoupon) string { return "" }

func reconciliationDefinitionMap(ctx context.Context, db reconciliationQueryer, source, domain, kind string, id int64) (int64, bool, error) {
	var target int64
	err := db.QueryRow(ctx, `SELECT target_id FROM config_definition_import_source_maps WHERE source_system=$1 AND domain=$2 AND source_kind=$3 AND source_key=$4`, source, domain, kind, strconv.FormatInt(id, 10)).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return target, err == nil, err
}

func reconcileEntitlementTarget(ctx context.Context, db reconciliationQueryer, m manifest, row sourceEntitlement, mapping reconciliationMapping, sourceDigest [32]byte) error {
	if mapping.targetTable != "order_service_entitlements" {
		return errors.New("reconciliation entitlement target mismatch")
	}
	productID, found, err := reconciliationDefinitionMap(ctx, db, m.SourceSystem, "product", "service_period_products", row.ServiceProductID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("reconciliation entitlement definition mapping missing")
	}
	var matches bool
	err = db.QueryRow(ctx, `SELECT
		e.source_system=$1 AND e.source_key=$2 AND
		e.customer_id=$3 AND e.service_product_id=$4 AND e.product_name=$5 AND
		e.status=$6 AND e.start_at=$7 AND e.end_at=$8 AND e.remark=$9 AND
		e.alliance IS NOT DISTINCT FROM $10 AND e.source_digest=$11 AND
		e.created_at=$12 AND e.updated_at=$13
		FROM order_service_entitlements e WHERE e.id=$14`,
		m.SourceSystem, strconv.FormatInt(row.SourceID, 10), mapping.customerID, productID,
		row.ProductName, row.Status, row.StartAt, row.EndAt, row.Remark, row.Alliance,
		sourceDigest[:], row.CreatedAt, row.UpdatedAt, mapping.targetID).Scan(&matches)
	if err != nil || !matches {
		return errors.New("reconciliation entitlement target mismatch")
	}
	return nil
}

func reconcileCouponTarget(ctx context.Context, db reconciliationQueryer, m manifest, row sourceCoupon, mapping reconciliationMapping, sourceDigest [32]byte) error {
	if mapping.targetTable != "coupon_customer_claims" {
		return errors.New("reconciliation coupon target mismatch")
	}
	couponID, found, err := reconciliationDefinitionMap(ctx, db, m.SourceSystem, "coupon", "commerce_coupons", row.CouponID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("reconciliation coupon definition mapping missing")
	}
	var matches bool
	err = db.QueryRow(ctx, `SELECT
		c.source_system=$1 AND c.source_key=$2 AND
		c.customer_id=$3 AND c.coupon_id=$4 AND c.status=$5 AND
		c.claim_no_masked=$6 AND c.claimed_at=$7 AND
		c.valid_from IS NOT DISTINCT FROM $8 AND c.valid_until IS NOT DISTINCT FROM $9 AND
		c.redeemed_at IS NOT DISTINCT FROM $10 AND c.source_digest=$11 AND
		c.created_at=$12 AND c.updated_at=$13
		FROM coupon_customer_claims c WHERE c.id=$14`,
		m.SourceSystem, strconv.FormatInt(row.SourceID, 10), mapping.customerID, couponID,
		mapCouponStatus(row.Status), historicalCouponClaimNoMasked(row), row.ClaimedAt,
		row.ValidFrom, row.ValidUntil, row.RedeemedAt, sourceDigest[:], row.CreatedAt,
		row.UpdatedAt, mapping.targetID).Scan(&matches)
	if err != nil || !matches {
		return errors.New("reconciliation coupon target mismatch")
	}
	return nil
}

func reconcileMappedCustomer(ctx context.Context, tx pgx.Tx, m manifest, subject string, mapping reconciliationMapping) error {
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	resolved, err := resolveSidebarSubject(platformpostgres.BindTransaction(ctx, tx), oneID, m.UnionIDScope, subject)
	if err != nil || resolved.Status != identityport.ResolveFound || int64(resolved.CustomerID) != mapping.customerID {
		return errors.New("reconciliation mapped customer mismatch")
	}
	return nil
}

func reconcileQuarantineReason(ctx context.Context, tx pgx.Tx, m manifest, kind, subject string, definitionID int64) (string, error) {
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	resolved, err := resolveSidebarSubject(platformpostgres.BindTransaction(ctx, tx), oneID, m.UnionIDScope, subject)
	if err != nil {
		return "", err
	}
	if resolved.Status == identityport.ResolveConflict {
		return "identity_conflict", nil
	}
	if resolved.Status != identityport.ResolveFound {
		return "identity_not_found", nil
	}
	domain, sourceKind := "product", "service_period_products"
	if kind == "coupon_claim" {
		domain, sourceKind = "coupon", "commerce_coupons"
	}
	_, found, err := reconciliationDefinitionMap(ctx, tx, m.SourceSystem, domain, sourceKind, definitionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "definition_not_mapped", nil
	}
	return "", errors.New("reconciliation quarantine has become mappable")
}

func reconcile(ctx context.Context, pool *platformpostgres.Pool, m manifest) error {
	if isOpaqueSource(m) {
		return errors.New("opaque source must be bound to external proof")
	}
	tx, err := pool.Native().BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id, input, mapped, quarantined, missing int64
	var digest []byte
	err = tx.QueryRow(ctx, `SELECT id,input_count,manifest_digest FROM sidebar_history_migration_batches WHERE run_key=$1 AND status IN ('applied','reconciled') FOR UPDATE`, m.RunKey).Scan(&id, &input, &digest)
	if err != nil {
		return err
	}
	if input != int64(len(m.Entitlements)+len(m.Coupons)) || string(digest) != string(m.rawDigest[:]) {
		return errors.New("reconciliation manifest mismatch")
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sidebar_history_migration_source_map WHERE batch_id=$1`, id).Scan(&mapped); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sidebar_history_migration_quarantine WHERE batch_id=$1`, id).Scan(&quarantined); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sidebar_history_migration_source_map m WHERE m.batch_id=$1 AND ((m.target_table='order_service_entitlements' AND NOT EXISTS(SELECT 1 FROM order_service_entitlements e WHERE e.id=m.target_id AND e.customer_id=m.customer_id AND e.source_digest=m.source_digest)) OR (m.target_table='coupon_customer_claims' AND NOT EXISTS(SELECT 1 FROM coupon_customer_claims c WHERE c.id=m.target_id AND c.customer_id=m.customer_id AND c.source_digest=m.source_digest)))`, id).Scan(&missing); err != nil {
		return err
	}
	if mapped+quarantined != input || missing != 0 {
		return errors.New("reconciliation conservation mismatch")
	}
	for _, row := range m.Entitlements {
		raw, marshalErr := json.Marshal(row)
		if marshalErr != nil {
			return marshalErr
		}
		sourceDigest := sha256.Sum256(raw)
		mapping, quarantinedRow, outcomeErr := reconciliationSourceOutcome(ctx, tx, id, "service_period_entitlement", strconv.FormatInt(row.SourceID, 10), sourceDigest, row.UnionID)
		if outcomeErr != nil {
			return outcomeErr
		}
		if quarantinedRow {
			reason, reasonErr := reconcileQuarantineReason(ctx, tx, m, "service_period_entitlement", row.UnionID, row.ServiceProductID)
			if reasonErr != nil || reason == "" || mapping.quarantineReason != reason {
				return errors.New("reconciliation entitlement quarantine mismatch")
			}
			continue
		}
		if err = reconcileMappedCustomer(ctx, tx, m, row.UnionID, mapping); err != nil {
			return err
		}
		if err = reconcileEntitlementTarget(ctx, tx, m, row, mapping, sourceDigest); err != nil {
			return err
		}
	}
	for _, row := range m.Coupons {
		raw, marshalErr := json.Marshal(row)
		if marshalErr != nil {
			return marshalErr
		}
		sourceDigest := sha256.Sum256(raw)
		mapping, quarantinedRow, outcomeErr := reconciliationSourceOutcome(ctx, tx, id, "coupon_claim", strconv.FormatInt(row.SourceID, 10), sourceDigest, row.UnionID)
		if outcomeErr != nil {
			return outcomeErr
		}
		if quarantinedRow {
			reason, reasonErr := reconcileQuarantineReason(ctx, tx, m, "coupon_claim", row.UnionID, row.CouponID)
			if reasonErr != nil || reason == "" || mapping.quarantineReason != reason {
				return errors.New("reconciliation coupon quarantine mismatch")
			}
			continue
		}
		if err = reconcileMappedCustomer(ctx, tx, m, row.UnionID, mapping); err != nil {
			return err
		}
		if err = reconcileCouponTarget(ctx, tx, m, row, mapping, sourceDigest); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE sidebar_history_migration_batches SET status='reconciled',completed_at=clock_timestamp() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "reconcile", "run_key": m.RunKey, "matched": true, "input": input, "mapped": mapped, "quarantined": quarantined})
}
