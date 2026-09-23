package target

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrHistoryDrift = errors.New("Group Ops history import drift")

// HistoryRunner keeps the historical migration in the existing configuration
// migration command, while its writes remain wholly behind the Group Ops owner
// port. It creates no current plan, River work, Provider request, or effect.
type HistoryRunner struct {
	UOW      platformport.UnitOfWork
	GroupOps groupopsport.HistoricalImporter
}

func (r HistoryRunner) Preflight(ctx context.Context, snap source.HistorySnapshot, digest [sha256.Size]byte) error {
	if r.UOW == nil || r.GroupOps == nil || snap.Validate() != nil {
		return ErrInvalid
	}
	if _, err := historyRecords(snap); err != nil {
		return ErrInvalid
	}
	manifest, err := json.Marshal(snap.Manifest)
	if err != nil {
		return ErrInvalid
	}
	batch := groupopsport.HistoricalImportBatch{SourceSystem: snap.Manifest.SourceSystem, SourceRevision: snap.Manifest.SourceRevision, SnapshotDigest: digest, Manifest: manifest}
	return r.UOW.Within(ctx, func(tx context.Context) error {
		err := r.GroupOps.PreflightHistoricalImport(tx, batch)
		if errors.Is(err, groupopsport.ErrHistoryConflict) {
			return ErrHistoryDrift
		}
		return err
	})
}

func (r HistoryRunner) Apply(ctx context.Context, snap source.HistorySnapshot, digest [sha256.Size]byte) (groupopsport.HistoricalImportResult, error) {
	if r.UOW == nil || r.GroupOps == nil || snap.Validate() != nil {
		return groupopsport.HistoricalImportResult{}, ErrInvalid
	}
	manifest, err := json.Marshal(snap.Manifest)
	if err != nil {
		return groupopsport.HistoricalImportResult{}, ErrInvalid
	}
	records, err := historyRecords(snap)
	if err != nil {
		return groupopsport.HistoricalImportResult{}, ErrInvalid
	}
	batch := groupopsport.HistoricalImportBatch{SourceSystem: snap.Manifest.SourceSystem, SourceRevision: snap.Manifest.SourceRevision, SnapshotDigest: digest, Manifest: manifest}
	var out groupopsport.HistoricalImportResult
	err = r.UOW.Within(ctx, func(tx context.Context) error {
		out, err = r.GroupOps.ApplyHistoricalImport(tx, batch, records)
		if errors.Is(err, groupopsport.ErrHistoryConflict) {
			return ErrHistoryDrift
		}
		return err
	})
	return out, err
}

func (r HistoryRunner) Verify(ctx context.Context, snap source.HistorySnapshot, digest [sha256.Size]byte) (groupopsport.HistoricalImportResult, error) {
	if r.UOW == nil || r.GroupOps == nil || snap.Validate() != nil {
		return groupopsport.HistoricalImportResult{}, ErrInvalid
	}
	manifest, err := json.Marshal(snap.Manifest)
	if err != nil {
		return groupopsport.HistoricalImportResult{}, ErrInvalid
	}
	records, err := historyRecords(snap)
	if err != nil {
		return groupopsport.HistoricalImportResult{}, ErrInvalid
	}
	batch := groupopsport.HistoricalImportBatch{SourceSystem: snap.Manifest.SourceSystem, SourceRevision: snap.Manifest.SourceRevision, SnapshotDigest: digest, Manifest: manifest}
	var out groupopsport.HistoricalImportResult
	err = r.UOW.Within(ctx, func(tx context.Context) error {
		out, err = r.GroupOps.VerifyHistoricalImport(tx, batch, records)
		if errors.Is(err, groupopsport.ErrHistoryConflict) {
			return ErrHistoryDrift
		}
		return err
	})
	return out, err
}

func historyRecords(snap source.HistorySnapshot) ([]groupopsport.HistoricalImportRecord, error) {
	records := make([]groupopsport.HistoricalImportRecord, 0, len(snap.Plans)+len(snap.DirectoryChats)+len(snap.DirectorySnapshots)+len(snap.Groups)+len(snap.Nodes))
	importedPlans := make(map[int64]bool, len(snap.Plans))
	for _, x := range snap.Plans {
		r := historyRecord("plans", fmt.Sprint(x.ID), x)
		if x.ID < 1 || x.Name == "" || historyTextTooLong(x.Name) || x.Name != strings.TrimSpace(x.Name) || historyTextTooLong(x.PlanCode) || x.PlanCode != strings.TrimSpace(x.PlanCode) || x.PlanType == "" || x.PlanType != strings.TrimSpace(x.PlanType) || x.Status == "" || x.Status != strings.TrimSpace(x.Status) || !historyReferenceValid(x.OwnerReference) || !historyReferenceValid(x.CreatedByReference) || !historyReferenceValid(x.UpdatedByReference) || x.CreatedAt.IsZero() || x.UpdatedAt.Before(x.CreatedAt) {
			r.QuarantineReason = "invalid_plan"
		} else {
			r.Plan = &groupopsport.HistoricalPlan{PlanID: x.ID, Name: x.Name, Status: groupopsport.PlanArchived, Revision: 1, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt, SourcePlanID: x.ID, SourceCode: x.PlanCode, PlanType: x.PlanType, OriginalStatus: x.Status, ArchivedAt: x.ArchivedAt, SourceCreatedByReference: x.CreatedByReference, SourceUpdatedByReference: x.UpdatedByReference, SourceOwnerReference: x.OwnerReference}
			importedPlans[x.ID] = true
		}
		records = append(records, r)
	}
	for _, x := range snap.DirectoryChats {
		r := historyRecord("directory_group_chats", "chat:"+x.ChatReference, x)
		if x.ChatReference == "" || historyTextTooLong(x.ChatReference) || x.ChatReference != strings.TrimSpace(x.ChatReference) || x.DisplayName != strings.TrimSpace(x.DisplayName) || x.Status == "" || x.Status != strings.TrimSpace(x.Status) || !historyReferenceValid(x.OwnerReference) || x.MemberCount < 0 || x.RecordedAt.IsZero() {
			r.QuarantineReason = "invalid_directory"
		} else {
			r.Directory = &groupopsport.HistoricalDirectory{SourceKind: "group_chats", ChatReference: x.ChatReference, DisplayName: optionalText(x.DisplayName), OwnerName: nil, MemberCount: &x.MemberCount, OriginalStatus: x.Status, RecordedAt: x.RecordedAt, SourceOwnerReference: x.OwnerReference}
		}
		records = append(records, r)
	}
	for _, x := range snap.DirectorySnapshots {
		r := historyRecord("directory_snapshots", "chat:"+x.ChatReference, x)
		if x.ChatReference == "" || historyTextTooLong(x.ChatReference) || x.ChatReference != strings.TrimSpace(x.ChatReference) || x.DisplayName != strings.TrimSpace(x.DisplayName) || x.Status == "" || x.Status != strings.TrimSpace(x.Status) || !historyReferenceValid(x.OwnerReference) || x.InternalMemberCount < 0 || x.ExternalMemberCount < 0 || x.RecordedAt.IsZero() {
			r.QuarantineReason = "invalid_directory"
		} else {
			r.Directory = &groupopsport.HistoricalDirectory{SourceKind: "wecom_group_chat_snapshots", ChatReference: x.ChatReference, DisplayName: optionalText(x.DisplayName), OwnerName: optionalText(x.OwnerName), InternalMemberCount: &x.InternalMemberCount, ExternalMemberCount: &x.ExternalMemberCount, OriginalStatus: x.Status, RecordedAt: x.RecordedAt, SourceOwnerReference: x.OwnerReference}
		}
		records = append(records, r)
	}
	for _, x := range snap.Groups {
		r := historyRecord("groups", fmt.Sprint(x.ID), x)
		if !importedPlans[x.PlanID] {
			r.QuarantineReason = "missing_plan"
		} else if x.ID < 1 || x.ChatReference == "" || historyTextTooLong(x.ChatReference) || x.ChatReference != strings.TrimSpace(x.ChatReference) || x.DisplayName == "" || historyTextTooLong(x.DisplayName) || x.DisplayName != strings.TrimSpace(x.DisplayName) || x.Status == "" || x.Status != strings.TrimSpace(x.Status) || !historyReferenceValid(x.OwnerReference) || x.InternalMemberCount < 0 || x.ExternalMemberCount < 0 || x.CreatedAt.IsZero() {
			r.QuarantineReason = "invalid_group"
		} else {
			r.Group = &groupopsport.HistoricalGroup{SourceGroupID: x.ID, SourcePlanID: x.PlanID, PlanID: x.PlanID, ChatReference: x.ChatReference, DisplayName: x.DisplayName, InternalMemberCount: x.InternalMemberCount, ExternalMemberCount: x.ExternalMemberCount, OriginalStatus: x.Status, CreatedAt: x.CreatedAt, RemovedAt: x.RemovedAt, SourceOwnerReference: x.OwnerReference}
		}
		records = append(records, r)
	}
	for _, x := range snap.Nodes {
		r := historyRecord("nodes", fmt.Sprint(x.ID), x)
		if !importedPlans[x.PlanID] {
			r.QuarantineReason = "missing_plan"
		} else if x.ID < 1 || x.DayIndex < 0 || x.TriggerTime != strings.TrimSpace(x.TriggerTime) || x.SortOrder < 0 || x.Status == "" || x.Status != strings.TrimSpace(x.Status) || !json.Valid(x.ContentPackage) || !json.Valid(x.Attachments) || x.CreatedAt.IsZero() || x.UpdatedAt.Before(x.CreatedAt) {
			r.QuarantineReason = "invalid_node"
		} else {
			content, err := json.Marshal(struct {
				ContentPackage json.RawMessage `json:"source_content_package"`
				Attachments    json.RawMessage `json:"source_attachments"`
				ActionTitle    string          `json:"source_action_title"`
				TextContent    string          `json:"source_text_content"`
				TriggerTime    string          `json:"source_trigger_time_label"`
			}{x.ContentPackage, x.Attachments, x.ActionTitle, x.TextContent, x.TriggerTime})
			if err != nil {
				return nil, err
			}
			r.Node = &groupopsport.HistoricalNode{SourceNodeID: x.ID, SourcePlanID: x.PlanID, PlanID: x.PlanID, DayIndex: x.DayIndex, TriggerTime: x.TriggerTime, SortOrder: x.SortOrder, OriginalStatus: x.Status, ActionTitle: x.ActionTitle, TextContent: x.TextContent, Attachments: append(json.RawMessage(nil), x.Attachments...), ContentPackage: content, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool {
		order := map[string]int{"plans": 0, "directory_group_chats": 1, "directory_snapshots": 2, "groups": 3, "nodes": 4}
		if order[records[i].SourceKind] != order[records[j].SourceKind] {
			return order[records[i].SourceKind] < order[records[j].SourceKind]
		}
		return records[i].SourceKey < records[j].SourceKey
	})
	return records, nil
}

func historyRecord(kind, key string, value any) groupopsport.HistoricalImportRecord {
	raw, _ := json.Marshal(value)
	return groupopsport.HistoricalImportRecord{SourceKind: kind, SourceKey: key, SourceDigest: sha256.Sum256(raw)}
}

func optionalText(value string) *string { return &value }

func historyTextTooLong(value string) bool { return utf8.RuneCountInString(value) > 128 }

func historyReferenceValid(value *string) bool { return value == nil || !historyTextTooLong(*value) }
