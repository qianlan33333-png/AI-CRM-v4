package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// Explicit description maintenance keeps its separate command receipts. It
// cannot provision customers, publish directory state or manufacture tag history.
func (s CustomerSyncService) ingestDescriptionBackfillPage(ctx context.Context, run CustomerSyncRun, staff string, page wecomport.ExternalContactPage, at time.Time) error {
	if s.DescriptionIntents == nil || s.IdentityResolver == nil {
		return ErrSyncNotReady
	}
	return s.UOW.Within(ctx, func(txCtx context.Context) error {
		var linked, conflicts int64
		for _, contact := range page.Contacts {
			raw, _ := json.Marshal(contact)
			item := SyncItem{ExternalUserID: contact.ExternalUserID, ExternalUserIDDigest: sha256.Sum256([]byte(contact.ExternalUserID)), StaffIDDigest: sha256.Sum256([]byte(staff)), PayloadDigest: sha256.Sum256(raw), Outcome: "conflict", ErrorCode: "verified_identity_link_unresolved"}
			result, err := s.IdentityResolver.Resolve(txCtx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: run.CorpScope, Value: contact.ExternalUserID, Assurance: identitydomain.AssuranceVerified, Source: "wecom.description_backfill"})
			if err != nil {
				return err
			}
			if result.Status == identityport.ResolveFound && result.CustomerID > 0 && result.IdentityID > 0 {
				item.CustomerID, item.IdentityID, item.Outcome, item.ErrorCode = result.CustomerID, result.IdentityID, "already_linked", ""
				for _, follow := range contact.FollowInfo {
					if follow.EmployeeID == "" {
						return ErrSyncCAS
					}
					native, err := platformpostgres.RequireTransaction(txCtx)
					if err != nil {
						return err
					}
					projected := follow.DescriptionProjected && follow.Description != nil
					if _, err = native.Exec(txCtx, `INSERT INTO wecom_contact_description_source_observations(source_run_id,customer_id,corp_scope,employee_id,description_projected,observed_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(source_run_id,customer_id,corp_scope,employee_id) DO UPDATE SET description_projected=wecom_contact_description_source_observations.description_projected OR EXCLUDED.description_projected,observed_at=EXCLUDED.observed_at`, run.ID, result.CustomerID, run.CorpScope, follow.EmployeeID, projected, at); err != nil {
						return err
					}
					if !projected {
						continue
					}
					external := effectDigest(contact.ExternalUserID)
					observed := outboundport.ContactDescriptionObservedDigest(*follow.Description)
					_, err = s.DescriptionIntents.WriteContactDescriptionIntentWithin(txCtx, outboundport.ContactDescriptionIntentCommand{
						CustomerID: result.CustomerID, EmployeeUserID: follow.EmployeeID, SourceRunID: run.ID, Replan: true,
						SourceDigest: effectDigest("wecom.contact.description.source.v1", "run", strconv.FormatInt(run.ID, 10), run.CorpScope, follow.EmployeeID, string(external), string(observed)),
						TargetDigest: outboundport.ContactDescriptionTargetDigest(follow.EmployeeID, contact.ExternalUserID), ObservedDescriptionDigest: observed, PayloadDigest: outboundport.ContactDescriptionPayloadDigest(observed),
						ReceiptKey: outboundport.ContactDescriptionRelationshipKey(run.CorpScope, follow.EmployeeID, external), Operation: outboundport.ContactDescriptionOperationWrite,
					})
					if errors.Is(err, outboundport.ErrContactDescriptionReplanRequired) {
						item.ErrorCode = "contact_description_replan_required"
						continue
					}
					if err != nil {
						return err
					}
				}
			}
			inserted, err := s.Store.InsertItem(txCtx, run.ID, run.CorpScope, item)
			if err != nil {
				return err
			}
			if inserted {
				if item.Outcome == "conflict" {
					conflicts++
				} else {
					linked++
				}
			}
		}
		index, status := run.StaffIndex, SyncIngesting
		if page.NextCursor == "" {
			index++
			if index >= len(run.StaffIDs) {
				status = SyncReconciling
			}
		}
		if err := s.Store.AddCountsAndAdvance(txCtx, run.ID, run.Version, 0, linked, conflicts, 0, linked, index, page.NextCursor, status); err != nil {
			return err
		}
		_, err := s.Audit.Append(txCtx, platformaudit.Event{IdempotencyKey: idempotency.Key("wecom-description-page:" + strconv.FormatInt(run.ID, 10) + ":" + strconv.Itoa(run.StaffIndex) + ":" + cursorKey(run.ProviderCursor)), Action: "wecom.contact_description_backfill_page_committed", ActorType: "system", ResourceType: "wecom_customer_sync", ResourceID: strconv.FormatInt(run.ID, 10), Payload: json.RawMessage(`{"pii":false}`), OccurredAt: s.now()})
		return err
	})
}

func (s CustomerSyncService) completeDescriptionBackfill(ctx context.Context, run CustomerSyncRun) error {
	return s.UOW.Within(ctx, func(tx context.Context) error {
		if run.Discovered != run.AlreadyLinked+run.Conflict || run.Projected != run.AlreadyLinked {
			return ErrSyncCAS
		}
		if err := s.Store.Complete(tx, run.ID, run.Version, 0); err != nil {
			return err
		}
		_, err := s.Audit.Append(tx, platformaudit.Event{IdempotencyKey: idempotency.Key("wecom-description-complete:" + strconv.FormatInt(run.ID, 10)), Action: "wecom.contact_description_backfill_submitted", ActorType: "system", ResourceType: "wecom_customer_sync", ResourceID: strconv.FormatInt(run.ID, 10), Payload: json.RawMessage(`{"provider_result_separate":true}`), OccurredAt: s.now()})
		return err
	})
}
