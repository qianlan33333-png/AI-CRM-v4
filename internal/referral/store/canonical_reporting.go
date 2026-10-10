package store

import (
	"context"
	"fmt"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

// Reporting projects immutable Referral-owned IDs through the stable OneID
// Port. No identity table is joined and no historical fact is rewritten.
// All calls share the caller's reporting snapshot. The 500-input chunk is the
// existing Identity batch contract, not a cap on campaign participants.
func (r *Repository) canonicalReportingWithin(ctx context.Context, campaignID int64, query string, args []any, viewerArgument int) (string, []any, error) {
	if r.roots == nil {
		return query, args, nil
	} // domain-only synthetic fixtures
	tx, err := transaction(ctx)
	if err != nil {
		return "", nil, err
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT id FROM (
 SELECT promoter_customer_id AS id FROM referral_product_sale_events WHERE campaign_id=$1
 UNION SELECT buyer_customer_id FROM referral_product_sale_events WHERE campaign_id=$1
 UNION SELECT beneficiary_customer_id FROM referral_product_sale_events WHERE campaign_id=$1
 UNION SELECT customer_id FROM referral_participations WHERE campaign_id=$1
 UNION SELECT inviter_customer_id FROM referral_participations WHERE campaign_id=$1
 UNION SELECT inviter_customer_id FROM referral_score_events WHERE campaign_id=$1
 ) subjects WHERE id IS NOT NULL ORDER BY id`, campaignID)
	if err != nil {
		return "", nil, mapError(err)
	}
	ids := []customerdomain.CustomerID{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return "", nil, mapError(err)
		}
		ids = append(ids, customerdomain.CustomerID(id))
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return "", nil, mapError(err)
	}
	viewer := int64(0)
	if viewerArgument >= 0 {
		viewer, _ = args[viewerArgument].(int64)
		if viewer > 0 {
			ids = append(ids, customerdomain.CustomerID(viewer))
		}
	}
	originals, canonical := make([]int64, 0, len(ids)), make([]int64, 0, len(ids))
	seen := map[customerdomain.CustomerID]bool{}
	viewerRoot := viewer
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		roots, e := r.roots.CanonicalCustomerRoots(ctx, ids[start:end])
		if e != nil {
			return "", nil, referralport.ErrUnavailable
		}
		for _, id := range ids[start:end] {
			root, ok := roots[id]
			if !ok || root < 1 {
				return "", nil, referralport.ErrUnavailable
			}
			if int64(id) == viewer {
				viewerRoot = int64(root)
			}
			if !seen[id] {
				originals = append(originals, int64(id))
				canonical = append(canonical, int64(root))
				seen[id] = true
			}
		}
	}
	args = append([]any(nil), args...)
	if viewerArgument >= 0 {
		args[viewerArgument] = viewerRoot
	}
	n := len(args) + 1
	args = append(args, originals, canonical)
	prefix := fmt.Sprintf(`WITH subject_roots AS (SELECT unnest($%d::bigint[]) AS id,unnest($%d::bigint[]) AS root),
 original_sale_events AS (SELECT id,campaign_id,occurred_at FROM referral_product_sale_events WHERE campaign_id=$1),
 original_score_events AS (SELECT id,campaign_id,occurred_at FROM referral_score_events WHERE campaign_id=$1),
 canonical_sale_events AS (
 SELECT e.id,e.campaign_id,p.root AS promoter_customer_id,b.root AS buyer_customer_id,z.root AS beneficiary_customer_id,
 e.team_id,e.occurred_at,e.amount_delta_minor,e.order_count_delta,e.reverses_sale_event_id,e.kind
 FROM referral_product_sale_events e
 LEFT JOIN subject_roots p ON p.id=e.promoter_customer_id
 JOIN subject_roots b ON b.id=e.buyer_customer_id JOIN subject_roots z ON z.id=e.beneficiary_customer_id
 WHERE e.campaign_id=$1 AND (p.root IS NULL OR (p.root<>b.root AND p.root<>z.root))
 ), canonical_participations AS (
 SELECT p.id,p.campaign_id,c.root AS customer_id,p.team_id,p.invitation_id,i.root AS inviter_customer_id,p.inviter_team_id,p.state,p.joined_at
 FROM referral_participations p JOIN subject_roots c ON c.id=p.customer_id
 LEFT JOIN subject_roots i ON i.id=p.inviter_customer_id WHERE p.campaign_id=$1
 ), canonical_score_events AS (
 SELECT e.id,e.campaign_id,e.participation_id,i.root AS inviter_customer_id,c.root AS buyer_customer_id,e.team_id,e.kind,e.delta,e.reverses_score_event_id,e.occurred_at
 FROM referral_score_events e JOIN referral_participations p ON p.id=e.participation_id
 JOIN subject_roots i ON i.id=e.inviter_customer_id JOIN subject_roots c ON c.id=p.customer_id
 WHERE e.campaign_id=$1 AND i.root<>c.root
 )`, n, n+1)
	query = strings.NewReplacer("referral_product_sale_events", "canonical_sale_events", "referral_participations", "canonical_participations", "referral_score_events", "canonical_score_events").Replace(query)
	if strings.HasPrefix(query, "WITH ") {
		query = prefix + ", " + strings.TrimPrefix(query, "WITH ")
	} else {
		query = prefix + " " + query
	}
	return query, args, nil
}

func (r *Repository) canonicalRankingQueriesWithin(ctx context.Context, campaignID int64, kind referralport.LeaderboardKind, query, ownQuery string, args, ownArgs []any, viewer int64, free bool) (string, string, []any, []any, int64, error) {
	if r.roots != nil && free {
		project := func(q string) string {
			q = strings.ReplaceAll(q, "e.occurred_at,e.id", "e.occurred_at,e.id,e.buyer_customer_id")
			q = strings.ReplaceAll(q, "count(*)", "count(DISTINCT buyer_customer_id)")
			if kind == referralport.LeaderboardPersonal {
				q = strings.ReplaceAll(q, "AS customer_id,team_id,max(team_name) AS team_name", "AS customer_id,0::bigint AS team_id,''::text AS team_name")
				q = strings.ReplaceAll(q, "GROUP BY inviter_customer_id,team_id", "GROUP BY inviter_customer_id")
			}
			return q
		}
		query, ownQuery = project(query), project(ownQuery)
	}
	var err error
	viewerArg := -1
	if kind != referralport.LeaderboardTeam {
		viewerArg = len(ownArgs) - 1
	}
	ownQuery, ownArgs, err = r.canonicalReportingWithin(ctx, campaignID, ownQuery, ownArgs, viewerArg)
	if err != nil {
		return "", "", nil, nil, 0, err
	}
	if viewerArg >= 0 {
		viewer = ownArgs[viewerArg].(int64)
	}
	query, args, err = r.canonicalReportingWithin(ctx, campaignID, query, args, -1)
	return query, ownQuery, args, ownArgs, viewer, err
}
