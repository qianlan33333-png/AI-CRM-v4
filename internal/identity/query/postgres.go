package query

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// PostgreSQL reads only Identity-owned tables and requires a transaction-bound
// context for every operation, including administration reads.
type PostgreSQL struct{ phoneVault *identitysecure.PhoneVault }

var _ Reader = PostgreSQL{}
var _ identityport.DirectoryIdentityReader = PostgreSQL{}
var _ identityport.MachineIdentityFactReader = PostgreSQL{}
var _ identityport.MachineIdentityExportReader = PostgreSQL{}
var _ identityport.CommerceResolver = PostgreSQL{}
var _ identityport.PaymentIdentityReader = PostgreSQL{}
var _ identityport.OutboundIdentityReader = PostgreSQL{}
var _ identityport.HXCUnionIDBatchResolver = PostgreSQL{}
var _ identityport.ExternalIdentityValueReader = PostgreSQL{}
var _ identityport.OutboundWeComIdentityReader = PostgreSQL{}
var _ identityport.CanonicalLineageReader = PostgreSQL{}
var _ identityport.CanonicalCustomerRootsReader = PostgreSQL{}
var _ identityport.TrustedCanonicalCustomerReader = PostgreSQL{}
var _ identityport.AdminRadarVisitorIdentityReader = PostgreSQL{}
var _ identityport.VerifiedOutboundPhoneReader = PostgreSQL{}

func (PostgreSQL) VerifiedWeComIdentityForCustomer(ctx context.Context, customerID customerdomain.CustomerID, corpID string) (string, bool, error) {
	if customerID < 1 || strings.TrimSpace(corpID) != corpID || corpID == "" {
		return "", false, identitydomain.ErrInvalidReference
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", false, err
	}
	rows, err := tx.Query(ctx, `SELECT normalized_value FROM customer_identities WHERE customer_id=$1 AND kind='wecom_external_userid' AND scope_key=$2 AND assurance='verified' AND status='active' ORDER BY id LIMIT 2`, customerID, "wecom-corp:"+corpID)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	values := make([]string, 0, 2)
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return "", false, err
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return "", false, err
	}
	if len(values) != 1 {
		return "", false, nil
	}
	return values[0], true, nil
}

// HasActiveVerifiedIdentity supplies a deliberately value-free assurance check
// for a Customer root selected by another bounded workflow (for example, an
// administrator assigning a campaign captain). Callers remain responsible for
// verifying that the requested ID is the canonical root; this query never
// follows a merge or exposes a usable external identity.
func (PostgreSQL) HasActiveVerifiedIdentity(ctx context.Context, customerID customerdomain.CustomerID) (bool, error) {
	if customerID < 1 {
		return false, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	var trusted bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1
		FROM customers customer
		WHERE customer.id=$1 AND customer.status='active'
		  AND EXISTS (
			SELECT 1 FROM customer_identities identity
			WHERE identity.customer_id=customer.id
			  AND identity.assurance='verified'
			  AND identity.status='active'
		  )
	)`, customerID).Scan(&trusted)
	if err != nil {
		return false, err
	}
	return trusted, nil
}

func NewPostgreSQL(phoneVault ...*identitysecure.PhoneVault) PostgreSQL {
	store := PostgreSQL{}
	if len(phoneVault) == 1 {
		store.phoneVault = phoneVault[0]
	}
	return store
}

// CanonicalLineage returns the canonical root and records which are currently
// merged into it. It reads only Identity-owned customer merge state and is
// intentionally a stable read Port rather than an Archive-side table join.
func (PostgreSQL) CanonicalLineage(ctx context.Context, customerID customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	if customerID < 1 {
		return nil, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	// Follow the same persisted canonical pointer used by the existing
	// Customer query. An unexpected cycle or a merged row without a target is
	// corrupt identity state, never a reason to pick an arbitrary deepest row.
	current := int64(customerID)
	seen := map[int64]struct{}{}
	for hops := 0; hops < 128; hops++ {
		if _, duplicate := seen[current]; duplicate {
			return nil, fmt.Errorf("canonical lineage cycle: %w", ErrInvalidQuery)
		}
		seen[current] = struct{}{}
		var status string
		var mergedInto *int64
		err = tx.QueryRow(ctx, `SELECT status,merged_into_customer_id FROM customers WHERE id=$1`, current).Scan(&status, &mergedInto)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("read canonical customer: %w", err)
		}
		if status != "merged" {
			if mergedInto != nil {
				return nil, fmt.Errorf("invalid canonical customer: %w", ErrInvalidQuery)
			}
			break
		}
		if mergedInto == nil || *mergedInto < 1 {
			return nil, fmt.Errorf("invalid merged customer: %w", ErrInvalidQuery)
		}
		current = *mergedInto
		if hops == 127 {
			return nil, fmt.Errorf("canonical lineage too deep: %w", ErrInvalidQuery)
		}
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE descendants(id,visited,cycle) AS (
		SELECT $1::bigint, ARRAY[$1::bigint], false
		UNION ALL
		SELECT customer.id, descendants.visited||customer.id, customer.id=ANY(descendants.visited)
		FROM descendants JOIN customers customer ON customer.merged_into_customer_id=descendants.id
		WHERE customer.status='merged' AND NOT descendants.cycle
	) SELECT id,cycle FROM descendants ORDER BY id`, current)
	if err != nil {
		return nil, fmt.Errorf("query canonical descendants: %w", err)
	}
	defer rows.Close()
	result := []customerdomain.CustomerID{}
	for rows.Next() {
		var id customerdomain.CustomerID
		var cycle bool
		if err = rows.Scan(&id, &cycle); err != nil {
			return nil, err
		}
		if cycle {
			return nil, fmt.Errorf("canonical descendant cycle: %w", ErrInvalidQuery)
		}
		result = append(result, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	return result, nil
}
func (PostgreSQL) ResolveHXCUnionIDs(ctx context.Context, references []identityport.ScopedUnionID) ([]identityport.ScopedUnionIDResult, error) {
	if len(references) == 0 {
		return []identityport.ScopedUnionIDResult{}, nil
	}
	if len(references) > 1000 {
		return nil, identitydomain.ErrInvalidReference
	}
	positions := make([]int32, 0, len(references))
	scopes := make([]string, 0, len(references))
	values := make([]string, 0, len(references))
	for _, reference := range references {
		if reference.Position < 0 || !strings.HasPrefix(reference.Scope, "wechat-open-platform:") || len(reference.Scope) <= len("wechat-open-platform:") || strings.TrimSpace(reference.UnionID) != reference.UnionID || reference.UnionID == "" {
			return nil, identitydomain.ErrInvalidReference
		}
		positions = append(positions, int32(reference.Position))
		scopes = append(scopes, reference.Scope)
		values = append(values, reference.UnionID)
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE input AS (
		SELECT * FROM unnest($1::integer[], $2::text[], $3::text[]) AS i(position, scope_key, value)
	), lineage AS (
		SELECT i.position, c.id AS customer_id, c.status, c.merged_into_customer_id, ARRAY[c.id] visited
		FROM input i JOIN customer_identities ci ON ci.kind='unionid' AND ci.scope_key=i.scope_key AND ci.normalized_value=i.value AND ci.status='active'
		JOIN customers c ON c.id=ci.customer_id
		UNION ALL
		SELECT l.position,c.id,c.status,c.merged_into_customer_id,l.visited||c.id FROM lineage l JOIN customers c ON c.id=l.merged_into_customer_id WHERE NOT c.id=ANY(l.visited)
	), roots AS (
		SELECT position,customer_id FROM lineage WHERE status<>'merged'
	), aggregate_roots AS (
		SELECT position, count(DISTINCT customer_id) AS root_count, min(customer_id) AS customer_id FROM roots GROUP BY position
	)
	SELECT i.position, COALESCE(a.root_count,0), a.customer_id FROM input i LEFT JOIN aggregate_roots a USING(position) ORDER BY i.position`, positions, scopes, values)
	if err != nil {
		return nil, fmt.Errorf("resolve HXC unionids: %w", err)
	}
	defer rows.Close()
	results := make([]identityport.ScopedUnionIDResult, 0, len(references))
	for rows.Next() {
		var position int
		var count int64
		var customerID *int64
		if err = rows.Scan(&position, &count, &customerID); err != nil {
			return nil, fmt.Errorf("scan HXC unionid: %w", err)
		}
		result := identityport.ScopedUnionIDResult{Position: position, Status: identityport.ResolveNotFound}
		if count == 1 && customerID != nil {
			result.Status = identityport.ResolveFound
			result.CustomerID = customerdomain.CustomerID(*customerID)
		}
		if count > 1 {
			result.Status = identityport.ResolveConflict
		}
		results = append(results, result)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate HXC unionids: %w", err)
	}
	return results, nil
}

func (PostgreSQL) VerifiedOutboundIdentity(ctx context.Context, customerID customerdomain.CustomerID, kind identitydomain.Kind, scope string) (identityport.OutboundIdentity, bool, error) {
	if customerID < 1 || identitydomain.ValidateNamespace(kind, scope) != nil {
		return identityport.OutboundIdentity{}, false, identitydomain.ErrInvalidReference
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.OutboundIdentity{}, false, err
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE lineage(id,status,merged_into_customer_id,visited) AS (
	        SELECT id,status,merged_into_customer_id,ARRAY[id] FROM customers WHERE id=$1
	        UNION ALL SELECT c.id,c.status,c.merged_into_customer_id,l.visited||c.id FROM customers c JOIN lineage l ON c.id=l.merged_into_customer_id WHERE NOT c.id=ANY(l.visited)
	    ) SELECT i.id,l.id,i.kind,i.scope_key,i.normalized_value FROM lineage l JOIN customer_identities i ON i.customer_id=l.id WHERE l.status<>'merged' AND i.kind=$2 AND i.scope_key=$3 AND i.assurance='verified' AND i.status='active' ORDER BY i.id LIMIT 2`, customerID, kind, scope)
	if err != nil {
		return identityport.OutboundIdentity{}, false, fmt.Errorf("query verified outbound identity: %w", err)
	}
	defer rows.Close()
	matches := []identityport.OutboundIdentity{}
	for rows.Next() {
		var item identityport.OutboundIdentity
		if err = rows.Scan(&item.IdentityID, &item.CustomerID, &item.Kind, &item.Scope, &item.Value); err != nil {
			return identityport.OutboundIdentity{}, false, fmt.Errorf("query verified outbound identity: %w", err)
		}
		matches = append(matches, item)
	}
	if err = rows.Err(); err != nil {
		return identityport.OutboundIdentity{}, false, fmt.Errorf("query verified outbound identity: %w", err)
	}
	if len(matches) == 0 {
		return identityport.OutboundIdentity{}, false, nil
	}
	if len(matches) > 1 {
		return identityport.OutboundIdentity{}, false, errors.New("outbound identity is ambiguous")
	}
	return matches[0], true, nil
}

// VerifiedOutboundPhone keeps the sensitive phone path outside the generic
// external-identity reader. Phone facts are vault protected, so only one
// active verified CN11 fact on the canonical customer root may be decrypted.
// A missing or ambiguous fact is intentionally not an error: Outbound must
// preserve the payment and record its planned identity-unavailable state
// rather than send an untrusted value.
func (store PostgreSQL) VerifiedOutboundPhone(ctx context.Context, customerID customerdomain.CustomerID, scope string) (string, bool, error) {
	if customerID < 1 || scope != "phone:cn11" {
		return "", false, ErrInvalidQuery
	}
	if store.phoneVault == nil {
		return "", false, errors.New("identity phone vault unavailable")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", false, err
	}
	lineage, err := store.CanonicalLineage(ctx, customerID)
	if err != nil {
		return "", false, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM customers WHERE id=ANY($1::bigint[]) AND status<>'merged' ORDER BY id LIMIT 2`, lineage)
	if err != nil {
		return "", false, fmt.Errorf("query verified outbound phone root: %w", err)
	}
	var roots []customerdomain.CustomerID
	for rows.Next() {
		var root customerdomain.CustomerID
		if err = rows.Scan(&root); err != nil {
			rows.Close()
			return "", false, fmt.Errorf("scan verified outbound phone root: %w", err)
		}
		roots = append(roots, root)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", false, fmt.Errorf("iterate verified outbound phone root: %w", err)
	}
	rows.Close()
	if len(roots) != 1 {
		return "", false, ErrInvalidQuery
	}
	root := roots[0]
	rows, err = tx.Query(ctx, `
	SELECT p.ciphertext
	FROM customer_identities i
	LEFT JOIN identity_phone_secrets p ON p.identity_id=i.id
	WHERE i.customer_id=$1 AND i.kind='phone' AND i.scope_key='phone:cn11' AND i.assurance='verified' AND i.status='active'
	ORDER BY i.id LIMIT 2`, root)
	if err != nil {
		return "", false, fmt.Errorf("query verified outbound phone: %w", err)
	}
	defer rows.Close()
	var ciphertexts [][]byte
	for rows.Next() {
		var ciphertext []byte
		if err = rows.Scan(&ciphertext); err != nil {
			return "", false, fmt.Errorf("scan verified outbound phone: %w", err)
		}
		ciphertexts = append(ciphertexts, ciphertext)
	}
	if err = rows.Err(); err != nil {
		return "", false, fmt.Errorf("iterate verified outbound phone: %w", err)
	}
	if len(ciphertexts) != 1 || len(ciphertexts[0]) == 0 {
		return "", false, nil
	}
	phone, err := store.phoneVault.Decrypt(ciphertexts[0])
	if err != nil {
		return "", false, errors.New("identity phone decrypt failed")
	}
	normalized, err := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: scope, Value: phone, Assurance: identitydomain.AssuranceVerified, Source: "identity.outbound_phone"})
	if err != nil {
		return "", false, errors.New("identity phone vault value invalid")
	}
	return normalized.NormalizedValue, true, nil
}

func (PostgreSQL) VerifiedPaymentIdentity(ctx context.Context, identityID int64, kind identitydomain.Kind, scope string) (identityport.VerifiedCommerceIdentity, bool, error) {
	if identityID < 1 || identitydomain.ValidateNamespace(kind, scope) != nil {
		return identityport.VerifiedCommerceIdentity{}, false, identitydomain.ErrInvalidReference
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.VerifiedCommerceIdentity{}, false, err
	}
	var result identityport.VerifiedCommerceIdentity
	err = tx.QueryRow(ctx, `WITH RECURSIVE lineage(id,status,merged_into_customer_id,visited) AS (
		SELECT c.id,c.status,c.merged_into_customer_id,ARRAY[c.id] FROM customers c JOIN customer_identities i ON i.customer_id=c.id
		WHERE i.id=$1 AND i.kind=$2 AND i.scope_key=$3 AND i.assurance='verified' AND i.status='active'
		UNION ALL SELECT c.id,c.status,c.merged_into_customer_id,l.visited||c.id FROM customers c JOIN lineage l ON c.id=l.merged_into_customer_id WHERE NOT c.id=ANY(l.visited)
	) SELECT i.id,l.id,i.kind,i.scope_key,i.normalized_value FROM customer_identities i JOIN lineage l ON TRUE WHERE i.id=$1 AND l.status<>'merged' LIMIT 1`, identityID, kind, scope).Scan(&result.IdentityID, &result.CustomerID, &result.Kind, &result.Scope, &result.Value)
	if errors.Is(err, pgx.ErrNoRows) {
		return identityport.VerifiedCommerceIdentity{}, false, nil
	}
	if err != nil {
		return identityport.VerifiedCommerceIdentity{}, false, fmt.Errorf("query verified payment identity: %w", err)
	}
	return result, true, nil
}

func (PostgreSQL) ResolveCommerce(ctx context.Context, set identityport.CommerceReferenceSet) (identityport.CommerceResolution, error) {
	if len(set.References) == 0 || len(set.References) > identityport.MaximumCommerceReferences {
		return identityport.CommerceResolution{Status: identityport.CommerceInvalid}, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.CommerceResolution{}, err
	}
	result := identityport.CommerceResolution{Matches: make([]identityport.CommerceIdentityMatch, 0, len(set.References))}
	missing := 0
	for position, reference := range set.References {
		normalized, normalizeErr := identitydomain.Normalize(reference)
		if normalizeErr != nil {
			return identityport.CommerceResolution{Status: identityport.CommerceInvalid}, nil
		}
		var identityID, customerID int64
		var assurance identitydomain.Assurance
		queryErr := tx.QueryRow(ctx, `WITH RECURSIVE lineage(id,status,merged_into_customer_id,visited) AS (
			SELECT c.id,c.status,c.merged_into_customer_id,ARRAY[c.id] FROM customers c
			JOIN customer_identities i ON i.customer_id=c.id
			WHERE i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3 AND i.status='active'
			UNION ALL SELECT c.id,c.status,c.merged_into_customer_id,l.visited || c.id FROM customers c
			JOIN lineage l ON c.id=l.merged_into_customer_id WHERE NOT c.id=ANY(l.visited)
		) SELECT i.id,l.id,i.assurance FROM customer_identities i JOIN lineage l ON TRUE
		WHERE i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3 AND i.status='active'
		AND l.status<>'merged' AND ($4<>'verified' OR i.assurance='verified')
		LIMIT 1`, string(normalized.Kind), normalized.Scope, normalized.NormalizedValue, string(normalized.Assurance)).Scan(&identityID, &customerID, &assurance)
		if errors.Is(queryErr, pgx.ErrNoRows) {
			missing++
			continue
		}
		if queryErr != nil {
			return identityport.CommerceResolution{}, fmt.Errorf("resolve commerce identity: %w", queryErr)
		}
		match := identityport.CommerceIdentityMatch{Position: position, IdentityID: identityID, CustomerID: customerdomain.CustomerID(customerID), Assurance: assurance}
		result.Matches = append(result.Matches, match)
		if result.CustomerID == 0 {
			result.CustomerID = match.CustomerID
		} else if result.CustomerID != match.CustomerID {
			result.Status, result.CustomerID = identityport.CommerceConflict, 0
			return result, nil
		}
	}
	if len(result.Matches) == 0 {
		result.Status, result.CustomerID = identityport.CommerceNotFound, 0
	} else if missing > 0 {
		result.Status, result.CustomerID = identityport.CommercePartial, 0
	} else {
		result.Status = identityport.CommerceResolved
	}
	return result, nil
}

func (PostgreSQL) VerifiedWeComCustomer(ctx context.Context, corpID, externalUserID string) (customerdomain.CustomerID, bool, error) {
	if strings.TrimSpace(corpID) != corpID || corpID == "" || strings.TrimSpace(externalUserID) != externalUserID || externalUserID == "" {
		return 0, false, identitydomain.ErrInvalidReference
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var customerID customerdomain.CustomerID
	err = tx.QueryRow(ctx, `WITH RECURSIVE lineage(id,status,merged_into_customer_id) AS (
		SELECT c.id,c.status,c.merged_into_customer_id FROM customers c JOIN customer_identities i ON i.customer_id=c.id
		WHERE i.kind='wecom_external_userid' AND i.scope_key=$1 AND i.normalized_value=$2 AND i.assurance='verified' AND i.status='active'
		UNION ALL SELECT c.id,c.status,c.merged_into_customer_id FROM customers c JOIN lineage l ON c.id=l.merged_into_customer_id
	) SELECT id FROM lineage WHERE status<>'merged' LIMIT 1`, "wecom-corp:"+corpID, externalUserID).Scan(&customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query verified wecom owner: %w", err)
	}
	return customerID, true, nil
}

func (store PostgreSQL) CustomerForPhone(ctx context.Context, phone string) (customerdomain.CustomerID, bool, error) {
	ref, err := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: phone, Assurance: identitydomain.AssuranceDeclared, Source: "customer_directory"})
	if err != nil {
		return 0, false, identitydomain.ErrInvalidReference
	}
	if store.phoneVault == nil {
		return 0, false, errors.New("identity phone vault unavailable")
	}
	digest := store.phoneVault.LookupDigest(ref.NormalizedValue)
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var customerID customerdomain.CustomerID
	err = tx.QueryRow(ctx, `
		WITH RECURSIVE lineage(id,status,merged_into_customer_id) AS (
			SELECT c.id,c.status,c.merged_into_customer_id FROM customers c
			JOIN customer_identities i ON i.customer_id=c.id
			WHERE i.kind='phone' AND i.status='active' AND ((i.scope_key='phone:cn11' AND i.normalized_value_digest=$1) OR (i.scope_key='phone:e164' AND i.normalized_value=$2))
			UNION ALL SELECT c.id,c.status,c.merged_into_customer_id FROM customers c JOIN lineage l ON c.id=l.merged_into_customer_id
		) SELECT id FROM lineage WHERE status <> 'merged' LIMIT 1`, digest[:], "+86"+ref.NormalizedValue).Scan(&customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query phone owner: %w", err)
	}
	return customerID, true, nil
}

func (PostgreSQL) DirectoryIdentities(ctx context.Context, customerID customerdomain.CustomerID) ([]identityport.DirectoryIdentitySummary, []identityport.MaskedPhone, error) {
	if customerID < 1 {
		return nil, nil, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT i.kind,i.scope_key,i.assurance,i.status,i.source,i.normalized_value,COALESCE(p.masked_value,''),i.created_at FROM customer_identities i LEFT JOIN identity_phone_secrets p ON p.identity_id=i.id WHERE i.customer_id=$1 AND i.status='active' ORDER BY i.id`, customerID)
	if err != nil {
		return nil, nil, fmt.Errorf("query directory identities: %w", err)
	}
	defer rows.Close()
	identities := []identityport.DirectoryIdentitySummary{}
	phones := []identityport.MaskedPhone{}
	for rows.Next() {
		var summary identityport.DirectoryIdentitySummary
		var value, protectedMask string
		if err = rows.Scan(&summary.Kind, &summary.Scope, &summary.Assurance, &summary.Status, &summary.Source, &value, &protectedMask, &summary.CreatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan directory identity: %w", err)
		}
		if summary.Kind == identitydomain.KindPhone {
			if protectedMask == "" {
				protectedMask = maskPhone(value)
			}
			phones = append(phones, identityport.MaskedPhone{Masked: protectedMask, Assurance: summary.Assurance})
		}
		identities = append(identities, summary)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate directory identities: %w", err)
	}
	return identities, phones, nil
}

func (store PostgreSQL) MachineIdentityFacts(ctx context.Context, customerID customerdomain.CustomerID) ([]identityport.MachineIdentityFact, error) {
	export, err := store.MachineIdentityExport(ctx, customerID)
	if err != nil {
		return nil, err
	}
	return export.Facts, nil
}

func (store PostgreSQL) HasVerifiedScopedUnion(ctx context.Context, customerID customerdomain.CustomerID, scope, value string) (bool, error) {
	if customerID < 1 || identitydomain.ValidateNamespace(identitydomain.KindUnionID, scope) != nil || value == "" {
		return false, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	lineage, err := store.CanonicalLineage(ctx, customerID)
	if err != nil || len(lineage) == 0 {
		return false, err
	}
	var verified, conflict bool
	err = tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM customer_identities WHERE customer_id=ANY($1::bigint[]) AND kind='unionid' AND scope_key=$2 AND normalized_value=$3 AND assurance='verified' AND status='active'),
		EXISTS(SELECT 1 FROM customer_identity_conflicts WHERE status='open' AND (left_customer_id=ANY($1::bigint[]) OR right_customer_id=ANY($1::bigint[])))`, lineage, scope, value).Scan(&verified, &conflict)
	return verified && !conflict, err
}

var _ identityport.VerifiedScopedUnionReader = PostgreSQL{}

// MachineIdentityExport follows the Identity-owned canonical lineage before
// exposing facts. It deliberately includes declared phones: assurance remains
// an output fact and callers must not turn it into verified evidence.
func (store PostgreSQL) MachineIdentityExport(ctx context.Context, customerID customerdomain.CustomerID) (identityport.MachineIdentityExport, error) {
	if customerID < 1 {
		return identityport.MachineIdentityExport{}, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.MachineIdentityExport{}, err
	}
	lineage, err := store.CanonicalLineage(ctx, customerID)
	if err != nil {
		return identityport.MachineIdentityExport{}, err
	}
	if len(lineage) == 0 || lineage[0] < 1 {
		return identityport.MachineIdentityExport{}, ErrInvalidQuery
	}
	result := identityport.MachineIdentityExport{Status: identityport.MachineIdentityExportMissing, CanonicalCustomerID: lineage[0], Facts: []identityport.MachineIdentityFact{}}
	var conflict bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_identity_conflicts WHERE status='open' AND (left_customer_id=ANY($1::bigint[]) OR right_customer_id=ANY($1::bigint[])))`, lineage).Scan(&conflict); err != nil {
		return identityport.MachineIdentityExport{}, fmt.Errorf("query machine identity conflict: %w", err)
	}
	if conflict {
		result.Status = identityport.MachineIdentityExportConflict
		return result, nil
	}
	rows, err := tx.Query(ctx, `SELECT i.kind,i.scope_key,i.normalized_value,i.assurance,i.source,i.status,p.ciphertext FROM customer_identities i LEFT JOIN identity_phone_secrets p ON p.identity_id=i.id WHERE i.customer_id=ANY($1::bigint[]) AND i.status='active' ORDER BY i.kind,i.scope_key,i.id`, lineage)
	if err != nil {
		return identityport.MachineIdentityExport{}, fmt.Errorf("query machine identity facts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item identityport.MachineIdentityFact
		var ciphertext []byte
		if err = rows.Scan(&item.Kind, &item.Scope, &item.Value, &item.Assurance, &item.Source, &item.Status, &ciphertext); err != nil {
			return identityport.MachineIdentityExport{}, err
		}
		if item.Kind == identitydomain.KindPhone && len(ciphertext) > 0 {
			if store.phoneVault == nil {
				return identityport.MachineIdentityExport{}, errors.New("identity phone vault unavailable")
			}
			value, e := store.phoneVault.Decrypt(ciphertext)
			if e != nil {
				return identityport.MachineIdentityExport{}, errors.New("identity phone decrypt failed")
			}
			item.Value = value
		}
		result.Facts = append(result.Facts, item)
	}
	if err = rows.Err(); err != nil {
		return identityport.MachineIdentityExport{}, err
	}
	if len(result.Facts) > 0 {
		result.Status = identityport.MachineIdentityExportFound
	}
	return result, nil
}

func (store PostgreSQL) RevealPhone(ctx context.Context, customerID customerdomain.CustomerID) (string, bool, error) {
	if customerID < 1 {
		return "", false, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", false, err
	}
	var legacy string
	var ciphertext []byte
	err = tx.QueryRow(ctx, `SELECT i.normalized_value,p.ciphertext FROM customer_identities i LEFT JOIN identity_phone_secrets p ON p.identity_id=i.id WHERE i.customer_id=$1 AND i.kind='phone' AND i.status='active' ORDER BY (i.assurance='verified') DESC,i.id DESC LIMIT 1`, customerID).Scan(&legacy, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query active phone: %w", err)
	}
	if len(ciphertext) > 0 {
		if store.phoneVault == nil {
			return "", false, errors.New("identity phone vault unavailable")
		}
		phone, decryptErr := store.phoneVault.Decrypt(ciphertext)
		if decryptErr != nil {
			return "", false, errors.New("identity phone decrypt failed")
		}
		return phone, true, nil
	}
	return strings.TrimPrefix(legacy, "+86"), true, nil
}

func (PostgreSQL) VerifiedExternalIdentityValue(ctx context.Context, customerID customerdomain.CustomerID, kind identitydomain.Kind, scope string) (string, bool, error) {
	if customerID < 1 || kind == identitydomain.KindPhone || scope == "" {
		return "", false, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", false, err
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE chain(id,status,merged_into_customer_id,visited) AS (
		SELECT id,status,merged_into_customer_id,ARRAY[id] FROM customers WHERE id=$1
		UNION ALL SELECT c.id,c.status,c.merged_into_customer_id,chain.visited||c.id FROM chain JOIN customers c ON c.id=chain.merged_into_customer_id WHERE NOT c.id=ANY(chain.visited)
	), root AS (SELECT id FROM chain WHERE status<>'merged' ORDER BY cardinality(visited) DESC LIMIT 1)
	SELECT normalized_value FROM customer_identities WHERE customer_id=(SELECT id FROM root) AND kind=$2 AND scope_key=$3 AND assurance='verified' AND status='active' ORDER BY id LIMIT 2`, customerID, kind, scope)
	if err != nil {
		return "", false, fmt.Errorf("query external identity: %w", err)
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return "", false, fmt.Errorf("scan external identity: %w", err)
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return "", false, fmt.Errorf("iterate external identity: %w", err)
	}
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, ErrInvalidQuery
	}
	return values[0], true, nil
}

func maskPhone(value string) string {
	value = strings.TrimPrefix(value, "+86")
	if len(value) <= 7 {
		return "***"
	}
	return value[:len(value)-8] + "****" + value[len(value)-4:]
}

func (PostgreSQL) Customer(ctx context.Context, customerID customerdomain.CustomerID) (CustomerDetail, error) {
	if customerID < 1 {
		return CustomerDetail{}, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return CustomerDetail{}, err
	}

	detail := CustomerDetail{CustomerID: customerID, Identities: []IdentitySummary{}, MergeLineage: []MergeLineageSummary{}}
	err = tx.QueryRow(ctx, `
		WITH RECURSIVE customer_chain AS (
			SELECT id, status, merged_into_customer_id, 0 AS depth, ARRAY[id] AS visited
			FROM customers
			WHERE id = $1
			UNION ALL
			SELECT next.id, next.status, next.merged_into_customer_id, chain.depth + 1, chain.visited || next.id
			FROM customer_chain chain
			JOIN customers next ON next.id = chain.merged_into_customer_id
			WHERE NOT next.id = ANY(chain.visited)
		)
		SELECT
			(SELECT status FROM customer_chain WHERE id = $1),
			id,
			status
		FROM customer_chain
		ORDER BY depth DESC
		LIMIT 1`, int64(customerID)).Scan(&detail.Status, &detail.CanonicalCustomerID, &detail.CanonicalStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerDetail{}, ErrNotFound
	}
	if err != nil {
		return CustomerDetail{}, fmt.Errorf("query customer root: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT kind, scope_key
		FROM customer_identities
		WHERE customer_id = $1 AND status = 'active'
		ORDER BY id`, int64(detail.CanonicalCustomerID))
	if err != nil {
		return CustomerDetail{}, fmt.Errorf("query active customer identities: %w", err)
	}
	for rows.Next() {
		var identity IdentitySummary
		if err = rows.Scan(&identity.Kind, &identity.Scope); err != nil {
			rows.Close()
			return CustomerDetail{}, fmt.Errorf("scan active customer identity: %w", err)
		}
		detail.Identities = append(detail.Identities, identity)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return CustomerDetail{}, fmt.Errorf("iterate active customer identities: %w", err)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		WITH RECURSIVE related_customers(id) AS (
			VALUES ($1::bigint)
			UNION
			SELECT CASE WHEN merge.from_customer_id = related.id
				THEN merge.to_customer_id ELSE merge.from_customer_id END
			FROM related_customers related
			JOIN customer_merges merge
				ON merge.from_customer_id = related.id OR merge.to_customer_id = related.id
		)
		SELECT DISTINCT merge.id, merge.from_customer_id, merge.to_customer_id,
			merge.reversible_status, merge.merged_at, merge.reversed_at
		FROM customer_merges merge
		JOIN related_customers related
			ON related.id = merge.from_customer_id OR related.id = merge.to_customer_id
		ORDER BY merge.id`, int64(customerID))
	if err != nil {
		return CustomerDetail{}, fmt.Errorf("query customer merge lineage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var lineage MergeLineageSummary
		if err = rows.Scan(&lineage.ID, &lineage.FromCustomerID, &lineage.ToCustomerID,
			&lineage.ReversibleStatus, &lineage.MergedAt, &lineage.ReversedAt); err != nil {
			return CustomerDetail{}, fmt.Errorf("scan customer merge lineage: %w", err)
		}
		detail.MergeLineage = append(detail.MergeLineage, lineage)
	}
	if err = rows.Err(); err != nil {
		return CustomerDetail{}, fmt.Errorf("iterate customer merge lineage: %w", err)
	}
	return detail, nil
}

// CanonicalCustomers resolves a bounded audience in one recursive query. It
// follows only Customer-owned merge links and never reads, creates, binds, or
// mutates external identities.
func (PostgreSQL) CanonicalCustomers(ctx context.Context, customerIDs []customerdomain.CustomerID) ([]CanonicalCustomerRoot, error) {
	if len(customerIDs) == 0 {
		return []CanonicalCustomerRoot{}, nil
	}
	if len(customerIDs) > 100000 {
		return nil, ErrInvalidQuery
	}
	ids := make([]int64, len(customerIDs))
	for index, id := range customerIDs {
		if id < 1 {
			return nil, ErrInvalidQuery
		}
		ids[index] = int64(id)
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE requested(requested_id, ordinal) AS (
			SELECT value, ordinal
			FROM unnest($1::bigint[]) WITH ORDINALITY AS input(value, ordinal)
		), customer_chain AS (
			SELECT requested.requested_id, requested.ordinal, customer.id,
				customer.merged_into_customer_id, 0 AS depth, ARRAY[customer.id] AS visited
			FROM requested
			JOIN customers customer ON customer.id=requested.requested_id
			UNION ALL
			SELECT chain.requested_id, chain.ordinal, next.id,
				next.merged_into_customer_id, chain.depth+1, chain.visited||next.id
			FROM customer_chain chain
			JOIN customers next ON next.id=chain.merged_into_customer_id
			WHERE NOT next.id=ANY(chain.visited)
		), resolved AS (
			SELECT DISTINCT ON (ordinal) ordinal, requested_id, id AS customer_id
			FROM customer_chain
			ORDER BY ordinal, depth DESC
		)
		SELECT requested_id, customer_id FROM resolved ORDER BY ordinal`, ids)
	if err != nil {
		return nil, fmt.Errorf("query canonical customer roots: %w", err)
	}
	defer rows.Close()
	result := make([]CanonicalCustomerRoot, 0, len(customerIDs))
	for rows.Next() {
		var item CanonicalCustomerRoot
		if err = rows.Scan(&item.RequestedCustomerID, &item.CustomerID); err != nil {
			return nil, fmt.Errorf("scan canonical customer root: %w", err)
		}
		result = append(result, item)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical customer roots: %w", err)
	}
	if len(result) != len(customerIDs) {
		return nil, ErrNotFound
	}
	return result, nil
}

func (PostgreSQL) Conflicts(ctx context.Context, options ListOptions) (ConflictPage, error) {
	options, err := normalizeOptions(options, map[string]struct{}{"open": {}, "resolved": {}, "ignored": {}})
	if err != nil {
		return ConflictPage{}, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return ConflictPage{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, left_customer_id, right_customer_id, reason, status, created_at, resolved_at
		FROM customer_identity_conflicts
		WHERE status = $1
		ORDER BY id
		LIMIT $2 OFFSET $3`, options.Status, options.Limit, options.Offset)
	if err != nil {
		return ConflictPage{}, fmt.Errorf("query identity conflicts: %w", err)
	}
	defer rows.Close()
	page := ConflictPage{Items: []Conflict{}, Limit: options.Limit, Offset: options.Offset}
	for rows.Next() {
		var item Conflict
		if err = rows.Scan(&item.ID, &item.LeftCustomerID, &item.RightCustomerID, &item.Reason,
			&item.Status, &item.CreatedAt, &item.ResolvedAt); err != nil {
			return ConflictPage{}, fmt.Errorf("scan identity conflict: %w", err)
		}
		item.Reason = publicReason(item.Reason)
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return ConflictPage{}, fmt.Errorf("iterate identity conflicts: %w", err)
	}
	return page, nil
}

func (PostgreSQL) MergeCandidates(ctx context.Context, options ListOptions) (MergeCandidatePage, error) {
	options, err := normalizeOptions(options, map[string]struct{}{"open": {}, "confirmed": {}, "rejected": {}})
	if err != nil {
		return MergeCandidatePage{}, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return MergeCandidatePage{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, left_customer_id, right_customer_id, evidence_strength, reason, status,
			selected_survivor_customer_id, created_at, resolved_at
		FROM customer_merge_candidates
		WHERE status = $1
		ORDER BY id
		LIMIT $2 OFFSET $3`, options.Status, options.Limit, options.Offset)
	if err != nil {
		return MergeCandidatePage{}, fmt.Errorf("query merge candidates: %w", err)
	}
	defer rows.Close()
	page := MergeCandidatePage{Items: []MergeCandidate{}, Limit: options.Limit, Offset: options.Offset}
	for rows.Next() {
		var item MergeCandidate
		if err = rows.Scan(&item.ID, &item.LeftCustomerID, &item.RightCustomerID, &item.EvidenceStrength,
			&item.Reason, &item.Status, &item.SelectedSurvivorCustomerID, &item.CreatedAt, &item.ResolvedAt); err != nil {
			return MergeCandidatePage{}, fmt.Errorf("scan merge candidate: %w", err)
		}
		item.Reason = publicReason(item.Reason)
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return MergeCandidatePage{}, fmt.Errorf("iterate merge candidates: %w", err)
	}
	return page, nil
}

func normalizeOptions(options ListOptions, allowed map[string]struct{}) (ListOptions, error) {
	if options.Status == "" {
		options.Status = "open"
	}
	if _, ok := allowed[options.Status]; !ok || options.Offset < 0 || options.Limit < 0 || options.Limit > MaximumLimit {
		return ListOptions{}, ErrInvalidQuery
	}
	if options.Limit == 0 {
		options.Limit = DefaultLimit
	}
	return options, nil
}

// reason columns are intentionally free text in the ledger. Only application
// reason codes may cross the HTTP query boundary; unexpected historical text
// is collapsed so an accidentally stored identifier cannot become an API leak.
func publicReason(reason string) string {
	switch reason {
	case "cross_root_link_requires_confirmation", "non_strong_evidence",
		"two_wecom_roots", "two_wecom_identities_same_root", "single_value_strong_namespace":
		return reason
	default:
		return "other"
	}
}
