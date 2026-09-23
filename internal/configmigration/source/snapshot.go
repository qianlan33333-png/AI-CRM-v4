package source

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = "aicrm-v2-config-definitions-v1"

var (
	ErrInvalidSnapshot = errors.New("invalid configuration definition snapshot")
	validOpaque        = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	validAgentCode     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,119}$`)
	validRevision      = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type Manifest struct {
	Scope          string            `json:"scope,omitempty"`
	SchemaVersion  string            `json:"schema_version"`
	SourceSystem   string            `json:"source_system"`
	SourceRevision string            `json:"source_revision"`
	SnapshotAt     time.Time         `json:"snapshot_at"`
	Counts         map[string]int    `json:"counts"`
	Digests        map[string]string `json:"digests"`
}

type Snapshot struct {
	Manifest       Manifest        `json:"manifest"`
	Products       []Product       `json:"products"`
	ServicePeriods []ServicePeriod `json:"service_periods"`
	Coupons        []Coupon        `json:"coupons"`
	CouponBindings []CouponBinding `json:"coupon_bindings"`
	GroupPlans     []GroupPlan     `json:"group_plans"`
	GroupNodes     []GroupNode     `json:"group_nodes"`
	GroupAssets    []GroupAsset    `json:"group_assets"`
	Agents         []Agent         `json:"agents"`
}

type Product struct {
	ID             int64     `json:"id"`
	ProductCode    string    `json:"product_code"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	PriceMinor     int64     `json:"price_minor"`
	Currency       string    `json:"currency"`
	Status         string    `json:"status"`
	Enabled        bool      `json:"enabled"`
	BuyButtonText  string    `json:"buy_button_text"`
	RequireMobile  bool      `json:"require_mobile"`
	LeadQRTitle    string    `json:"lead_qr_title"`
	LeadQRSubtitle string    `json:"lead_qr_subtitle"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ServicePeriod struct {
	ID             int64     `json:"id"`
	TradeProductID int64     `json:"trade_product_id"`
	DurationDays   int32     `json:"duration_days"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Coupon struct {
	PublicSlug           *string    `json:"public_slug,omitempty"`
	IssuedCount          *int64     `json:"issued_count,omitempty"`
	ID                   int64      `json:"id"`
	Name                 string     `json:"name"`
	DiscountAmountTotal  int64      `json:"discount_amount_total"`
	Currency             string     `json:"currency"`
	Status               string     `json:"status"`
	TotalIssueLimit      int64      `json:"total_issue_limit"`
	PerUserIssueLimit    int64      `json:"per_user_issue_limit"`
	ClaimStartsAt        time.Time  `json:"claim_starts_at"`
	ClaimEndsAt          time.Time  `json:"claim_ends_at"`
	ValidityMode         string     `json:"validity_mode"`
	UseStartsAt          *time.Time `json:"use_starts_at"`
	UseEndsAt            *time.Time `json:"use_ends_at"`
	RelativeValidityDays *int32     `json:"relative_validity_days"`
	Instructions         string     `json:"instructions"`
	SourceCreatedBy      string     `json:"source_created_by"`
	SourceUpdatedBy      string     `json:"source_updated_by"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type CouponBinding struct {
	ID             int64     `json:"id"`
	CouponID       int64     `json:"coupon_id"`
	TradeProductID int64     `json:"trade_product_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type GroupPlan struct {
	ID              int64     `json:"id"`
	PlanCode        string    `json:"plan_code"`
	Name            string    `json:"name"`
	PlanType        string    `json:"plan_type"`
	Status          string    `json:"status"`
	Description     string    `json:"description"`
	SourceCreatedBy string    `json:"source_created_by"`
	SourceUpdatedBy string    `json:"source_updated_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type GroupNode struct {
	ID               int64     `json:"id"`
	PlanID           int64     `json:"plan_id"`
	DayIndex         int32     `json:"day_index"`
	TriggerTimeLabel string    `json:"trigger_time_label"`
	ActionTitle      string    `json:"action_title"`
	TextContent      string    `json:"text_content"`
	SortOrder        int32     `json:"sort_order"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type GroupAsset struct {
	ID        int64     `json:"id"`
	PlanID    int64     `json:"plan_id"`
	ChatID    string    `json:"chat_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Agent struct {
	ID                  int64      `json:"id"`
	AgentCode           string     `json:"agent_code"`
	AgentName           string     `json:"agent_name"`
	Status              string     `json:"status"`
	AutomationType      string     `json:"automation_type"`
	DraftRolePrompt     string     `json:"draft_role_prompt"`
	DraftTaskPrompt     string     `json:"draft_task_prompt"`
	PublishedRolePrompt string     `json:"published_role_prompt"`
	PublishedTaskPrompt string     `json:"published_task_prompt"`
	DraftVersion        int64      `json:"draft_version"`
	PublishedVersion    int64      `json:"published_version"`
	FixedContentText    string     `json:"fixed_content_text"`
	NeedHumanReview     bool       `json:"need_human_review"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	ArchivedAt          *time.Time `json:"archived_at"`
}

var tableOrder = []string{"products", "service_periods", "coupons", "coupon_bindings", "group_plans", "group_nodes", "group_assets", "agents"}

func Parse(plain []byte) (Snapshot, [sha256.Size]byte, error) {
	var snapshot Snapshot
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return Snapshot{}, [sha256.Size]byte{}, ErrInvalidSnapshot
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, [sha256.Size]byte{}, err
	}
	// Return the digest of the normalized representation.  The parser accepts
	// harmless whitespace/key-order differences while still rejecting unknown
	// fields and trailing JSON above; callers therefore get one stable digest
	// for semantically identical snapshots.
	_, digest, err := snapshot.Canonical()
	if err != nil {
		return Snapshot{}, [sha256.Size]byte{}, ErrInvalidSnapshot
	}
	return snapshot, digest, nil
}

func (snapshot Snapshot) Canonical() ([]byte, [sha256.Size]byte, error) {
	normalizeSnapshot(&snapshot)
	if err := snapshot.Validate(); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	return raw, sha256.Sum256(raw), nil
}

// CanonicalDigest returns the SHA-256 digest of the normalized snapshot JSON.
// It is deliberately computed from the same representation used by
// SealToFile, so a digest cannot silently refer to a different JSON encoding.
func (snapshot Snapshot) CanonicalDigest() ([sha256.Size]byte, error) {
	_, digest, err := snapshot.Canonical()
	return digest, err
}

// CanonicalDigestHex is the transport-friendly form of CanonicalDigest.
func (snapshot Snapshot) CanonicalDigestHex() (string, error) {
	digest, err := snapshot.CanonicalDigest()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest[:]), nil
}

// ValidateSnapshot is a named function for callers that do not need a method
// value.  Both forms intentionally share exactly one validator.
func ValidateSnapshot(snapshot Snapshot) error { return snapshot.Validate() }

func (snapshot Snapshot) Validate() error {
	m := snapshot.Manifest
	if m.Scope != "" && m.Scope != "commerce-only" {
		return ErrInvalidSnapshot
	}
	if m.Scope == "commerce-only" {
		if len(snapshot.GroupPlans)+len(snapshot.GroupNodes)+len(snapshot.GroupAssets)+len(snapshot.Agents) != 0 {
			return ErrInvalidSnapshot
		}
		for _, c := range snapshot.Coupons {
			if c.PublicSlug == nil || c.IssuedCount == nil || *c.IssuedCount < 0 {
				return ErrInvalidSnapshot
			}
		}
	} else {
		for _, c := range snapshot.Coupons {
			if c.PublicSlug != nil || c.IssuedCount != nil {
				return ErrInvalidSnapshot
			}
		}
	}
	if m.SchemaVersion != SchemaVersion || !validText(m.SourceSystem, 160) || !validRevision.MatchString(m.SourceRevision) || m.SnapshotAt.IsZero() || len(m.Counts) != len(tableOrder) || len(m.Digests) != len(tableOrder) {
		return ErrInvalidSnapshot
	}
	rows := snapshotRows(snapshot)
	for _, name := range tableOrder {
		raw, err := json.Marshal(rows[name])
		digest := sha256.Sum256(raw)
		if err != nil || m.Counts[name] != sliceLength(rows[name]) || m.Digests[name] != hex.EncodeToString(digest[:]) {
			return ErrInvalidSnapshot
		}
	}
	productIDs, productCodes := map[int64]bool{}, map[string]bool{}
	for _, row := range snapshot.Products {
		if row.ID < 1 || productIDs[row.ID] || productCodes[row.ProductCode] || !validText(row.ProductCode, 200) || !validText(row.Name, 200) || len(row.Description) > 10000 || row.PriceMinor < 1 || row.Currency != "CNY" || (row.Status != "active" && row.Status != "disabled") || row.Enabled != (row.Status == "active") || invalidTimes(row.CreatedAt, row.UpdatedAt) {
			return ErrInvalidSnapshot
		}
		productIDs[row.ID], productCodes[row.ProductCode] = true, true
	}
	serviceByProduct := map[int64]bool{}
	for _, row := range snapshot.ServicePeriods {
		if row.ID < 1 || !productIDs[row.TradeProductID] || serviceByProduct[row.TradeProductID] || row.DurationDays < 1 || invalidTimes(row.CreatedAt, row.UpdatedAt) {
			return ErrInvalidSnapshot
		}
		serviceByProduct[row.TradeProductID] = true
	}
	couponIDs := map[int64]bool{}
	for _, row := range snapshot.Coupons {
		fixed := row.ValidityMode == "fixed_range" && row.UseStartsAt != nil && row.UseEndsAt != nil && row.UseEndsAt.After(*row.UseStartsAt) && row.RelativeValidityDays == nil
		relative := row.ValidityMode == "relative_days" && row.UseStartsAt == nil && row.UseEndsAt == nil && row.RelativeValidityDays != nil && *row.RelativeValidityDays > 0
		if row.ID < 1 || couponIDs[row.ID] || !validText(row.Name, 45) || row.DiscountAmountTotal < 1 || row.Currency != "CNY" || (row.Status != "published" && row.Status != "archived") || row.TotalIssueLimit < 1 || row.PerUserIssueLimit < 1 || row.PerUserIssueLimit > row.TotalIssueLimit || row.ClaimStartsAt.IsZero() || !row.ClaimEndsAt.After(row.ClaimStartsAt) || (!fixed && !relative) || len(row.Instructions) > 200 || !validOptionalText(row.SourceCreatedBy, 160) || !validOptionalText(row.SourceUpdatedBy, 160) || invalidTimes(row.CreatedAt, row.UpdatedAt) {
			return ErrInvalidSnapshot
		}
		couponIDs[row.ID] = true
	}
	bindingIDs := map[int64]bool{}
	for _, row := range snapshot.CouponBindings {
		if row.ID < 1 || bindingIDs[row.ID] || !couponIDs[row.CouponID] || !productIDs[row.TradeProductID] || row.CreatedAt.IsZero() {
			return ErrInvalidSnapshot
		}
		bindingIDs[row.ID] = true
	}
	planIDs, planCodes := map[int64]bool{}, map[string]bool{}
	for _, row := range snapshot.GroupPlans {
		if row.ID < 1 || planIDs[row.ID] || planCodes[row.PlanCode] || !validText(row.PlanCode, 120) || !validText(row.Name, 128) || (row.Status != "active" && row.Status != "disabled") || (row.PlanType != "standard" && row.PlanType != "webhook") || !validOptionalText(row.SourceCreatedBy, 160) || !validOptionalText(row.SourceUpdatedBy, 160) || invalidTimes(row.CreatedAt, row.UpdatedAt) {
			return ErrInvalidSnapshot
		}
		planIDs[row.ID], planCodes[row.PlanCode] = true, true
	}
	nodeIDs := map[int64]bool{}
	for _, row := range snapshot.GroupNodes {
		if row.ID < 1 || nodeIDs[row.ID] || !planIDs[row.PlanID] || row.DayIndex < 1 || !validText(row.TriggerTimeLabel, 64) || len(row.ActionTitle) > 256 || !validText(row.TextContent, 1000) || row.SortOrder < 0 || invalidTimes(row.CreatedAt, row.UpdatedAt) {
			return ErrInvalidSnapshot
		}
		nodeIDs[row.ID] = true
	}
	assetIDs := map[int64]bool{}
	for _, row := range snapshot.GroupAssets {
		if row.ID < 1 || assetIDs[row.ID] || !planIDs[row.PlanID] || !validOpaque.MatchString(row.ChatID) || len([]rune(row.ChatID)) > 32 || row.CreatedAt.IsZero() {
			return ErrInvalidSnapshot
		}
		assetIDs[row.ID] = true
	}
	agentIDs, agentCodes := map[int64]bool{}, map[string]bool{}
	for _, row := range snapshot.Agents {
		if row.ID < 1 || agentIDs[row.ID] || agentCodes[row.AgentCode] || !validAgentCode.MatchString(row.AgentCode) || !validText(row.AgentName, 100) || (row.Status != "active" && row.Status != "archived") || (row.Status == "archived") != (row.ArchivedAt != nil) || (row.AutomationType != "agent" && row.AutomationType != "fixed_script") || row.DraftVersion < 1 || row.PublishedVersion < 1 || row.PublishedVersion > row.DraftVersion || row.NeedHumanReview || invalidTimes(row.CreatedAt, row.UpdatedAt) || (row.AutomationType == "agent" && row.FixedContentText != "") || len([]rune(row.FixedContentText)) > 4000 {
			return ErrInvalidSnapshot
		}
		agentIDs[row.ID], agentCodes[row.AgentCode] = true, true
	}
	return nil
}

func PopulateManifest(snapshot *Snapshot, sourceSystem, sourceRevision string, at time.Time) error {
	if snapshot == nil {
		return ErrInvalidSnapshot
	}
	normalizeSnapshot(snapshot)
	snapshot.Manifest = Manifest{Scope: snapshot.Manifest.Scope, SchemaVersion: SchemaVersion, SourceSystem: sourceSystem, SourceRevision: sourceRevision, SnapshotAt: at.UTC(), Counts: map[string]int{}, Digests: map[string]string{}}
	for name, rows := range snapshotRows(*snapshot) {
		raw, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		snapshot.Manifest.Counts[name] = sliceLength(rows)
		snapshot.Manifest.Digests[name] = hex.EncodeToString(digest[:])
	}
	return snapshot.Validate()
}

func (snapshot Snapshot) Summary() map[string]int {
	keys := append([]string(nil), tableOrder...)
	sort.Strings(keys)
	out := make(map[string]int, len(keys))
	for _, key := range keys {
		out[key] = snapshot.Manifest.Counts[key]
	}
	return out
}

func ValidateExpectedBaseline(snapshot Snapshot) error {
	expected := map[string]int{"products": 31, "service_periods": 2, "coupons": 15, "coupon_bindings": 15, "group_plans": 12, "group_nodes": 3, "group_assets": 14, "agents": 10}
	for name, count := range expected {
		if snapshot.Manifest.Counts[name] != count {
			return fmt.Errorf("%w: %s count %d, expected %d", ErrInvalidSnapshot, name, snapshot.Manifest.Counts[name], count)
		}
	}
	return nil
}

func snapshotRows(snapshot Snapshot) map[string]any {
	return map[string]any{"products": snapshot.Products, "service_periods": snapshot.ServicePeriods, "coupons": snapshot.Coupons, "coupon_bindings": snapshot.CouponBindings, "group_plans": snapshot.GroupPlans, "group_nodes": snapshot.GroupNodes, "group_assets": snapshot.GroupAssets, "agents": snapshot.Agents}
}

func validText(value string, max int) bool {
	return value != "" && strings.TrimSpace(value) == value && len([]rune(value)) <= max
}

func validOptionalText(value string, max int) bool {
	return value == "" || strings.TrimSpace(value) == value && len([]rune(value)) <= max
}

func invalidTimes(created, updated time.Time) bool {
	return created.IsZero() || updated.IsZero() || updated.Before(created)
}

func sliceLength(value any) int {
	raw, _ := json.Marshal(value)
	var rows []json.RawMessage
	_ = json.Unmarshal(raw, &rows)
	return len(rows)
}

// normalizeSnapshot makes the digest independent of nil-vs-empty slices and
// of callers constructing rows in a different order.  Reader queries already
// return these orders; doing it here also protects hand-built fixtures and
// makes canonicalization a property of the package rather than of SQL.
func normalizeSnapshot(snapshot *Snapshot) {
	if snapshot == nil {
		return
	}
	if snapshot.Products == nil {
		snapshot.Products = []Product{}
	}
	if snapshot.ServicePeriods == nil {
		snapshot.ServicePeriods = []ServicePeriod{}
	}
	if snapshot.Coupons == nil {
		snapshot.Coupons = []Coupon{}
	}
	if snapshot.CouponBindings == nil {
		snapshot.CouponBindings = []CouponBinding{}
	}
	if snapshot.GroupPlans == nil {
		snapshot.GroupPlans = []GroupPlan{}
	}
	if snapshot.GroupNodes == nil {
		snapshot.GroupNodes = []GroupNode{}
	}
	if snapshot.GroupAssets == nil {
		snapshot.GroupAssets = []GroupAsset{}
	}
	if snapshot.Agents == nil {
		snapshot.Agents = []Agent{}
	}
	sort.SliceStable(snapshot.Products, func(i, j int) bool { return snapshot.Products[i].ID < snapshot.Products[j].ID })
	sort.SliceStable(snapshot.ServicePeriods, func(i, j int) bool { return snapshot.ServicePeriods[i].ID < snapshot.ServicePeriods[j].ID })
	sort.SliceStable(snapshot.Coupons, func(i, j int) bool { return snapshot.Coupons[i].ID < snapshot.Coupons[j].ID })
	sort.SliceStable(snapshot.CouponBindings, func(i, j int) bool { return snapshot.CouponBindings[i].ID < snapshot.CouponBindings[j].ID })
	sort.SliceStable(snapshot.GroupPlans, func(i, j int) bool { return snapshot.GroupPlans[i].ID < snapshot.GroupPlans[j].ID })
	sort.SliceStable(snapshot.GroupNodes, func(i, j int) bool {
		a, b := snapshot.GroupNodes[i], snapshot.GroupNodes[j]
		if a.PlanID != b.PlanID {
			return a.PlanID < b.PlanID
		}
		if a.DayIndex != b.DayIndex {
			return a.DayIndex < b.DayIndex
		}
		if a.TriggerTimeLabel != b.TriggerTimeLabel {
			return a.TriggerTimeLabel < b.TriggerTimeLabel
		}
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		return a.ID < b.ID
	})
	sort.SliceStable(snapshot.GroupAssets, func(i, j int) bool {
		if snapshot.GroupAssets[i].PlanID != snapshot.GroupAssets[j].PlanID {
			return snapshot.GroupAssets[i].PlanID < snapshot.GroupAssets[j].PlanID
		}
		return snapshot.GroupAssets[i].ID < snapshot.GroupAssets[j].ID
	})
	sort.SliceStable(snapshot.Agents, func(i, j int) bool { return snapshot.Agents[i].ID < snapshot.Agents[j].ID })
}
