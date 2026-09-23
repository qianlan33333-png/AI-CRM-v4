package query

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (store PostgreSQL) HXCSourceConflicts(ctx context.Context, options ListOptions) (HXCSourceConflictPage, error) {
	options, err := normalizeOptions(options, map[string]struct{}{"open": {}, "resolved": {}, "ignored": {}})
	if err != nil {
		return HXCSourceConflictPage{}, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return HXCSourceConflictPage{}, err
	}
	rows, err := tx.Query(ctx, `SELECT c.id,s.subject_digest,c.reason_code,c.left_customer_id,c.right_customer_id,c.merge_candidate_id,
		c.evidence_digest,c.status,c.version,c.created_at,c.resolved_at
		FROM identity_source_conflicts c JOIN identity_source_subjects s ON s.id=c.subject_id
		WHERE s.source_system='hxc' AND c.status=$1 ORDER BY c.id LIMIT $2 OFFSET $3`, options.Status, options.Limit, options.Offset)
	if err != nil {
		return HXCSourceConflictPage{}, fmt.Errorf("query HXC source conflicts: %w", err)
	}
	defer rows.Close()
	page := HXCSourceConflictPage{Items: []HXCSourceConflict{}, Limit: options.Limit, Offset: options.Offset}
	for rows.Next() {
		var item HXCSourceConflict
		var subjectDigest, evidence []byte
		var left, right, candidate *int64
		if err = rows.Scan(&item.ID, &subjectDigest, &item.Reason, &left, &right, &candidate, &evidence, &item.Status, &item.Version, &item.CreatedAt, &item.ResolvedAt); err != nil {
			return HXCSourceConflictPage{}, fmt.Errorf("scan HXC source conflict: %w", err)
		}
		item.SubjectRef = "HXC-" + hex.EncodeToString(subjectDigest[:6])
		item.EvidenceDigest = hex.EncodeToString(evidence)
		if left != nil {
			item.LeftCustomerID = customerdomain.CustomerID(*left)
		}
		if right != nil {
			item.RightCustomerID = customerdomain.CustomerID(*right)
		}
		if candidate != nil {
			item.MergeCandidateID = *candidate
		}
		item.Observations = []HXCSourceObservation{}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return HXCSourceConflictPage{}, fmt.Errorf("iterate HXC source conflicts: %w", err)
	}
	rows.Close()
	for index := range page.Items {
		observationRows, queryErr := tx.Query(ctx, `SELECT o.kind,o.scope_key,o.display_value,o.assurance,o.status
			FROM identity_source_observations o JOIN identity_source_conflicts c ON c.subject_id=o.subject_id
			WHERE c.id=$1 ORDER BY o.kind,o.id DESC`, page.Items[index].ID)
		if queryErr != nil {
			return HXCSourceConflictPage{}, fmt.Errorf("query HXC conflict observations: %w", queryErr)
		}
		for observationRows.Next() {
			var observation HXCSourceObservation
			if scanErr := observationRows.Scan(&observation.Kind, &observation.Scope, &observation.DisplayValue, &observation.Assurance, &observation.Status); scanErr != nil {
				observationRows.Close()
				return HXCSourceConflictPage{}, fmt.Errorf("scan HXC conflict observation: %w", scanErr)
			}
			page.Items[index].Observations = append(page.Items[index].Observations, observation)
		}
		if queryErr = observationRows.Err(); queryErr != nil {
			observationRows.Close()
			return HXCSourceConflictPage{}, fmt.Errorf("iterate HXC conflict observations: %w", queryErr)
		}
		observationRows.Close()
	}
	return page, nil
}

type hxcRoot struct {
	count      int64
	customerID customerdomain.CustomerID
}

func (store PostgreSQL) InspectHXCSubjects(ctx context.Context, subjects []identityport.HXCSubject) ([]identityport.HXCSubjectResult, error) {
	if len(subjects) == 0 {
		return []identityport.HXCSubjectResult{}, nil
	}
	if len(subjects) > 1000 {
		return nil, identitydomain.ErrInvalidReference
	}
	if store.phoneVault == nil {
		return nil, errors.New("identity phone vault unavailable")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	unionPositions, unionScopes, unionValues := []int32{}, []string{}, []string{}
	phonePositions, phoneDigests, legacyPhones := []int32{}, [][]byte{}, []string{}
	results := make([]identityport.HXCSubjectResult, len(subjects))
	valid := make([]bool, len(subjects))
	for index, subject := range subjects {
		results[index] = identityport.HXCSubjectResult{Position: subject.Position, Disposition: identityport.HXCUnmatched, MatchedBy: identityport.HXCMatchNone, Reason: identityport.HXCReasonNoMatch}
		if subject.Position < 0 || subject.SourceUpdatedAt.IsZero() {
			return nil, identitydomain.ErrInvalidReference
		}
		if subject.ConflictReason != "" {
			results[index].Disposition = identityport.HXCConflict
			if subject.ConflictReason == identityport.HXCReasonInvalidUnionID || subject.ConflictReason == identityport.HXCReasonInvalidPhone {
				results[index].Disposition = identityport.HXCInvalid
			}
			results[index].Reason = subject.ConflictReason
			continue
		}
		if subject.UnionID == "" && subject.Phone == "" {
			results[index].Reason = identityport.HXCReasonMissingIdentity
			continue
		}
		valid[index] = true
		if subject.UnionID != "" && subject.UnionIDVerified {
			ref, normalizeErr := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: subject.UnionIDScope, Value: subject.UnionID, Assurance: identitydomain.AssuranceVerified, Source: "hxc"})
			if normalizeErr != nil {
				results[index].Disposition = identityport.HXCInvalid
				results[index].Reason = identityport.HXCReasonInvalidUnionID
				valid[index] = false
				continue
			}
			unionPositions = append(unionPositions, int32(index))
			unionScopes = append(unionScopes, ref.Scope)
			unionValues = append(unionValues, ref.NormalizedValue)
		}
		if subject.Phone != "" {
			ref, normalizeErr := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: subject.Phone, Assurance: identitydomain.AssuranceDeclared, Source: "hxc"})
			if normalizeErr != nil {
				results[index].Disposition = identityport.HXCInvalid
				results[index].Reason = identityport.HXCReasonInvalidPhone
				valid[index] = false
				continue
			}
			digest := store.phoneVault.LookupDigest(ref.NormalizedValue)
			phonePositions = append(phonePositions, int32(index))
			phoneDigests = append(phoneDigests, append([]byte(nil), digest[:]...))
			legacyPhones = append(legacyPhones, "+86"+ref.NormalizedValue)
		}
	}
	unionRoots, err := hxcUnionRoots(ctx, tx, unionPositions, unionScopes, unionValues)
	if err != nil {
		return nil, err
	}
	phoneRoots, err := hxcPhoneRoots(ctx, tx, phonePositions, phoneDigests, legacyPhones)
	if err != nil {
		return nil, err
	}
	for index, subject := range subjects {
		if !valid[index] {
			continue
		}
		results[index] = classifyHXCRoots(subject, unionRoots[index], phoneRoots[index])
	}
	return results, nil
}

func hxcUnionRoots(ctx context.Context, tx pgx.Tx, positions []int32, scopes, values []string) (map[int]hxcRoot, error) {
	if len(positions) == 0 {
		return map[int]hxcRoot{}, nil
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE input AS (
		SELECT * FROM unnest($1::integer[], $2::text[], $3::text[]) AS i(position, scope_key, value)
	), lineage AS (
		SELECT i.position,c.id,c.status,c.merged_into_customer_id,ARRAY[c.id] visited
		FROM input i JOIN customer_identities ci ON ci.kind='unionid' AND ci.scope_key=i.scope_key AND ci.normalized_value=i.value AND ci.assurance='verified' AND ci.status='active'
		JOIN customers c ON c.id=ci.customer_id
		UNION ALL
		SELECT l.position,c.id,c.status,c.merged_into_customer_id,l.visited||c.id
		FROM lineage l JOIN customers c ON c.id=l.merged_into_customer_id WHERE NOT c.id=ANY(l.visited)
	), roots AS (SELECT position,id FROM lineage WHERE status<>'merged')
	SELECT position,count(DISTINCT id),min(id) FROM roots GROUP BY position`, positions, scopes, values)
	if err != nil {
		return nil, fmt.Errorf("resolve HXC unionid roots: %w", err)
	}
	defer rows.Close()
	return scanHXCRoots(rows)
}

func hxcPhoneRoots(ctx context.Context, tx pgx.Tx, positions []int32, digests [][]byte, legacy []string) (map[int]hxcRoot, error) {
	if len(positions) == 0 {
		return map[int]hxcRoot{}, nil
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE input AS (
		SELECT * FROM unnest($1::integer[], $2::bytea[], $3::text[]) AS i(position, digest, legacy_value)
	), lineage AS (
		SELECT i.position,c.id,c.status,c.merged_into_customer_id,ARRAY[c.id] visited
		FROM input i JOIN customer_identities ci ON ci.kind='phone' AND ci.status='active'
		 AND ((ci.scope_key='phone:cn11' AND ci.normalized_value_digest=i.digest)
		   OR (ci.scope_key='phone:e164' AND ci.normalized_value=i.legacy_value))
		JOIN customers c ON c.id=ci.customer_id
		UNION ALL
		SELECT l.position,c.id,c.status,c.merged_into_customer_id,l.visited||c.id
		FROM lineage l JOIN customers c ON c.id=l.merged_into_customer_id WHERE NOT c.id=ANY(l.visited)
	), roots AS (SELECT position,id FROM lineage WHERE status<>'merged')
	SELECT position,count(DISTINCT id),min(id) FROM roots GROUP BY position`, positions, digests, legacy)
	if err != nil {
		return nil, fmt.Errorf("resolve HXC phone roots: %w", err)
	}
	defer rows.Close()
	return scanHXCRoots(rows)
}

func scanHXCRoots(rows pgx.Rows) (map[int]hxcRoot, error) {
	result := map[int]hxcRoot{}
	for rows.Next() {
		var position int
		var root hxcRoot
		if err := rows.Scan(&position, &root.count, &root.customerID); err != nil {
			return nil, err
		}
		result[position] = root
	}
	return result, rows.Err()
}

func classifyHXCRoots(subject identityport.HXCSubject, union, phone hxcRoot) identityport.HXCSubjectResult {
	result := identityport.HXCSubjectResult{Position: subject.Position, Disposition: identityport.HXCUnmatched, MatchedBy: identityport.HXCMatchNone, Reason: identityport.HXCReasonNoMatch}
	if union.count == 1 {
		result.UnionCustomerID = union.customerID
	}
	if phone.count == 1 {
		result.PhoneCustomerID = phone.customerID
	}
	if union.count > 1 || phone.count > 1 {
		result.Disposition, result.Reason = identityport.HXCConflict, identityport.HXCReasonIdentityMultipleRoots
		return result
	}
	if union.count == 1 && phone.count == 1 && union.customerID != phone.customerID {
		result.Disposition, result.Reason = identityport.HXCConflict, identityport.HXCReasonCrossRoot
		return result
	}
	switch {
	case union.count == 1 && phone.count == 1:
		result.Disposition, result.MatchedBy, result.Reason, result.CustomerID = identityport.HXCMatched, identityport.HXCMatchBoth, identityport.HXCReasonMatchedBoth, union.customerID
	case union.count == 1:
		result.Disposition, result.MatchedBy, result.Reason, result.CustomerID = identityport.HXCMatched, identityport.HXCMatchUnionID, identityport.HXCReasonMatchedUnionID, union.customerID
	case phone.count == 1:
		result.Disposition, result.MatchedBy, result.Reason, result.CustomerID = identityport.HXCMatched, identityport.HXCMatchPhone, identityport.HXCReasonMatchedPhone, phone.customerID
	case strings.TrimSpace(subject.UnionID) == "" && strings.TrimSpace(subject.Phone) == "":
		result.Reason = identityport.HXCReasonMissingIdentity
	}
	return result
}
