package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"strings"
	"time"
)

func groupExternalHash(scope, value string) string {
	h := sha256.Sum256([]byte("wecom-group-member-v1\x00" + scope + "\x00" + value))
	return hex.EncodeToString(h[:])
}

type GroupProviderFacts struct{}

func (GroupProviderFacts) AudienceGroupMembership(ctx context.Context, corp, chat string, at time.Time, maxAge time.Duration) (wecomport.AudienceGroupMembership, error) {
	f := wecomport.AudienceGroupMembership{}
	if !strings.HasPrefix(corp, "wecom-corp:") || len(corp) <= len("wecom-corp:") || at.IsZero() || maxAge <= 0 || maxAge > 24*time.Hour {
		return f, wecomport.ErrGroupMembershipUnavailable
	}
	tx, e := pg.RequireTransaction(ctx)
	if e != nil {
		return f, e
	}
	var complete bool
	e = tx.QueryRow(ctx, `SELECT complete FROM wecom_group_provider_facts WHERE corp_scope=$1 AND chat_reference=$2`, corp, chat).Scan(&complete)
	if e != nil || !complete {
		return f, wecomport.ErrGroupMembershipUnavailable
	}
	e = tx.QueryRow(ctx, `SELECT observed_at,identity_hashes FROM wecom_group_provider_observations WHERE corp_scope=$1 AND chat_reference=$2 AND observed_at<=$3 ORDER BY observed_at DESC LIMIT 1`, corp, chat, at).Scan(&f.ObservedAt, &f.ExternalIdentityHashes)
	if e != nil || at.Sub(f.ObservedAt) > maxAge {
		return f, wecomport.ErrGroupMembershipUnavailable
	}
	f.Complete = true
	f.ProviderComplete = true
	return f, nil
}

type GroupCandidateFacts struct {
	Identity identityport.GroupCandidateIdentityReader
}

func (s GroupCandidateFacts) OutsideGroupCandidates(ctx context.Context, corp, chat string, at time.Time, maxAge time.Duration, candidates []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	if s.Identity == nil {
		return nil, wecomport.ErrGroupMembershipUnavailable
	}
	f, e := (GroupProviderFacts{}).AudienceGroupMembership(ctx, corp, chat, at, maxAge)
	if e != nil {
		return nil, e
	}
	return outsideGroupCandidates(ctx, s.Identity, corp, f.ExternalIdentityHashes, candidates)
}
func outsideGroupCandidates(ctx context.Context, reader identityport.GroupCandidateIdentityReader, corp string, hashes []string, candidates []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	members := map[string]bool{}
	for _, h := range hashes {
		members[h] = true
	}
	out := []customerdomain.CustomerID{}
	seen := map[customerdomain.CustomerID]bool{}
	for _, id := range candidates {
		if id < 1 || seen[id] {
			continue
		}
		seen[id] = true
		value, known, e := reader.VerifiedWeComIdentityForCustomer(ctx, id, strings.TrimPrefix(corp, "wecom-corp:"))
		if e != nil {
			return nil, e
		}
		if !known || value == "" {
			continue
		}
		if !members[groupExternalHash(corp, value)] {
			out = append(out, id)
		}
	}
	return out, nil
}
