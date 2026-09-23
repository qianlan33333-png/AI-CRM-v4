package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"sort"
	"strings"
	"time"
)

// OneID Resolve only. Provider reads precede local UoW. This bounded manual
// refresh owns no retries, scheduler, lease or external effect.
type GroupMembershipRefresh struct {
	Provider  wecomport.GroupMembershipProvider
	Resolver  identityport.Resolver
	Store     GroupMembershipFactStore
	Audit     audit.Store
	UOW       platformport.UnitOfWork
	CorpScope string
	Now       func() time.Time
}
type GroupMembershipFactStore interface {
	SaveGroupMembership(context.Context, string, string, wecomport.AudienceGroupMembership, string) error
}

func (s GroupMembershipRefresh) Refresh(ctx context.Context, chat string) (wecomport.AudienceGroupMembership, error) {
	result := wecomport.AudienceGroupMembership{CustomerIDs: []customerdomain.CustomerID{}}
	if s.Provider == nil || s.Resolver == nil || s.Store == nil || s.Audit == nil || s.UOW == nil || !strings.HasPrefix(s.CorpScope, "wecom-corp:") || len(s.CorpScope) <= len("wecom-corp:") || strings.TrimSpace(chat) != chat || chat == "" {
		return result, wecomport.ErrGroupMembershipUnavailable
	}
	// Reject nested transaction callers before doing any Provider I/O.
	if _, err := pg.RequireTransaction(ctx); err == nil {
		return result, wecomport.ErrGroupMembershipUnavailable
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	result.ObservedAt = now().UTC()
	// Invalidate the prior snapshot before network I/O, so a failed read or
	// process interruption cannot leave stale success usable for exclusion.
	if err := s.UOW.Within(ctx, func(tx context.Context) error {
		if e := s.Store.SaveGroupMembership(tx, s.CorpScope, chat, result, "refreshing"); e != nil {
			return e
		}
		digest := sha256.Sum256([]byte(s.CorpScope + "\x00" + chat))
		safe := hex.EncodeToString(digest[:])
		key, _ := idempotency.Parse("wecom-group-refresh:" + safe + ":" + result.ObservedAt.Format("20060102T150405.000000000Z"))
		_, e := s.Audit.Append(tx, audit.Event{IdempotencyKey: key, Action: "wecom.group_membership.refreshing", ActorType: "system", ActorID: "group_membership_refresh", ResourceType: "wecom_group", ResourceID: safe, Payload: json.RawMessage(`{}`), OccurredAt: result.ObservedAt})
		return e
	}); err != nil {
		return result, err
	}
	response, readErr := s.Provider.ReadGroupMembership(ctx, chat)
	failure := ""
	if readErr != nil || response.ChatID != chat || response.ExternalUserIDs == nil || len(response.ExternalUserIDs) > 10000 {
		failure = "provider_read_failed"
	}
	err := s.UOW.Within(ctx, func(tx context.Context) error {
		if failure == "" {
			seen := map[string]bool{}
			ids := map[customerdomain.CustomerID]bool{}
			result.ExternalCount = len(response.ExternalUserIDs)
			for _, value := range response.ExternalUserIDs {
				if value == "" || strings.TrimSpace(value) != value || seen[value] {
					failure = "provider_response_invalid"
					break
				}
				seen[value] = true
				result.ExternalIdentityHashes = append(result.ExternalIdentityHashes, groupExternalHash(s.CorpScope, value))
				resolved, e := s.Resolver.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: s.CorpScope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "wecom_group_membership"})
				if e != nil {
					failure = "identity_read_failed"
					break
				}
				if resolved.Status != identityport.ResolveFound || resolved.CustomerID < 1 {
					result.UnresolvedCount++
					continue
				}
				ids[resolved.CustomerID] = true
			}
			for id := range ids {
				result.CustomerIDs = append(result.CustomerIDs, id)
			}
			sort.Slice(result.CustomerIDs, func(i, j int) bool { return result.CustomerIDs[i] < result.CustomerIDs[j] })
			result.ProviderComplete = failure == ""
			if result.UnresolvedCount > 0 {
				failure = "identity_unresolved"
			}
		}
		result.Complete = failure == ""
		if e := s.Store.SaveGroupMembership(tx, s.CorpScope, chat, result, failure); e != nil {
			return e
		}
		digest := sha256.Sum256([]byte(s.CorpScope + "\x00" + chat))
		safe := hex.EncodeToString(digest[:])
		key, _ := idempotency.Parse("wecom-group-membership:" + safe + ":" + result.ObservedAt.Format("20060102T150405.000000000Z"))
		body, _ := json.Marshal(map[string]any{"complete": result.Complete, "provider_complete": result.ProviderComplete, "external_count": result.ExternalCount, "unresolved_count": result.UnresolvedCount, "failure_code": failure})
		_, e := s.Audit.Append(tx, audit.Event{IdempotencyKey: key, Action: "wecom.group_membership.observed", ActorType: "system", ActorID: "group_membership_refresh", ResourceType: "wecom_group", ResourceID: safe, Payload: body, OccurredAt: result.ObservedAt})
		return e
	})
	if err != nil {
		return result, err
	}
	if !result.ProviderComplete {
		return result, wecomport.ErrGroupMembershipUnavailable
	}
	return result, nil
}

type PostgreSQLGroupMembershipFacts struct{}

func (PostgreSQLGroupMembershipFacts) SaveGroupMembership(ctx context.Context, corp, chat string, f wecomport.AudienceGroupMembership, failure string) error {
	tx, e := pg.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	ids := []int64{}
	for _, id := range f.CustomerIDs {
		ids = append(ids, int64(id))
	}
	tag, e := tx.Exec(ctx, `INSERT INTO wecom_group_membership_facts(corp_scope,chat_reference,observed_at,complete,external_count,unresolved_count,customer_ids,failure_code) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(corp_scope,chat_reference) DO UPDATE SET observed_at=excluded.observed_at,complete=excluded.complete,external_count=excluded.external_count,unresolved_count=excluded.unresolved_count,customer_ids=excluded.customer_ids,failure_code=excluded.failure_code WHERE wecom_group_membership_facts.observed_at < excluded.observed_at OR (wecom_group_membership_facts.observed_at = excluded.observed_at AND wecom_group_membership_facts.failure_code='refreshing' AND excluded.failure_code<>'refreshing')`, corp, chat, f.ObservedAt, f.Complete, f.ExternalCount, f.UnresolvedCount, ids, failure)
	if e == nil && tag.RowsAffected() != 1 {
		return wecomport.ErrGroupMembershipUnavailable
	}
	if e != nil {
		return e
	}
	hashes := f.ExternalIdentityHashes
	if hashes == nil {
		hashes = []string{}
	}
	_, e = tx.Exec(ctx, `INSERT INTO wecom_group_provider_facts(corp_scope,chat_reference,observed_at,complete,identity_hashes) VALUES($1,$2,$3,$4,$5) ON CONFLICT(corp_scope,chat_reference) DO UPDATE SET observed_at=excluded.observed_at,complete=excluded.complete,identity_hashes=excluded.identity_hashes`, corp, chat, f.ObservedAt, f.ProviderComplete, hashes)
	if e != nil {
		return e
	}
	if f.ProviderComplete {
		_, e = tx.Exec(ctx, `INSERT INTO wecom_group_provider_observations(corp_scope,chat_reference,observed_at,identity_hashes) VALUES($1,$2,$3,$4)`, corp, chat, f.ObservedAt, hashes)
		if e != nil {
			return e
		}
	}
	if f.Complete {
		_, e = tx.Exec(ctx, `INSERT INTO wecom_group_membership_observations(corp_scope,chat_reference,observed_at,external_count,customer_ids) VALUES($1,$2,$3,$4,$5)`, corp, chat, f.ObservedAt, f.ExternalCount, ids)
	}
	return e
}
func (PostgreSQLGroupMembershipFacts) AudienceGroupMembership(ctx context.Context, corp, chat string, at time.Time, maxAge time.Duration) (wecomport.AudienceGroupMembership, error) {
	f := wecomport.AudienceGroupMembership{}
	if at.IsZero() || maxAge <= 0 || maxAge > 24*time.Hour {
		return f, wecomport.ErrGroupMembershipUnavailable
	}
	tx, e := pg.RequireTransaction(ctx)
	if e != nil {
		return f, e
	}
	var ids []int64
	e = tx.QueryRow(ctx, `SELECT observed_at,complete,external_count,unresolved_count,customer_ids FROM wecom_group_membership_facts WHERE corp_scope=$1 AND chat_reference=$2`, corp, chat).Scan(&f.ObservedAt, &f.Complete, &f.ExternalCount, &f.UnresolvedCount, &ids)
	if e != nil || !f.Complete || f.UnresolvedCount != 0 {
		return f, wecomport.ErrGroupMembershipUnavailable
	}
	// Later successful refreshes must not erase the observation preceding the
	// scheduler's frozen reference. A latest failed/inflight read above still
	// blocks negative selection, regardless of older successful history.
	e = tx.QueryRow(ctx, `SELECT observed_at,external_count,customer_ids FROM wecom_group_membership_observations WHERE corp_scope=$1 AND chat_reference=$2 AND observed_at <= $3 ORDER BY observed_at DESC LIMIT 1`, corp, chat, at).Scan(&f.ObservedAt, &f.ExternalCount, &ids)
	if e != nil {
		return f, wecomport.ErrGroupMembershipUnavailable
	}
	for _, id := range ids {
		f.CustomerIDs = append(f.CustomerIDs, customerdomain.CustomerID(id))
	}
	if !f.Complete || f.UnresolvedCount != 0 || f.ObservedAt.After(at) || at.Sub(f.ObservedAt) > maxAge {
		return f, wecomport.ErrGroupMembershipUnavailable
	}
	return f, nil
}
