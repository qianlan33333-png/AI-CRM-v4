package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"time"
)

func (PostgreSQLCustomerSyncStore) DirectoryPublication(ctx context.Context, scope string) (wecomport.DirectoryPublication, error) {
	if err := lockDirectoryRead(ctx); err != nil {
		return wecomport.DirectoryPublication{}, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return wecomport.DirectoryPublication{}, err
	}
	var p wecomport.DirectoryPublication
	err = tx.QueryRow(ctx, `SELECT revision,last_run_id,observed_at,COALESCE(full_observed_at,'0001-01-01'::timestamptz),complete,baseline_initialized,baseline_date::text FROM wecom_directory_publications WHERE corp_scope=$1 AND revision>0 AND observed_at IS NOT NULL`, scope).Scan(&p.Revision, &p.RunID, &p.ObservedAt, &p.FullObservedAt, &p.Complete, &p.BaselineInitialized, &p.BaselineDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrSyncNotReady
	}
	return p, err
}
func (s PostgreSQLCustomerSyncStore) DirectoryCustomerProfile(ctx context.Context, id customerdomain.CustomerID) (wecomport.DirectoryCustomerProfile, error) {
	if id < 1 {
		return wecomport.DirectoryCustomerProfile{}, ErrSyncNotFound
	}
	if err := lockDirectoryRead(ctx); err != nil {
		return wecomport.DirectoryCustomerProfile{}, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return wecomport.DirectoryCustomerProfile{}, err
	}
	p := wecomport.DirectoryCustomerProfile{CustomerID: id, Availability: "missing", FollowUsers: []wecomport.DirectoryFollow{}}
	var scope string
	err = tx.QueryRow(ctx, `SELECT p.corp_scope,p.activation_status,p.display_name,p.corp_name,p.gender,p.contact_type,p.avatar_url,p.fetched_at FROM wecom_external_contact_profiles p JOIN wecom_customer_sync_runs r ON r.id=p.last_seen_run_id AND r.status='succeeded' WHERE customer_id=$1`, id).Scan(&scope, &p.Availability, &p.DisplayName, &p.CorpName, &p.Gender, &p.ContactType, &p.AvatarURL, &p.CapturedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	p.Publication, err = s.DirectoryPublication(ctx, scope)
	if errors.Is(err, ErrSyncNotReady) {
		p.Availability = "initializing"
		return p, nil
	}
	if err != nil {
		return p, err
	}
	rows, err := tx.Query(ctx, `SELECT o.employee_id,o.relationship_status,o.observed_at,o.detail FROM wecom_customer_owner_observations o JOIN wecom_customer_sync_runs r ON r.id=o.last_seen_run_id AND r.status='succeeded' WHERE o.customer_id=$1 AND o.corp_scope=$2 ORDER BY o.employee_id`, id, scope)
	if err != nil {
		return p, err
	}
	indices := map[string]int{}
	for rows.Next() {
		var f wecomport.DirectoryFollow
		var raw []byte
		if err = rows.Scan(&f.EmployeeID, &f.Status, &f.ObservedAt, &raw); err != nil {
			rows.Close()
			return p, err
		}
		if err = json.Unmarshal(raw, &f.Details); err != nil {
			rows.Close()
			return p, err
		}
		f.Details.Tags = nil
		f.Tags = []wecomport.DirectoryTag{}
		indices[f.EmployeeID] = len(p.FollowUsers)
		p.FollowUsers = append(p.FollowUsers, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	rows, err = tx.Query(ctx, `SELECT t.employee_id,t.provider_tag_id,t.observed_name,t.group_name,t.provider_tag_type,t.observation_status,t.observed_at,t.period_started_at,t.period_start_kind,t.baseline_date::text FROM wecom_customer_tag_observations t JOIN wecom_customer_sync_runs r ON r.id=t.last_seen_run_id AND r.status='succeeded' WHERE t.customer_id=$1 AND t.corp_scope=$2 ORDER BY t.employee_id,t.provider_tag_type,t.provider_tag_id`, id, scope)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var employee string
		var tag wecomport.DirectoryTag
		if err = rows.Scan(&employee, &tag.ID, &tag.Name, &tag.GroupName, &tag.Type, &tag.Status, &tag.ObservedAt, &tag.PeriodStartedAt, &tag.PeriodStartKind, &tag.BaselineDate); err != nil {
			return p, err
		}
		if i, ok := indices[employee]; ok {
			p.FollowUsers[i].Tags = append(p.FollowUsers[i].Tags, tag)
		}
	}
	return p, rows.Err()
}
func validTagHistoryQuery(q wecomport.TagHistoryQuery) bool {
	if q.CustomerID < 0 || q.BeforeID < 0 || q.Limit < 0 || q.Limit > 1000 {
		return false
	}
	for _, s := range []string{q.FromDate, q.ToDate} {
		if s != "" {
			t, err := time.Parse("2006-01-02", s)
			if err != nil || t.Format("2006-01-02") != s {
				return false
			}
		}
	}
	return q.FromDate == "" || q.ToDate == "" || q.FromDate <= q.ToDate
}
func (PostgreSQLCustomerSyncStore) CustomerTagHistory(ctx context.Context, q wecomport.TagHistoryQuery) (wecomport.TagHistoryPage, error) {
	out := wecomport.TagHistoryPage{Items: []wecomport.TagHistoryEvent{}}
	if !validTagHistoryQuery(q) {
		return out, ErrSyncCAS
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT id,customer_id,employee_id,provider_tag_id,provider_tag_type,observed_name,group_name,event_type,reason,discovered_at,discovered_date::text,registration_date::text,previous_observed_at,provider_operated_at,from_revision,to_revision,run_id,customer_transition
 FROM wecom_customer_tag_history WHERE ($1::bigint=0 OR customer_id=$1) AND ($2='' OR employee_id=$2) AND ($3='' OR provider_tag_id=$3)
 AND ($4='' OR registration_date>=NULLIF($4,'')::date) AND ($5='' OR registration_date<=NULLIF($5,'')::date) AND ($6::bigint=0 OR id<$6) ORDER BY id DESC LIMIT $7`, q.CustomerID, q.EmployeeID, q.ProviderTagID, q.FromDate, q.ToDate, q.BeforeID, q.Limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e wecomport.TagHistoryEvent
		if err = rows.Scan(&e.ID, &e.CustomerID, &e.EmployeeID, &e.ProviderTagID, &e.ProviderTagType, &e.Name, &e.GroupName, &e.EventType, &e.Reason, &e.DiscoveredAt, &e.DiscoveredDate, &e.RegistrationDate, &e.PreviousObservedAt, &e.ProviderOperatedAt, &e.FromRevision, &e.ToRevision, &e.RunID, &e.CustomerTransition); err != nil {
			return out, err
		}
		out.Items = append(out.Items, e)
	}
	if len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		out.NextBeforeID = out.Items[len(out.Items)-1].ID
	}
	return out, rows.Err()
}
func (PostgreSQLCustomerSyncStore) CustomerTagHistoryStatistics(ctx context.Context, q wecomport.TagHistoryQuery) ([]wecomport.TagHistoryStatistic, error) {
	if !validTagHistoryQuery(q) {
		return nil, ErrSyncCAS
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT registration_date::text,CASE WHEN $2='' THEN COALESCE(customer_event_type,event_type) ELSE event_type END AS event_type,count(*),count(DISTINCT customer_id) FROM wecom_customer_tag_history
 WHERE ($1::bigint=0 OR customer_id=$1) AND ($2='' OR employee_id=$2) AND ($3='' OR provider_tag_id=$3) AND ($4='' OR registration_date>=NULLIF($4,'')::date) AND ($5='' OR registration_date<=NULLIF($5,'')::date)
 AND ($2<>'' OR customer_transition) GROUP BY 1,2 ORDER BY 1,2`, q.CustomerID, q.EmployeeID, q.ProviderTagID, q.FromDate, q.ToDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wecomport.TagHistoryStatistic{}
	for rows.Next() {
		var s wecomport.TagHistoryStatistic
		if err = rows.Scan(&s.Date, &s.EventType, &s.EventCount, &s.CustomerCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
