package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strconv"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type stagedContact struct {
	CustomerID     customerdomain.CustomerID
	IdentityID     int64
	Contact        wecomport.ExternalContact
	ObservedAt     time.Time
	FollowObserved map[string]time.Time
}

type directoryPublicationStore interface {
	StageContact(context.Context, int64, identityport.ProvisionResult, wecomport.ExternalContact, time.Time) error
	StagedContacts(context.Context, int64) ([]stagedContact, error)
	BeginPublication(context.Context, CustomerSyncRun) error
	EndPublication(context.Context, CustomerSyncRun, time.Time) error
}

func (PostgreSQLCustomerSyncStore) StageContact(ctx context.Context, runID int64, provision identityport.ProvisionResult, contact wecomport.ExternalContact, at time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(contact)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wecom_customer_profile_staging(run_id,customer_id,identity_id,contact,observed_at) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(run_id,customer_id) DO UPDATE SET contact=EXCLUDED.contact,identity_id=EXCLUDED.identity_id,observed_at=EXCLUDED.observed_at
 WHERE wecom_customer_profile_staging.observed_at<EXCLUDED.observed_at`, runID, provision.CustomerID, provision.IdentityID, raw, at.UTC())
	if err != nil {
		return err
	}
	for _, f := range contact.FollowInfo {
		if f.EmployeeID == "" {
			return ErrSyncCAS
		}
		raw, err = json.Marshal(f)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO wecom_customer_follow_staging(run_id,customer_id,employee_id,follow_info,observed_at) VALUES($1,$2,$3,$4,$5)
   ON CONFLICT(run_id,customer_id,employee_id) DO UPDATE SET follow_info=EXCLUDED.follow_info,observed_at=EXCLUDED.observed_at
   WHERE wecom_customer_follow_staging.observed_at<EXCLUDED.observed_at`, runID, provision.CustomerID, f.EmployeeID, raw, at.UTC())
		if err != nil {
			return err
		}
	}
	return nil
}

func (PostgreSQLCustomerSyncStore) StagedContacts(ctx context.Context, runID int64) ([]stagedContact, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,identity_id,contact,observed_at FROM wecom_customer_profile_staging WHERE run_id=$1 ORDER BY customer_id`, runID)
	if err != nil {
		return nil, err
	}
	out := []stagedContact{}
	for rows.Next() {
		var s stagedContact
		var raw []byte
		if err = rows.Scan(&s.CustomerID, &s.IdentityID, &raw, &s.ObservedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &s.Contact); err != nil {
			rows.Close()
			return nil, err
		}
		s.Contact.FollowInfo = nil
		s.FollowObserved = map[string]time.Time{}
		out = append(out, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	indices := map[customerdomain.CustomerID]int{}
	for i, s := range out {
		indices[s.CustomerID] = i
	}
	rows, err = tx.Query(ctx, `SELECT customer_id,follow_info,observed_at FROM wecom_customer_follow_staging WHERE run_id=$1 ORDER BY customer_id,employee_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id customerdomain.CustomerID
		var raw []byte
		var f wecomport.ExternalContactFollowInfo
		var followAt time.Time
		if err = rows.Scan(&id, &raw, &followAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &f); err != nil {
			return nil, err
		}
		i, ok := indices[id]
		if !ok {
			return nil, ErrSyncCAS
		}
		out[i].Contact.FollowInfo = append(out[i].Contact.FollowInfo, f)
		out[i].FollowObserved[f.EmployeeID] = followAt
	}
	return out, rows.Err()
}

// Readers hold the shared lock for their entire audience UoW, so multiple Port
// calls cannot combine facts from different publications under READ COMMITTED.
func lockDirectoryRead(ctx context.Context) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('wecom.directory.publication.v1',0))`)
	return err
}

func (PostgreSQLCustomerSyncStore) BeginPublication(ctx context.Context, run CustomerSyncRun) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('wecom.directory.publication.v1',0))`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wecom_directory_publications(corp_scope,revision,last_run_id) VALUES($1,1,$2)
 ON CONFLICT(corp_scope) DO UPDATE SET revision=wecom_directory_publications.revision+1,last_run_id=EXCLUDED.last_run_id`, run.CorpScope, run.ID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `CREATE TEMP TABLE IF NOT EXISTS wecom_tag_union_before ON COMMIT DROP AS
 SELECT DISTINCT customer_id,corp_scope,provider_tag_type,provider_tag_id FROM wecom_customer_tag_observations WHERE observation_status='active'`)
	return err
}

func (PostgreSQLCustomerSyncStore) EndPublication(ctx context.Context, run CustomerSyncRun, at time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	// A change of the employee carrying a tag is not a customer-level addition/removal.
	_, err = tx.Exec(ctx, `WITH after_tags AS (SELECT DISTINCT customer_id,corp_scope,provider_tag_type,provider_tag_id FROM wecom_customer_tag_observations WHERE observation_status='active'),
 eligible AS (SELECT h.id,row_number() OVER (PARTITION BY h.customer_id,h.corp_scope,h.provider_tag_type,h.provider_tag_id,h.event_type='baseline' ORDER BY h.id) AS n
 FROM wecom_customer_tag_history h LEFT JOIN wecom_tag_union_before b USING(customer_id,corp_scope,provider_tag_type,provider_tag_id)
 LEFT JOIN after_tags a USING(customer_id,corp_scope,provider_tag_type,provider_tag_id)
 WHERE h.run_id=$1 AND ((h.event_type='baseline' AND NOT EXISTS(SELECT 1 FROM wecom_customer_tag_history prior WHERE prior.customer_id=h.customer_id AND prior.corp_scope=h.corp_scope AND prior.provider_tag_type=h.provider_tag_type AND prior.provider_tag_id=h.provider_tag_id AND prior.event_type='baseline' AND prior.customer_transition AND prior.to_revision<h.to_revision)) OR (h.event_type IN ('added','readded') AND b.customer_id IS NULL AND a.customer_id IS NOT NULL)
 OR (h.event_type='removed' AND b.customer_id IS NOT NULL AND a.customer_id IS NULL)))
 UPDATE wecom_customer_tag_history h SET customer_transition=true,customer_event_type=CASE WHEN h.event_type IN ('baseline','removed') THEN h.event_type WHEN EXISTS(SELECT 1 FROM wecom_customer_tag_history old WHERE old.customer_id=h.customer_id AND old.corp_scope=h.corp_scope AND old.provider_tag_type=h.provider_tag_type AND old.provider_tag_id=h.provider_tag_id AND old.to_revision<h.to_revision AND old.customer_transition AND old.event_type IN ('baseline','added','readded')) THEN 'readded' ELSE 'added' END FROM eligible e WHERE h.id=e.id AND e.n=1`, run.ID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE wecom_directory_publications SET observed_at=$2,
 full_observed_at=CASE WHEN $3 THEN $2 ELSE full_observed_at END,
 complete=CASE WHEN $3 THEN $4 AND NOT EXISTS(SELECT 1 FROM wecom_customer_owner_observations WHERE last_seen_run_id=$5 AND NOT (COALESCE((detail->>'TagsProjected')::boolean,false) OR detail->'Tags' <> 'null'::jsonb)) ELSE complete END,
 baseline_initialized=baseline_initialized OR ($3 AND $4 AND NOT EXISTS(SELECT 1 FROM wecom_customer_owner_observations WHERE last_seen_run_id=$5 AND NOT COALESCE((detail->>'TagsProjected')::boolean,false))) WHERE corp_scope=$1`, run.CorpScope, at.UTC(), run.Trigger != "new_contact", run.Conflict == 0, run.ID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wecom_external_contact_profiles SET publication_revision=(SELECT revision FROM wecom_directory_publications WHERE corp_scope=$2) WHERE last_seen_run_id=$1 AND corp_scope=$2`, run.ID, run.CorpScope); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wecom_customer_owner_observations SET publication_revision=(SELECT revision FROM wecom_directory_publications WHERE corp_scope=$2) WHERE last_seen_run_id=$1 AND corp_scope=$2`, run.ID, run.CorpScope); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM wecom_customer_follow_staging WHERE run_id=$1`, run.ID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM wecom_customer_profile_staging WHERE run_id=$1`, run.ID)
	return err
}

func (service CustomerSyncService) publishStaged(ctx context.Context, run CustomerSyncRun, at time.Time) error {
	store, ok := service.Store.(directoryPublicationStore)
	if !ok {
		return nil
	}
	if err := store.BeginPublication(ctx, run); err != nil {
		return err
	}
	contacts, err := store.StagedContacts(ctx, run.ID)
	if err != nil {
		return err
	}
	for _, s := range contacts {
		digestRaw, _ := json.Marshal(s.Contact)
		digest := sha256.Sum256(digestRaw)
		// The profile and every employee use the original read clock, not publication
		// completion. A newer incremental read wins even if this full run ends later.
		if err = service.Store.UpsertProfile(ctx, run.ID, run.CorpScope, identityport.ProvisionResult{CustomerID: s.CustomerID, IdentityID: s.IdentityID}, s.Contact, digest, s.ObservedAt); err != nil {
			return err
		}
		current, err := platformpostgres.RequireTransaction(ctx)
		if err != nil {
			return err
		}
		var accepted bool
		if err = current.QueryRow(ctx, `SELECT last_seen_run_id=$2 FROM wecom_external_contact_profiles WHERE customer_id=$1`, s.CustomerID, run.ID).Scan(&accepted); err != nil {
			return err
		}
		if !accepted {
			continue
		}
		if run.Trigger == "new_contact" {
			if _, err = current.Exec(ctx, `UPDATE wecom_external_contact_profiles SET complete_follow_observed_at=$2 WHERE customer_id=$1`, s.CustomerID, s.ObservedAt); err != nil {
				return err
			}
		}
		for _, f := range s.Contact.FollowInfo {
			if err = service.Store.UpsertProfileObservations(ctx, run.ID, run.CorpScope, s.CustomerID, []wecomport.ExternalContactFollowInfo{f}, s.FollowObserved[f.EmployeeID]); err != nil {
				return err
			}
		}
		if err = service.Projection.UpsertDirectoryProjection(ctx, customerport.DirectoryProjection{CustomerID: s.CustomerID, CustomerStatus: customerdomain.StatusActive, DisplayName: s.Contact.Name, AvatarURL: s.Contact.AvatarURL, Gender: s.Contact.Gender, ContactType: s.Contact.Type, CorpName: s.Contact.CorpName, OneIDLabel: "CID-" + strconv.FormatInt(int64(s.CustomerID), 10), ActivationState: "active", Source: "wecom_directory_sync", SourceVersion: run.ID, LastSyncedAt: s.ObservedAt, UpdatedAt: at}); err != nil {
			return err
		}
		if err = service.Timeline.AppendTimeline(ctx, customerport.TimelineEvent{CustomerID: s.CustomerID, SourceDomain: "wecom", SourceEventID: "directory-sync:" + strconv.FormatInt(run.ID, 10) + ":" + strconv.FormatInt(int64(s.CustomerID), 10), EventType: "customer.profile_synced", Title: "企微客户资料已同步", OccurredAt: at}); err != nil {
			return err
		}
	}
	return nil
}

func (s CustomerSyncService) notifyPublication(ctx context.Context, scope string) error {
	if s.PublicationNotifier == nil {
		return nil
	}
	reader, ok := s.Store.(wecomport.DirectoryPublicationReader)
	if !ok {
		return ErrSyncNotReady
	}
	p, err := reader.DirectoryPublication(ctx, scope)
	if err != nil {
		return err
	}
	return s.PublicationNotifier.DirectoryPublishedWithin(ctx, p)
}

// A reduced Provider visibility range cannot prove that contacts disappeared.
// Keep the entire last publication until an operator restores a complete range.
func (PostgreSQLCustomerSyncStore) PublicationScopeComplete(ctx context.Context, run CustomerSyncRun) (bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	var complete bool
	err = tx.QueryRow(ctx, `WITH previous AS (
 SELECT staff_ids FROM wecom_customer_sync_runs WHERE corp_scope=$1 AND id<>$2 AND status='succeeded' AND trigger_type IN ('initial','daily','manual','unionid_refresh') AND jsonb_array_length(staff_ids)>0 ORDER BY completed_at DESC,id DESC LIMIT 1)
 SELECT NOT EXISTS(SELECT 1 FROM previous CROSS JOIN LATERAL jsonb_array_elements_text(previous.staff_ids) staff WHERE staff.value NOT IN(SELECT jsonb_array_elements_text(staff_ids) FROM wecom_customer_sync_runs WHERE id=$2))`, run.CorpScope, run.ID).Scan(&complete)
	return complete, err
}

// A single-contact detail has a complete follow_user set, unlike a staff page.
// Losing a follow user is an observation gap, not an explicit tag removal.
func (PostgreSQLCustomerSyncStore) ReconcileCustomerFollows(ctx context.Context, runID, customerID int64, at time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE wecom_customer_owner_observations SET relationship_status='stale',followed_at=NULL,observed_at=$3,stale_at=$3,updated_at=$3 WHERE customer_id=$2 AND corp_scope=(SELECT corp_scope FROM wecom_customer_sync_runs WHERE id=$1) AND last_seen_run_id<>$1 AND observed_at<$3 AND relationship_status='active' AND EXISTS(SELECT 1 FROM wecom_external_contact_profiles p WHERE p.customer_id=$2 AND p.last_seen_run_id=$1)`, runID, customerID, at.UTC())
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE wecom_customer_tag_observations t SET observation_status='stale',absence_reason='follow_not_observed',stale_at=$3,updated_at=$3 WHERE t.customer_id=$2 AND t.corp_scope=(SELECT corp_scope FROM wecom_customer_sync_runs WHERE id=$1) AND t.observation_status='active' AND EXISTS(SELECT 1 FROM wecom_customer_owner_observations o WHERE o.customer_id=t.customer_id AND o.corp_scope=t.corp_scope AND o.employee_id=t.employee_id AND o.relationship_status='stale' AND o.observed_at=$3)`, runID, customerID, at.UTC())
	return err
}
