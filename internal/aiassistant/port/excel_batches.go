package port

import (
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

// ExcelBatchRow is the frozen business input from the controlled Excel
// component. UnionID and sender stay opaque until the existing execution-time
// Identity Port resolution; this type deliberately has no customer ID.
type ExcelBatchRow struct {
	UnionID      string
	SenderUserID string
	Text         string
	Card         ExcelCard
	Segment      string
}

func (r ExcelBatchRow) Valid() bool {
	return validLegacyReferencePart(r.UnionID, 256) &&
		validLegacyReferencePart(r.SenderUserID, 256) &&
		strings.TrimSpace(r.Text) != "" && len(r.Text) <= 8000 &&
		r.Card.Valid() && (r.Segment == "" || r.Segment == "A" || r.Segment == "B" || r.Segment == "C" || r.Segment == "D")
}

type ExcelBatchCommand struct {
	Actor          Actor
	IdempotencyKey string
	BatchKey       string
	StrategyKey    string
	Name           string
	Scope          string
	FileDigest     effectport.Digest
	Rows           []ExcelBatchRow
	OccurredAt     time.Time
}

func (c ExcelBatchCommand) Valid() bool {
	if !c.Actor.Valid() || len(c.IdempotencyKey) < 8 || len(c.IdempotencyKey) > 200 || !validLegacyReferencePart(c.BatchKey, 128) || !validLegacyReferencePart(c.StrategyKey, 120) ||
		strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 || !strings.HasPrefix(c.Scope, "wechat-open-platform:") ||
		!effectport.ValidDigest(c.FileDigest) || len(c.Rows) == 0 || len(c.Rows) > MaxRecipients || c.OccurredAt.IsZero() {
		return false
	}
	seen := make(map[string]struct{}, len(c.Rows))
	for _, row := range c.Rows {
		if !row.Valid() {
			return false
		}
		if _, duplicate := seen[row.UnionID]; duplicate {
			return false
		}
		seen[row.UnionID] = struct{}{}
	}
	return true
}

type ReplaceExcelBatchCommand struct {
	Actor           Actor
	PlanID          PlanID
	ExpectedVersion int64
	IdempotencyKey  string
	Scope           string
	FileDigest      effectport.Digest
	Rows            []ExcelBatchRow
	OccurredAt      time.Time
}

func (c ReplaceExcelBatchCommand) Valid() bool {
	return c.Actor.Valid() && c.PlanID > 0 && c.ExpectedVersion > 0 && len(c.IdempotencyKey) >= 8 && len(c.IdempotencyKey) <= 200 && strings.HasPrefix(c.Scope, "wechat-open-platform:") &&
		effectport.ValidDigest(c.FileDigest) && len(c.Rows) > 0 && len(c.Rows) <= MaxRecipients && !c.OccurredAt.IsZero()
}

type UpdateExcelRowCommand struct {
	Actor           Actor
	PlanID          PlanID
	RecipientID     RecipientID
	ExpectedVersion int64
	IdempotencyKey  string
	Text            string
	Path            string
	Title           string
	Segment         string
	Excluded        bool
}

func (c UpdateExcelRowCommand) Valid() bool {
	return c.Actor.Valid() && c.PlanID > 0 && c.RecipientID > 0 && c.ExpectedVersion > 0 && len(c.IdempotencyKey) >= 8 && len(c.IdempotencyKey) <= 200 &&
		strings.TrimSpace(c.Text) != "" && len(c.Text) <= 8000 && validLegacyReferencePart(c.Path, 1024) && strings.HasPrefix(c.Path, "pages/") && !strings.Contains(c.Path, "://") && !strings.HasPrefix(c.Path, "//") && len(c.Title) <= 512 &&
		(c.Segment == "" || c.Segment == "A" || c.Segment == "B" || c.Segment == "C" || c.Segment == "D")
}

type ExcelBatchSummary struct {
	TotalRows      int `json:"total_rows"`
	ExcludedRows   int `json:"excluded_rows"`
	EmptyTitleRows int `json:"empty_title_rows"`
	ExpectedTasks  int `json:"expected_tasks"`
}

type ExcelBatchMeta struct {
	PlanID       PlanID
	BatchKey     string
	StrategyKey  string
	SourceOrigin string
	FileDigest   effectport.Digest
	CoverDigest  effectport.Digest
	CoverImageID int64
	Revision     int
	CreatedAt    time.Time
}

func (m ExcelBatchMeta) Linked() bool { return m.PlanID > 0 && m.StrategyKey != "" }

// ExcelBatchOverview is the batch-owned part of the bounded Operation Cycle
// list projection. A missing entry is a known absence of a batch; an
// unavailable bulk read is represented by the caller, never guessed here.
type ExcelBatchOverview struct {
	Meta        ExcelBatchMeta
	State       PlanState
	PlanVersion int64
	SourceKind  string
	Summary     ExcelBatchSummary
}

type ExcelBatchVersion struct {
	PlanID          PlanID            `json:"plan_id"`
	ContentRevision int               `json:"content_version"`
	FileDigest      effectport.Digest `json:"file_digest"`
	CoverDigest     effectport.Digest `json:"cover_digest"`
	CoverImageID    int64             `json:"cover_image_id"`
	CreatedBy       int64             `json:"created_by"`
	CreatedAt       time.Time         `json:"created_at"`
}

type ExcelBatchRecipientPage struct {
	Items      []Recipient
	NextCursor string
}

type ExcelBatchRowAttributes struct {
	ContentRevision int
	Segment         string
	Excluded        bool
}
