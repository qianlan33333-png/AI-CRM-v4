// Package store contains the PostgreSQL persistence adapter for OneID.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// PostgresStore has no pool deliberately: every operation obtains the
// UnitOfWork-bound transaction from its context, so an accidental autocommit
// call is rejected by platformpostgres.RequireTransaction.
type PostgresStore struct {
	phoneVault       *identitysecure.PhoneVault
	observationVault *identitysecure.ObservationVault
}

func NewPostgresStore(phoneVault ...*identitysecure.PhoneVault) *PostgresStore {
	store := &PostgresStore{}
	if len(phoneVault) == 1 {
		store.phoneVault = phoneVault[0]
	}
	return store
}

func NewPostgresStoreWithObservation(phoneVault *identitysecure.PhoneVault, observationVault *identitysecure.ObservationVault) *PostgresStore {
	return &PostgresStore{phoneVault: phoneVault, observationVault: observationVault}
}

var _ identityapp.Store = (*PostgresStore)(nil)
var _ identityport.CanonicalCustomerOverviewReader = (*PostgresStore)(nil)

// overviewRuntimeCreationSources is deliberately closed. Every listed value
// is emitted by a current interactive Provider path that calls
// ProvisionVerifiedIdentity at the customer-root creation interaction. A
// directory sync, archive ingestion, or generic WeCom callback can first
// encounter a pre-existing contact, so those sources intentionally remain
// unknown until Identity persists a stronger creation-origin fact.
var overviewRuntimeCreationSources = []string{
	"wechat_miniprogram",
	"wechat.payment.h5_oauth.userinfo",
	"wechat.survey.oauth.userinfo",
	"wechat.radar.oauth",
}

// ReadNewCanonicalCustomerOverview reads only Identity-owned records. A root
// is known-new only when its first persisted verified identity came from an
// explicitly declared interactive runtime source and no immutable historical
// subject receipt names the root. It counts the original root-provision fact
// even if a later lifecycle change retires, closes, or merges that root; a
// later mutable state must not erase a real historical creation measurement.
// This prevents cutover/import rows and any origin that cannot be proven from
// becoming business "new customers".
func (store *PostgresStore) ReadNewCanonicalCustomerOverview(ctx context.Context, window identityport.OverviewWindow) (identityport.NewCanonicalCustomerOverview, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.NewCanonicalCustomerOverview{}, err
	}
	if !window.Valid() {
		return identityport.NewCanonicalCustomerOverview{}, errStore
	}
	result := identityport.NewCanonicalCustomerOverview{Evidence: "identity.runtime_verified_source.v1"}
	err = tx.QueryRow(ctx, `WITH candidates AS (
	SELECT c.id,
		EXISTS(
			SELECT 1 FROM identity_history_import_receipts h
			WHERE h.outcome='canonical' AND h.customer_id=c.id
		) AS historical,
		(
			SELECT i.source FROM customer_identities i
			WHERE i.customer_id=c.id AND i.assurance='verified'
			ORDER BY i.id ASC LIMIT 1
		) AS initial_source
	FROM customers c
	WHERE c.created_at >= $1 AND c.created_at < $2
)
SELECT
	COALESCE(COUNT(*) FILTER (WHERE NOT historical AND initial_source=ANY($3::text[])),0),
	COALESCE(COUNT(*) FILTER (WHERE historical),0),
	COALESCE(COUNT(*) FILTER (WHERE NOT historical AND (initial_source IS NULL OR NOT(initial_source=ANY($3::text[])))),0)
FROM candidates`, window.Start.UTC(), window.End.UTC(), overviewRuntimeCreationSources).Scan(
		&result.KnownNewCanonicalCustomers,
		&result.HistoricalExcluded,
		&result.UnknownSource,
	)
	if err != nil {
		return identityport.NewCanonicalCustomerOverview{}, persistenceFailure(err)
	}
	return result, nil
}

func (store *PostgresStore) AttachDeclaredPhone(ctx context.Context, command identityport.DeclaredPhoneCommand, ref identitydomain.NormalizedReference) (identityport.DeclaredAttachResult, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.DeclaredAttachResult{}, err
	}
	if store.phoneVault == nil {
		return identityport.DeclaredAttachResult{}, errStore
	}
	keyDigest := sha256.Sum256([]byte(command.IdempotencyKey))
	payloadDigest := sha256.Sum256([]byte(strconv.FormatInt(int64(command.CustomerID), 10) + "\x00" + ref.NormalizedValue + "\x00" + command.Source + "\x00" + command.SourceEventID))
	var priorOutcome string
	var priorPayload []byte
	var priorCustomer, priorIdentity *int64
	err = tx.QueryRow(ctx, `SELECT outcome,payload_digest,customer_id,identity_id FROM identity_declared_phone_receipts WHERE key_digest=$1`, keyDigest[:]).Scan(&priorOutcome, &priorPayload, &priorCustomer, &priorIdentity)
	if err == nil {
		if string(priorPayload) != string(payloadDigest[:]) {
			return identityport.DeclaredAttachResult{}, identityapp.ErrDeclaredPayloadMismatch
		}
		result := identityport.DeclaredAttachResult{Status: identityport.DeclaredReplayed, ReplayOf: identityport.DeclaredAttachStatus(priorOutcome)}
		if priorCustomer != nil {
			result.CustomerID = customerdomain.CustomerID(*priorCustomer)
		}
		if priorIdentity != nil {
			result.IdentityID = *priorIdentity
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return identityport.DeclaredAttachResult{}, persistenceFailure(err)
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT status='active' FROM customers WHERE id=$1 FOR UPDATE`, command.CustomerID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		result := identityport.DeclaredAttachResult{Status: identityport.DeclaredInvalid}
		return result, storePhoneReceipt(ctx, tx, keyDigest, payloadDigest, command, result)
	}
	if err != nil {
		return identityport.DeclaredAttachResult{}, persistenceFailure(err)
	}
	lookup := store.phoneVault.LookupDigest(ref.NormalizedValue)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "phone:cn11:"+hex.EncodeToString(lookup[:])); err != nil {
		return identityport.DeclaredAttachResult{}, persistenceFailure(err)
	}
	result := identityport.DeclaredAttachResult{CustomerID: command.CustomerID}
	var owner int64
	err = tx.QueryRow(ctx, `SELECT id,customer_id FROM customer_identities WHERE kind='phone' AND status='active' AND ((scope_key='phone:cn11' AND normalized_value_digest=$1) OR (scope_key='phone:e164' AND normalized_value=$2)) FOR UPDATE`, lookup[:], "+86"+ref.NormalizedValue).Scan(&result.IdentityID, &owner)
	if err == nil {
		if customerdomain.CustomerID(owner) == command.CustomerID {
			result.Status = identityport.DeclaredAlreadyLinked
		} else {
			result.Status = identityport.DeclaredConflict
		}
		return result, storePhoneReceipt(ctx, tx, keyDigest, payloadDigest, command, result)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return identityport.DeclaredAttachResult{}, persistenceFailure(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,normalized_value_digest,assurance,source,source_event_id,normalizer_version,verified_at) VALUES($1,'phone','phone:cn11','',$2,'declared',$3,$4,$5,NULL) RETURNING id`, command.CustomerID, lookup[:], command.Source, command.SourceEventID, ref.NormalizerVersion).Scan(&result.IdentityID); err != nil {
		return identityport.DeclaredAttachResult{}, persistenceFailure(err)
	}
	ciphertext, err := store.phoneVault.Encrypt(ref.NormalizedValue)
	if err != nil {
		return identityport.DeclaredAttachResult{}, errStore
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity_phone_secrets(identity_id,ciphertext,masked_value,key_version) VALUES($1,$2,$3,$4)`, result.IdentityID, ciphertext, identitysecure.MaskPhone(ref.NormalizedValue), identitysecure.PhoneKeyVersion); err != nil {
		return identityport.DeclaredAttachResult{}, persistenceFailure(err)
	}
	result.Status = identityport.DeclaredAttached
	if _, err = tx.Exec(ctx, `UPDATE customers SET version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, command.CustomerID); err != nil {
		return identityport.DeclaredAttachResult{}, persistenceFailure(err)
	}
	return result, storePhoneReceipt(ctx, tx, keyDigest, payloadDigest, command, result)
}

func storePhoneReceipt(ctx context.Context, tx pgx.Tx, keyDigest, payloadDigest [32]byte, command identityport.DeclaredPhoneCommand, result identityport.DeclaredAttachResult) error {
	var customer, identity any
	if result.CustomerID > 0 {
		customer = result.CustomerID
	}
	if result.IdentityID > 0 {
		identity = result.IdentityID
	}
	_, err := tx.Exec(ctx, `INSERT INTO identity_declared_phone_receipts(key_digest,payload_digest,customer_id,identity_id,outcome,source,source_event_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, keyDigest[:], payloadDigest[:], customer, identity, result.Status, command.Source, command.SourceEventID)
	if err != nil {
		return persistenceFailure(err)
	}
	return nil
}

var errStore = errors.New("identity persistence failed")

// persistenceError retains only PostgreSQL's non-sensitive diagnostic labels.
// Error deliberately remains stable: SQL text, detail, hints and values never
// cross the store boundary. Package-local integration tests can still report a
// SQLSTATE and constraint name when a migration or statement is incompatible.
type persistenceError struct {
	code       string
	constraint string
}

func (*persistenceError) Error() string { return errStore.Error() }

func (*persistenceError) Is(target error) bool { return target == errStore }

func persistenceFailure(err error) error {
	if err == nil {
		return errStore
	}
	var safe *persistenceError
	if errors.As(err, &safe) {
		return safe
	}
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) {
		return &persistenceError{code: postgres.Code, constraint: postgres.ConstraintName}
	}
	return errStore
}

func (store *PostgresStore) Resolve(ctx context.Context, reference identitydomain.NormalizedReference) (identityapp.StoredIdentity, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.StoredIdentity{}, false, err
	}
	// Keep the normal matcher independent of the phone-vault migration. Several
	// import and historical-read paths deliberately create a narrow pre-vault
	// identity schema, and non-phone facts must not make those paths depend on
	// phone-only columns.
	match := `i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3`
	args := []any{string(reference.Kind), reference.Scope, reference.NormalizedValue}
	var phoneDigest []byte
	phoneE164 := ""
	if reference.Kind == identitydomain.KindPhone && store.phoneVault != nil {
		cn11 := ""
		switch reference.Scope {
		case "phone:cn11":
			cn11 = reference.NormalizedValue
		case "phone:e164":
			if strings.HasPrefix(reference.NormalizedValue, "+86") {
				cn11 = strings.TrimPrefix(reference.NormalizedValue, "+86")
			}
		}
		// Never derive a CN lookup from a merely eleven-character international
		// value. Normalize again at the narrow CN boundary so the digest and the
		// legacy E.164 fallback are only enabled for a valid mainland number.
		if normalized, normalizeErr := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: cn11, Assurance: identitydomain.AssuranceDeclared, Source: "identity.resolve"}); normalizeErr == nil {
			cn11 = normalized.NormalizedValue
			digest := store.phoneVault.LookupDigest(cn11)
			phoneDigest = digest[:]
			phoneE164 = "+86" + cn11
		}
		// A vault-aware lookup covers protected CN11 facts and the historical
		// plaintext E.164 representation. The explicit kind and non-empty
		// guards prevent an international phone from matching encrypted rows
		// whose legacy normalized value is intentionally empty.
		match = `(i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3) OR
			(i.kind='phone' AND ((i.scope_key='phone:cn11' AND $4::bytea IS NOT NULL AND i.normalized_value_digest=$4) OR
			(i.scope_key='phone:e164' AND $5 <> '' AND i.normalized_value=$5)))`
		args = append(args, phoneDigest, phoneE164)
	}
	var id, customerID int64
	var kind, scope, value, assurance, source string
	var normalizer int16
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE lineage(id, status, merged_into_customer_id) AS (
			SELECT c.id, c.status, c.merged_into_customer_id FROM customers c
			JOIN customer_identities i ON i.customer_id=c.id
		WHERE i.status='active' AND (`+match+`)
			UNION ALL SELECT c.id, c.status, c.merged_into_customer_id FROM customers c JOIN lineage l ON c.id=l.merged_into_customer_id
		) SELECT DISTINCT ON (l.id) i.id, l.id, i.kind, i.scope_key, i.normalized_value, i.assurance, i.source, i.normalizer_version
		FROM customer_identities i JOIN lineage l ON true
		WHERE i.status='active' AND (`+match+`) AND l.status <> 'merged'
		ORDER BY l.id,i.id LIMIT 2`, args...)
	if err != nil {
		return identityapp.StoredIdentity{}, false, persistenceFailure(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if err = rows.Scan(&id, &customerID, &kind, &scope, &value, &assurance, &source, &normalizer); err != nil {
			return identityapp.StoredIdentity{}, false, persistenceFailure(err)
		}
	}
	if err = rows.Err(); err != nil {
		return identityapp.StoredIdentity{}, false, persistenceFailure(err)
	}
	if count == 0 {
		return identityapp.StoredIdentity{}, false, nil
	}
	if count > 1 {
		return identityapp.StoredIdentity{}, false, identityapp.ErrResolveConflict
	}
	if reference.Kind == identitydomain.KindPhone {
		value = reference.NormalizedValue
	}
	return identityapp.StoredIdentity{ID: id, CustomerID: customerdomain.CustomerID(customerID), Reference: identitydomain.NormalizedReference{Kind: identitydomain.Kind(kind), Scope: scope, NormalizedValue: value, Assurance: identitydomain.Assurance(assurance), Source: source, NormalizerVersion: normalizer}}, true, nil
}

func (store *PostgresStore) Provision(ctx context.Context, fact identitydomain.VerifiedFact) (identityapp.ProvisionedIdentity, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.ProvisionedIdentity{}, err
	}
	ref := fact.Reference()
	if err = lockIdentityKey(ctx, tx, ref); err != nil {
		return identityapp.ProvisionedIdentity{}, persistenceFailure(err)
	}
	if existing, found, lookupErr := existingIdentity(ctx, tx, ref); lookupErr != nil {
		return identityapp.ProvisionedIdentity{}, persistenceFailure(lookupErr)
	} else if found {
		return identityapp.ProvisionedIdentity{CustomerID: existing.CustomerID, IdentityID: existing.ID}, nil
	}
	var customerID, identityID int64
	if err = tx.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		return identityapp.ProvisionedIdentity{}, persistenceFailure(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,$2,$3,$4,'verified',$5,$6,CURRENT_TIMESTAMP) RETURNING id`, customerID, string(ref.Kind), ref.Scope, ref.NormalizedValue, ref.Source, ref.NormalizerVersion).Scan(&identityID); err != nil {
		// The advisory key lock serializes every Store writer for this identity.
		// Returning the error rolls the newly inserted customer back atomically.
		return identityapp.ProvisionedIdentity{}, persistenceFailure(err)
	}
	return identityapp.ProvisionedIdentity{CustomerID: customerdomain.CustomerID(customerID), IdentityID: identityID, Created: true}, nil
}

func (store *PostgresStore) Link(ctx context.Context, command identityapp.LinkCommand) (identityapp.LinkResult, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.LinkResult{}, err
	}
	return link(ctx, tx, command)
}

func (store *PostgresStore) CreateLinkIntent(ctx context.Context, command identityapp.LinkIntentCommand) (identityapp.CreatedLinkIntent, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.CreatedLinkIntent{}, err
	}
	root, err := lockActiveRoot(ctx, tx, command.SourceCustomerID)
	if err != nil {
		return identityapp.CreatedLinkIntent{}, identityapp.ErrInvalidLinkCommand
	}
	raw, err := randomToken()
	if err != nil {
		return identityapp.CreatedLinkIntent{}, errStore
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO identity_link_intents(token_hash,source_customer_id,source_customer_version,purpose,target_kind,expected_scope_key,expires_at,source,source_event_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, tokenHash(raw), root.ID, root.Version, string(command.Purpose), string(command.TargetKind), command.ExpectedScope, command.ExpiresAt, command.Source, command.SourceEventID).Scan(&id)
	if err != nil {
		return identityapp.CreatedLinkIntent{}, persistenceFailure(err)
	}
	return identityapp.CreatedLinkIntent{ID: id, Token: raw, ExpiresAt: command.ExpiresAt}, nil
}

func (store *PostgresStore) ConsumeLinkIntent(ctx context.Context, command identityapp.ConsumeLinkIntentCommand) (identityapp.LinkResult, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.LinkResult{}, err
	}
	hash := tokenHash(command.Token)
	intent, err := lockIntent(ctx, tx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return identityapp.LinkResult{Status: identityapp.LinkIntentReplay}, nil
	}
	if err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	fingerprint := consumptionFingerprint(command)
	if intent.Status == "consumed" {
		if intent.Fingerprint != fingerprint {
			return identityapp.LinkResult{}, identityapp.ErrLinkIntentPayloadMismatch
		}
		return replay(intent.Result), nil
	}
	if intent.Status == "expired" {
		return identityapp.LinkResult{Status: identityapp.LinkIntentExpired}, nil
	}
	if intent.Status == "cancelled" {
		return identityapp.LinkResult{Status: identityapp.LinkIntentInvalidated}, nil
	}
	if intent.Status != "pending" {
		return identityapp.LinkResult{Status: identityapp.LinkIntentReplay}, nil
	}
	if !intent.ExpiresAt.After(time.Now()) {
		tag, updateErr := tx.Exec(ctx, `UPDATE identity_link_intents SET status='expired',updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='pending'`, intent.ID)
		if updateErr != nil {
			return identityapp.LinkResult{}, persistenceFailure(updateErr)
		}
		if tag.RowsAffected() != 1 {
			return identityapp.LinkResult{}, identityapp.ErrConcurrentIdentityChange
		}
		return identityapp.LinkResult{Status: identityapp.LinkIntentExpired}, nil
	}
	ref := command.Target.Reference()
	if ref.Kind != identitydomain.Kind(intent.TargetKind) || (intent.ExpectedScope != "" && ref.Scope != intent.ExpectedScope) {
		return identityapp.LinkResult{Status: identityapp.LinkScopeMismatch}, nil
	}
	if err = lockIdentityKey(ctx, tx, ref); err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	root, existing, found, participantErr := lockLinkParticipants(ctx, tx, intent.SourceID, ref)
	if participantErr != nil || root.ID != intent.SourceID || root.Version != intent.SourceVersion {
		if participantErr != nil && !errors.Is(participantErr, errCustomerRootChanged) && !errors.Is(participantErr, pgx.ErrNoRows) {
			return identityapp.LinkResult{}, persistenceFailure(participantErr)
		}
		tag, updateErr := tx.Exec(ctx, `UPDATE identity_link_intents SET status='cancelled',updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='pending'`, intent.ID)
		if updateErr != nil {
			return identityapp.LinkResult{}, persistenceFailure(updateErr)
		}
		if tag.RowsAffected() != 1 {
			return identityapp.LinkResult{}, identityapp.ErrConcurrentIdentityChange
		}
		return identityapp.LinkResult{Status: identityapp.LinkIntentInvalidated}, nil
	}
	// Consumption itself is an immutable identity-owned event, including an
	// attach/already-linked outcome that has no candidate or conflict row.
	evidenceID, err := writeEvidence(ctx, tx, int64(root.ID), 0, 0, 0, command.Evidence)
	if err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	result, err := linkLocked(ctx, tx, root, existing, found, command.Target, command.Evidence)
	if err != nil {
		return identityapp.LinkResult{}, err
	}
	metadata, err := resultSnapshotMetadata(result)
	if err != nil {
		return identityapp.LinkResult{}, errStore
	}
	tag, err := tx.Exec(ctx, `UPDATE identity_link_intents SET status='consumed',consumed_at=CURRENT_TIMESTAMP,consumption_fingerprint=$2,consumed_evidence_id=$3,consumed_identity_id=NULLIF($4::bigint,0::bigint),consumed_customer_id=$5,result_status=$6,result_candidate_id=$7,result_conflict_id=$8,metadata_json=metadata_json || $9::jsonb,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='pending'`, intent.ID, fingerprint, evidenceID, result.IdentityID, result.CustomerID, string(result.Status), nullableID(candidateID(result)), nullableID(conflictID(result)), string(metadata))
	if err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	if tag.RowsAffected() != 1 {
		return identityapp.LinkResult{}, identityapp.ErrConcurrentIdentityChange
	}
	return result, nil
}

func (store *PostgresStore) ConfirmMerge(ctx context.Context, command identityapp.ConfirmMergeCommand) (identityapp.LinkResult, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.LinkResult{}, err
	}
	var left, right int64
	err = tx.QueryRow(ctx, `SELECT left_customer_id,right_customer_id FROM customer_merge_candidates WHERE id=$1`, command.CandidateID).Scan(&left, &right)
	if err != nil {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	if err = lockCustomers(ctx, tx, []int64{left, right}); err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	candidate, err := lockCandidate(ctx, tx, command.CandidateID)
	if err != nil {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	if candidate.Status != "open" || candidate.Evidence.Strength != identitydomain.EvidenceStrong || (int64(command.SurvivorCustomerID) != int64(candidate.LeftCustomerID) && int64(command.SurvivorCustomerID) != int64(candidate.RightCustomerID)) {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	leftRoot, err1 := activeCustomerLocked(ctx, tx, int64(candidate.LeftCustomerID))
	rightRoot, err2 := activeCustomerLocked(ctx, tx, int64(candidate.RightCustomerID))
	if err1 != nil || err2 != nil || leftRoot.Version != candidate.LeftVersion || rightRoot.Version != candidate.RightVersion {
		tag, rejectErr := tx.Exec(ctx, `UPDATE customer_merge_candidates SET status='rejected',resolved_by='system',resolved_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1 AND status='open'`, candidate.ID)
		if rejectErr != nil {
			return identityapp.LinkResult{}, persistenceFailure(rejectErr)
		}
		if tag.RowsAffected() != 1 {
			return identityapp.LinkResult{}, identityapp.ErrConcurrentIdentityChange
		}
		candidate.Status = "rejected"
		return identityapp.LinkResult{Status: identityapp.LinkCandidateRejected, CustomerID: command.SurvivorCustomerID, Candidate: &candidate}, nil
	}
	if reason, err := crossRootConflict(ctx, tx, int64(candidate.LeftCustomerID), int64(candidate.RightCustomerID)); err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	} else if reason != "" {
		conflict, err := createConflict(ctx, tx, int64(candidate.LeftCustomerID), int64(candidate.RightCustomerID), reason, candidate.Evidence)
		if err != nil {
			return identityapp.LinkResult{}, persistenceFailure(err)
		}
		_, err = tx.Exec(ctx, `UPDATE customer_merge_candidates SET status='rejected',resolved_by='system',resolved_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1`, candidate.ID)
		if err != nil {
			return identityapp.LinkResult{}, persistenceFailure(err)
		}
		candidate.Status = "rejected"
		return identityapp.LinkResult{Status: identityapp.LinkConflict, CustomerID: command.SurvivorCustomerID, Candidate: &candidate, Conflict: &conflict}, nil
	}
	leftWecom, err := hasWeCom(ctx, tx, int64(candidate.LeftCustomerID))
	if err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	rightWecom, err := hasWeCom(ctx, tx, int64(candidate.RightCustomerID))
	if err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	if leftWecom != rightWecom && ((leftWecom && int64(command.SurvivorCustomerID) != int64(candidate.LeftCustomerID)) || (rightWecom && int64(command.SurvivorCustomerID) != int64(candidate.RightCustomerID))) {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	loser := int64(candidate.LeftCustomerID)
	if int64(command.SurvivorCustomerID) == int64(candidate.LeftCustomerID) {
		loser = int64(candidate.RightCustomerID)
	}
	// The migration's composite FK deliberately permits a merge ledger only
	// after the candidate names its explicit survivor. Both writes share this
	// transaction, so no externally visible intermediate confirmation exists.
	tag, err := tx.Exec(ctx, `UPDATE customer_merge_candidates SET status='confirmed',selected_survivor_customer_id=$2,resolved_by=$3,resolved_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1 AND status='open'`, candidate.ID, int64(command.SurvivorCustomerID), command.Operator)
	if err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	if tag.RowsAffected() != 1 {
		return identityapp.LinkResult{}, identityapp.ErrConcurrentIdentityChange
	}
	merge, err := performMerge(ctx, tx, candidate, loser, int64(command.SurvivorCustomerID), command.Operator)
	if err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	candidate.Status = "confirmed"
	return identityapp.LinkResult{Status: identityapp.LinkMerged, CustomerID: command.SurvivorCustomerID, Candidate: &candidate, Merge: &merge}, nil
}

func (store *PostgresStore) RevertMerge(ctx context.Context, mergeID int64) (identityapp.MergeRecord, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	var from, to int64
	if err = tx.QueryRow(ctx, `SELECT from_customer_id,to_customer_id FROM customer_merges WHERE id=$1`, mergeID).Scan(&from, &to); err != nil {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	if err = lockCustomers(ctx, tx, []int64{from, to}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
		}
		return identityapp.MergeRecord{}, persistenceFailure(err)
	}
	merge, mergeStatus, err := lockMerge(ctx, tx, mergeID)
	if err != nil || mergeStatus != "not_reversed" {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	fromC, e1 := activeOrMergedCustomer(ctx, tx, from)
	toC, e2 := activeCustomerLocked(ctx, tx, to)
	if e1 != nil || e2 != nil || fromC.Status != "merged" || fromC.MergedTo != to ||
		fromC.Version < merge.FromVersionAfter || toC.Version < merge.ToVersionAfter ||
		fromC.Lineage != merge.FromLineageAfter || toC.Lineage != merge.ToLineageAfter {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	type member struct {
		id, from, to, before, after int64
		restoredAt                  *time.Time
		afterRestore                *int64
		owner, currentVersion       int64
		status                      string
	}
	rows, err := tx.Query(ctx, `SELECT m.identity_id,m.from_customer_id,m.to_customer_id,m.identity_version_before,m.identity_version_after,m.restored_at,m.identity_version_after_restore,i.customer_id,i.version,i.status FROM customer_merge_identity_members m JOIN customer_identities i ON i.id=m.identity_id WHERE m.merge_id=$1 ORDER BY m.identity_id FOR UPDATE OF m,i`, mergeID)
	if err != nil {
		return identityapp.MergeRecord{}, persistenceFailure(err)
	}
	members := []member{}
	for rows.Next() {
		var m member
		if err = rows.Scan(&m.id, &m.from, &m.to, &m.before, &m.after, &m.restoredAt, &m.afterRestore, &m.owner, &m.currentVersion, &m.status); err != nil {
			rows.Close()
			return identityapp.MergeRecord{}, persistenceFailure(err)
		}
		members = append(members, m)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return identityapp.MergeRecord{}, persistenceFailure(err)
	}
	for _, m := range members {
		if m.from != from || m.to != to || m.before < 1 || m.after != m.before+1 ||
			m.restoredAt != nil || m.afterRestore != nil || m.owner != to || m.currentVersion != m.after || m.status != "active" {
			return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
		}
	}
	var later bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_merges WHERE id>$1 AND (from_customer_id=$2 OR to_customer_id=$2 OR from_customer_id=$3 OR to_customer_id=$3))`, mergeID, from, to).Scan(&later)
	if err != nil {
		return identityapp.MergeRecord{}, persistenceFailure(err)
	}
	if later {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	for _, m := range members {
		tag, updateErr := tx.Exec(ctx, `UPDATE customer_identities SET customer_id=$2,version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND customer_id=$3 AND version=$4 AND status='active'`, m.id, from, to, m.after)
		if updateErr != nil {
			return identityapp.MergeRecord{}, persistenceFailure(updateErr)
		}
		if tag.RowsAffected() != 1 {
			return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
		}
		tag, updateErr = tx.Exec(ctx, `UPDATE customer_merge_identity_members SET restored_at=CURRENT_TIMESTAMP,identity_version_after_restore=identity_version_after+1 WHERE merge_id=$1 AND identity_id=$2 AND restored_at IS NULL AND identity_version_after_restore IS NULL`, mergeID, m.id)
		if updateErr != nil {
			return identityapp.MergeRecord{}, persistenceFailure(updateErr)
		}
		if tag.RowsAffected() != 1 {
			return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE customers SET status='active',merged_into_customer_id=NULL,merged_at=NULL,version=version+1,lineage_version=lineage_version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='merged' AND merged_into_customer_id=$2 AND version=$3 AND lineage_version=$4`, from, to, fromC.Version, fromC.Lineage)
	if err != nil {
		return identityapp.MergeRecord{}, persistenceFailure(err)
	}
	if tag.RowsAffected() != 1 {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	tag, err = tx.Exec(ctx, `UPDATE customers SET version=version+1,lineage_version=lineage_version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='active' AND version=$2 AND lineage_version=$3`, to, toC.Version, toC.Lineage)
	if err != nil {
		return identityapp.MergeRecord{}, persistenceFailure(err)
	}
	if tag.RowsAffected() != 1 {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	tag, err = tx.Exec(ctx, `UPDATE customer_merges SET reversible_status='reversed',reversed_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1 AND reversible_status='not_reversed'`, mergeID)
	if err != nil {
		return identityapp.MergeRecord{}, persistenceFailure(err)
	}
	if tag.RowsAffected() != 1 {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	merge.Reversed = true
	return merge, nil
}

// Helpers below deliberately keep all SQL error text private. The public
// application contract maps expected failures to app errors above.
type customer struct {
	ID               int64
	Status           string
	MergedTo         int64
	Version, Lineage int64
}

var errCustomerRootChanged = errors.New("customer root changed concurrently")

func lockIdentityKey(ctx context.Context, tx pgx.Tx, ref identitydomain.NormalizedReference) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, identityLockValue(ref))
	return err
}

func identityLockValue(ref identitydomain.NormalizedReference) string {
	// PostgreSQL text cannot contain NUL. Length prefixes preserve the full
	// namespace tuple without delimiter ambiguity or PII-bearing error output.
	parts := []string{string(ref.Kind), ref.Scope, ref.NormalizedValue}
	value := ""
	for _, part := range parts {
		value += strconv.Itoa(len(part)) + ":" + part
	}
	return value
}
func lockCustomers(ctx context.Context, tx pgx.Tx, ids []int64) error {
	ordered := append([]int64(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	var previous int64
	for index, id := range ordered {
		if id < 1 {
			return errCustomerRootChanged
		}
		if index > 0 && id == previous {
			continue
		}
		var lockedID int64
		if err := tx.QueryRow(ctx, `SELECT id FROM customers WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); err != nil {
			return err
		}
		previous = id
	}
	return nil
}
func activeCustomerLocked(ctx context.Context, tx pgx.Tx, id int64) (customer, error) {
	var c customer
	err := tx.QueryRow(ctx, `SELECT id,status,COALESCE(merged_into_customer_id,0),version,lineage_version FROM customers WHERE id=$1`, id).Scan(&c.ID, &c.Status, &c.MergedTo, &c.Version, &c.Lineage)
	if err != nil || c.Status != "active" {
		return customer{}, errors.New("inactive")
	}
	return c, nil
}
func activeOrMergedCustomer(ctx context.Context, tx pgx.Tx, id int64) (customer, error) {
	var c customer
	err := tx.QueryRow(ctx, `SELECT id,status,COALESCE(merged_into_customer_id,0),version,lineage_version FROM customers WHERE id=$1`, id).Scan(&c.ID, &c.Status, &c.MergedTo, &c.Version, &c.Lineage)
	return c, err
}

func customerPath(ctx context.Context, tx pgx.Tx, start int64) ([]customer, error) {
	seen := make(map[int64]struct{})
	path := make([]customer, 0, 2)
	current := start
	for current > 0 && len(path) < 64 {
		if _, duplicate := seen[current]; duplicate {
			return nil, errCustomerRootChanged
		}
		seen[current] = struct{}{}
		item, err := activeOrMergedCustomer(ctx, tx, current)
		if err != nil {
			return nil, err
		}
		path = append(path, item)
		if item.Status != "merged" {
			return path, nil
		}
		if item.MergedTo < 1 {
			return nil, errCustomerRootChanged
		}
		current = item.MergedTo
	}
	return nil, errCustomerRootChanged
}

type lockedCustomerRoots struct {
	terminalByStart map[int64]customer
	lockedIDs       map[int64]struct{}
}

// lockCustomerPaths first observes every lineage path, locks their union in
// ascending customer-id order, then re-reads the paths. If a concurrent merge
// introduced a root outside that locked set, the operation fails closed rather
// than using a stale source/root association.
func lockCustomerPaths(ctx context.Context, tx pgx.Tx, starts []int64) (lockedCustomerRoots, error) {
	locked := lockedCustomerRoots{
		terminalByStart: make(map[int64]customer, len(starts)),
		lockedIDs:       make(map[int64]struct{}),
	}
	ids := make([]int64, 0, len(starts)*2)
	for _, start := range starts {
		path, err := customerPath(ctx, tx, start)
		if err != nil {
			return lockedCustomerRoots{}, err
		}
		for _, item := range path {
			if _, exists := locked.lockedIDs[item.ID]; exists {
				continue
			}
			locked.lockedIDs[item.ID] = struct{}{}
			ids = append(ids, item.ID)
		}
	}
	if err := lockCustomers(ctx, tx, ids); err != nil {
		return lockedCustomerRoots{}, err
	}
	for _, start := range starts {
		path, err := customerPath(ctx, tx, start)
		if err != nil || len(path) == 0 {
			return lockedCustomerRoots{}, errCustomerRootChanged
		}
		for _, item := range path {
			if _, wasLocked := locked.lockedIDs[item.ID]; !wasLocked {
				return lockedCustomerRoots{}, errCustomerRootChanged
			}
		}
		locked.terminalByStart[start] = path[len(path)-1]
	}
	return locked, nil
}

func (locked lockedCustomerRoots) activeRootFor(ctx context.Context, tx pgx.Tx, owner int64) (customer, error) {
	path, err := customerPath(ctx, tx, owner)
	if err != nil || len(path) == 0 {
		return customer{}, errCustomerRootChanged
	}
	for _, item := range path {
		if _, wasLocked := locked.lockedIDs[item.ID]; !wasLocked {
			return customer{}, errCustomerRootChanged
		}
	}
	root := path[len(path)-1]
	if root.Status != "active" {
		return customer{}, errCustomerRootChanged
	}
	return root, nil
}

func lockActiveRoot(ctx context.Context, tx pgx.Tx, id customerdomain.CustomerID) (customer, error) {
	locked, err := lockCustomerPaths(ctx, tx, []int64{int64(id)})
	if err != nil {
		return customer{}, err
	}
	root := locked.terminalByStart[int64(id)]
	if root.Status != "active" {
		return customer{}, errCustomerRootChanged
	}
	return root, nil
}

type storedIdentityRow struct {
	identityapp.StoredIdentity
	Version int64
}

func lookupIdentity(ctx context.Context, tx pgx.Tx, ref identitydomain.NormalizedReference, forUpdate bool) (storedIdentityRow, bool, error) {
	var row storedIdentityRow
	var cid int64
	var kind, scope, value, assurance, source string
	var nv int16
	query := `SELECT i.id,i.customer_id,i.kind,i.scope_key,i.normalized_value,i.assurance,i.source,i.normalizer_version,i.version FROM customer_identities i WHERE i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3 AND i.status='active'`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, query, string(ref.Kind), ref.Scope, ref.NormalizedValue).Scan(&row.ID, &cid, &kind, &scope, &value, &assurance, &source, &nv, &row.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedIdentityRow{}, false, nil
	}
	if err != nil {
		return storedIdentityRow{}, false, err
	}
	row.CustomerID = customerdomain.CustomerID(cid)
	row.Reference = identitydomain.NormalizedReference{Kind: identitydomain.Kind(kind), Scope: scope, NormalizedValue: value, Assurance: identitydomain.Assurance(assurance), Source: source, NormalizerVersion: nv}
	return row, true, nil
}

func existingIdentity(ctx context.Context, tx pgx.Tx, ref identitydomain.NormalizedReference) (identityapp.StoredIdentity, bool, error) {
	observed, found, err := lookupIdentity(ctx, tx, ref, false)
	if err != nil || !found {
		return identityapp.StoredIdentity{}, found, err
	}
	locked, err := lockCustomerPaths(ctx, tx, []int64{int64(observed.CustomerID)})
	if err != nil {
		return identityapp.StoredIdentity{}, false, err
	}
	current, found, err := lookupIdentity(ctx, tx, ref, true)
	if err != nil || !found || current.ID != observed.ID {
		return identityapp.StoredIdentity{}, false, errCustomerRootChanged
	}
	root, err := locked.activeRootFor(ctx, tx, int64(current.CustomerID))
	if err != nil {
		return identityapp.StoredIdentity{}, false, err
	}
	current.CustomerID = customerdomain.CustomerID(root.ID)
	return current.StoredIdentity, true, nil
}

func link(ctx context.Context, tx pgx.Tx, command identityapp.LinkCommand) (identityapp.LinkResult, error) {
	ref := command.Target.Reference()
	if err := lockIdentityKey(ctx, tx, ref); err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	source, existing, found, err := lockLinkParticipants(ctx, tx, int64(command.SourceCustomerID), ref)
	if err != nil {
		if errors.Is(err, errCustomerRootChanged) || errors.Is(err, pgx.ErrNoRows) {
			return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
		}
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	return linkLocked(ctx, tx, source, existing, found, command.Target, command.Evidence)
}

func lockLinkParticipants(ctx context.Context, tx pgx.Tx, sourceID int64, ref identitydomain.NormalizedReference) (customer, identityapp.StoredIdentity, bool, error) {
	observed, found, err := lookupIdentity(ctx, tx, ref, false)
	if err != nil {
		return customer{}, identityapp.StoredIdentity{}, false, err
	}
	starts := []int64{sourceID}
	if found {
		starts = append(starts, int64(observed.CustomerID))
	}
	locked, err := lockCustomerPaths(ctx, tx, starts)
	if err != nil {
		return customer{}, identityapp.StoredIdentity{}, false, err
	}
	source := locked.terminalByStart[sourceID]
	if source.Status != "active" {
		return customer{}, identityapp.StoredIdentity{}, false, errCustomerRootChanged
	}
	if !found {
		return source, identityapp.StoredIdentity{}, false, nil
	}
	current, currentFound, err := lookupIdentity(ctx, tx, ref, true)
	if err != nil {
		return customer{}, identityapp.StoredIdentity{}, false, err
	}
	if !currentFound || current.ID != observed.ID {
		return customer{}, identityapp.StoredIdentity{}, false, errCustomerRootChanged
	}
	targetRoot, err := locked.activeRootFor(ctx, tx, int64(current.CustomerID))
	if err != nil {
		return customer{}, identityapp.StoredIdentity{}, false, err
	}
	current.CustomerID = customerdomain.CustomerID(targetRoot.ID)
	return source, current.StoredIdentity, true, nil
}

func linkLocked(ctx context.Context, tx pgx.Tx, source customer, existing identityapp.StoredIdentity, found bool, fact identitydomain.VerifiedFact, evidence identitydomain.LinkEvidence) (identityapp.LinkResult, error) {
	ref := fact.Reference()
	if !found {
		if evidence.Strength != identitydomain.EvidenceStrong {
			return identityapp.LinkResult{}, identityapp.ErrInsufficientLinkEvidence
		}
		if reason, err := sameRootConflict(ctx, tx, source.ID, ref); err != nil {
			return identityapp.LinkResult{}, persistenceFailure(err)
		} else if reason != "" {
			conflict, err := createConflict(ctx, tx, source.ID, source.ID, reason, evidence)
			if err != nil {
				return identityapp.LinkResult{}, persistenceFailure(err)
			}
			return identityapp.LinkResult{Status: identityapp.LinkConflict, CustomerID: customerdomain.CustomerID(source.ID), Conflict: &conflict}, nil
		}
		var identityID int64
		err := tx.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,$2,$3,$4,'verified',$5,$6,CURRENT_TIMESTAMP) RETURNING id`, source.ID, string(ref.Kind), ref.Scope, ref.NormalizedValue, ref.Source, ref.NormalizerVersion).Scan(&identityID)
		if err != nil {
			return identityapp.LinkResult{}, persistenceFailure(err)
		}
		tag, err := tx.Exec(ctx, `UPDATE customers SET version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='active' AND version=$2`, source.ID, source.Version)
		if err != nil {
			return identityapp.LinkResult{}, persistenceFailure(err)
		}
		if tag.RowsAffected() != 1 {
			return identityapp.LinkResult{}, identityapp.ErrConcurrentIdentityChange
		}
		return identityapp.LinkResult{Status: identityapp.LinkAttached, CustomerID: customerdomain.CustomerID(source.ID), IdentityID: identityID}, nil
	}
	if existing.CustomerID == customerdomain.CustomerID(source.ID) {
		return identityapp.LinkResult{Status: identityapp.LinkAlreadyLinked, CustomerID: existing.CustomerID, IdentityID: existing.ID}, nil
	}
	left, err := activeCustomerLocked(ctx, tx, source.ID)
	if err != nil {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	right, err := activeCustomerLocked(ctx, tx, int64(existing.CustomerID))
	if err != nil {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	candidate, err := createCandidate(ctx, tx, left, right, evidence, existing.ID)
	if err != nil {
		return identityapp.LinkResult{}, persistenceFailure(err)
	}
	return identityapp.LinkResult{Status: identityapp.LinkCandidate, CustomerID: customerdomain.CustomerID(left.ID), IdentityID: existing.ID, Candidate: &candidate}, nil
}

func writeEvidence(ctx context.Context, tx pgx.Tx, left, right, leftIdentity, rightIdentity int64, evidence identitydomain.LinkEvidence) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO identity_link_evidence(left_customer_id,right_customer_id,left_identity_id,right_identity_id,evidence_type,strength,source,source_event_id,evidence_digest,policy_version) VALUES($1,NULLIF($2,0),NULLIF($3,0),NULLIF($4,0),$5,$6,$7,$8,$9,$10) RETURNING id`, left, right, leftIdentity, rightIdentity, evidence.Type, string(evidence.Strength), evidence.Source, evidence.EventID, evidence.Digest, evidence.PolicyVersion).Scan(&id)
	return id, err
}

func createCandidate(ctx context.Context, tx pgx.Tx, left, right customer, evidence identitydomain.LinkEvidence, rightIdentity int64) (identityapp.MergeCandidate, error) {
	// Both endpoint lineages are already locked in canonical customer-id order,
	// so opposite-direction requests serialize before consulting the partial
	// unique index.
	existing, err := lockCandidateByPair(ctx, tx, left.ID, right.ID)
	if err == nil {
		if candidateVersionsMatch(existing, left, right) {
			if rank(evidence.Strength) <= rank(existing.Evidence.Strength) {
				return existing, nil
			}
			evidenceID, writeErr := writeCandidateEvidence(ctx, tx, int64(existing.LeftCustomerID), int64(existing.RightCustomerID), right.ID, rightIdentity, evidence)
			if writeErr != nil {
				return identityapp.MergeCandidate{}, writeErr
			}
			tag, updateErr := tx.Exec(ctx, `UPDATE customer_merge_candidates SET evidence_id=$2,evidence_strength=$3,reason=$4,version=version+1 WHERE id=$1 AND status='open'`, existing.ID, evidenceID, string(evidence.Strength), reasonFor(evidence))
			if updateErr != nil {
				return identityapp.MergeCandidate{}, updateErr
			}
			if tag.RowsAffected() != 1 {
				return identityapp.MergeCandidate{}, errCustomerRootChanged
			}
			return lockCandidate(ctx, tx, existing.ID)
		}
		tag, rejectErr := tx.Exec(ctx, `UPDATE customer_merge_candidates SET status='rejected',resolved_by='system',resolved_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1 AND status='open'`, existing.ID)
		if rejectErr != nil {
			return identityapp.MergeCandidate{}, rejectErr
		}
		if tag.RowsAffected() != 1 {
			return identityapp.MergeCandidate{}, errCustomerRootChanged
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return identityapp.MergeCandidate{}, err
	}
	evidenceID, err := writeCandidateEvidence(ctx, tx, left.ID, right.ID, right.ID, rightIdentity, evidence)
	if err != nil {
		return identityapp.MergeCandidate{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO customer_merge_candidates(left_customer_id,right_customer_id,left_customer_version,right_customer_version,evidence_id,evidence_strength,reason) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, left.ID, right.ID, left.Version, right.Version, evidenceID, string(evidence.Strength), reasonFor(evidence)).Scan(&id)
	if err != nil {
		return identityapp.MergeCandidate{}, err
	}
	return identityapp.MergeCandidate{ID: id, LeftCustomerID: customerdomain.CustomerID(left.ID), RightCustomerID: customerdomain.CustomerID(right.ID), Evidence: evidence, Reason: reasonFor(evidence), Status: "open", LeftVersion: left.Version, RightVersion: right.Version}, nil
}

func writeCandidateEvidence(ctx context.Context, tx pgx.Tx, left, right, targetCustomer, targetIdentity int64, evidence identitydomain.LinkEvidence) (int64, error) {
	if targetCustomer == left {
		return writeEvidence(ctx, tx, left, right, targetIdentity, 0, evidence)
	}
	if targetCustomer == right {
		return writeEvidence(ctx, tx, left, right, 0, targetIdentity, evidence)
	}
	return 0, errCustomerRootChanged
}

func candidateVersionsMatch(candidate identityapp.MergeCandidate, left, right customer) bool {
	versions := map[int64]int64{left.ID: left.Version, right.ID: right.Version}
	leftVersion, leftFound := versions[int64(candidate.LeftCustomerID)]
	rightVersion, rightFound := versions[int64(candidate.RightCustomerID)]
	return leftFound && rightFound && candidate.LeftVersion == leftVersion && candidate.RightVersion == rightVersion
}
func reasonFor(e identitydomain.LinkEvidence) string {
	if e.Strength == identitydomain.EvidenceStrong {
		return "cross_root_link_requires_confirmation"
	}
	return "non_strong_evidence"
}
func rank(v identitydomain.EvidenceStrength) int {
	if v == identitydomain.EvidenceStrong {
		return 3
	}
	if v == identitydomain.EvidenceMedium {
		return 2
	}
	if v == identitydomain.EvidenceWeak {
		return 1
	}
	return 0
}
func lockCandidateByPair(ctx context.Context, tx pgx.Tx, left, right int64) (identityapp.MergeCandidate, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM customer_merge_candidates WHERE status='open' AND LEAST(left_customer_id,right_customer_id)=LEAST($1::bigint,$2::bigint) AND GREATEST(left_customer_id,right_customer_id)=GREATEST($1::bigint,$2::bigint) FOR UPDATE`, left, right).Scan(&id)
	if err != nil {
		return identityapp.MergeCandidate{}, err
	}
	return lockCandidate(ctx, tx, id)
}

func sameRootConflict(ctx context.Context, tx pgx.Tx, customerID int64, ref identitydomain.NormalizedReference) (string, error) {
	if !singleStrong(ref.Kind) {
		return "", nil
	}
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_identities WHERE customer_id=$1 AND status='active' AND assurance='verified' AND kind=$2 AND scope_key=$3 AND normalized_value<>$4)`, customerID, string(ref.Kind), ref.Scope, ref.NormalizedValue).Scan(&exists)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	if ref.Kind == identitydomain.KindWeComExternalUserID {
		return "two_wecom_identities_same_root", nil
	}
	return "single_value_strong_namespace", nil
}
func hasWeCom(ctx context.Context, tx pgx.Tx, customerID int64) (bool, error) {
	var yes bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_identities WHERE customer_id=$1 AND status='active' AND kind='wecom_external_userid')`, customerID).Scan(&yes)
	return yes, err
}
func crossRootConflict(ctx context.Context, tx pgx.Tx, left, right int64) (string, error) {
	lw, e := hasWeCom(ctx, tx, left)
	if e != nil {
		return "", e
	}
	rw, e := hasWeCom(ctx, tx, right)
	if e != nil {
		return "", e
	}
	if lw && rw {
		return "two_wecom_roots", nil
	}
	var exists bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_identities a JOIN customer_identities b ON a.kind=b.kind AND a.scope_key=b.scope_key AND a.normalized_value<>b.normalized_value WHERE a.customer_id=$1 AND b.customer_id=$2 AND a.status='active' AND b.status='active' AND a.assurance='verified' AND b.assurance='verified' AND a.kind IN ('wecom_external_userid','unionid','mp_openid','oa_openid','alipay_user_id','alipay_oauth_user_id','alipay_oauth_open_id','alipay_buyer_id','alipay_buyer_open_id','first_party_member_id'))`, left, right).Scan(&exists)
	if e != nil {
		return "", e
	}
	if exists {
		return "single_value_strong_namespace", nil
	}
	return "", nil
}
func singleStrong(k identitydomain.Kind) bool {
	return k != identitydomain.KindPhone && k != identitydomain.KindExtension
}

type intentRow struct {
	ID, SourceID, SourceVersion                    int64
	TargetKind, ExpectedScope, Status, Fingerprint string
	ExpiresAt                                      time.Time
	Result                                         identityapp.LinkResult
}

type intentMetadata struct {
	ResultSnapshot *identityapp.LinkResult `json:"result_snapshot,omitempty"`
}

func resultSnapshotMetadata(result identityapp.LinkResult) ([]byte, error) {
	// LinkResult contains only internal ids, policy labels and digest-only
	// evidence; the verified external identity and one-time token are excluded.
	return json.Marshal(intentMetadata{ResultSnapshot: &result})
}

func lockIntent(ctx context.Context, tx pgx.Tx, hash string) (intentRow, error) {
	var row intentRow
	var resultStatus string
	var customerID, identityID, candidateID, conflictID *int64
	var metadata []byte
	err := tx.QueryRow(ctx, `SELECT id,source_customer_id,source_customer_version,target_kind,expected_scope_key,status,expires_at,COALESCE(consumption_fingerprint,''),COALESCE(result_status,''),consumed_customer_id,consumed_identity_id,result_candidate_id,result_conflict_id,metadata_json FROM identity_link_intents WHERE token_hash=$1 FOR UPDATE`, hash).Scan(&row.ID, &row.SourceID, &row.SourceVersion, &row.TargetKind, &row.ExpectedScope, &row.Status, &row.ExpiresAt, &row.Fingerprint, &resultStatus, &customerID, &identityID, &candidateID, &conflictID, &metadata)
	if err != nil {
		return intentRow{}, err
	}
	if resultStatus != "" {
		var decoded intentMetadata
		if row.Status != "consumed" || json.Unmarshal(metadata, &decoded) != nil || decoded.ResultSnapshot == nil ||
			!snapshotMatchesColumns(*decoded.ResultSnapshot, resultStatus, customerID, identityID, candidateID, conflictID) {
			return intentRow{}, errStore
		}
		row.Result = *decoded.ResultSnapshot
	}
	return row, nil
}

func snapshotMatchesColumns(result identityapp.LinkResult, status string, customerID, identityID, storedCandidateID, storedConflictID *int64) bool {
	return string(result.Status) == status && optionalIDMatches(int64(result.CustomerID), customerID) &&
		optionalIDMatches(result.IdentityID, identityID) && optionalIDMatches(candidateID(result), storedCandidateID) &&
		optionalIDMatches(conflictID(result), storedConflictID) && result.Merge == nil && result.ReplayOf == ""
}

func optionalIDMatches(value int64, stored *int64) bool {
	if stored == nil {
		return value == 0
	}
	return value == *stored
}
func candidateID(result identityapp.LinkResult) int64 {
	if result.Candidate != nil {
		return result.Candidate.ID
	}
	return 0
}
func conflictID(result identityapp.LinkResult) int64 {
	if result.Conflict != nil {
		return result.Conflict.ID
	}
	return 0
}
func nullableID(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
func replay(original identityapp.LinkResult) identityapp.LinkResult {
	original.ReplayOf = original.Status
	original.Status = identityapp.LinkIntentReplay
	return original
}

func lockCandidate(ctx context.Context, tx pgx.Tx, id int64) (identityapp.MergeCandidate, error) {
	var c identityapp.MergeCandidate
	var strength string
	err := tx.QueryRow(ctx, `SELECT c.id,c.left_customer_id,c.right_customer_id,c.left_customer_version,c.right_customer_version,c.evidence_strength,c.reason,c.status,e.evidence_type,e.source,e.source_event_id,e.evidence_digest,e.policy_version FROM customer_merge_candidates c JOIN identity_link_evidence e ON e.id=c.evidence_id WHERE c.id=$1 FOR UPDATE`, id).Scan(&c.ID, &c.LeftCustomerID, &c.RightCustomerID, &c.LeftVersion, &c.RightVersion, &strength, &c.Reason, &c.Status, &c.Evidence.Type, &c.Evidence.Source, &c.Evidence.EventID, &c.Evidence.Digest, &c.Evidence.PolicyVersion)
	c.Evidence.Strength = identitydomain.EvidenceStrength(strength)
	return c, err
}
func readConflict(ctx context.Context, tx pgx.Tx, id int64) (identityapp.Conflict, error) {
	var c identityapp.Conflict
	var strength string
	err := tx.QueryRow(ctx, `SELECT c.id,c.left_customer_id,c.right_customer_id,c.reason,e.evidence_type,e.strength,e.source,e.source_event_id,e.evidence_digest,e.policy_version FROM customer_identity_conflicts c LEFT JOIN identity_link_evidence e ON e.id=c.evidence_id WHERE c.id=$1`, id).Scan(&c.ID, &c.LeftCustomerID, &c.RightCustomerID, &c.Reason, &c.Evidence.Type, &strength, &c.Evidence.Source, &c.Evidence.EventID, &c.Evidence.Digest, &c.Evidence.PolicyVersion)
	c.Evidence.Strength = identitydomain.EvidenceStrength(strength)
	return c, err
}
func lockMerge(ctx context.Context, tx pgx.Tx, id int64) (identityapp.MergeRecord, string, error) {
	var m identityapp.MergeRecord
	var reversedAt *time.Time
	var status, strength string
	err := tx.QueryRow(ctx, `SELECT m.id,m.candidate_id,m.from_customer_id,m.to_customer_id,m.from_customer_version_before,m.from_customer_version_after,m.to_customer_version_before,m.to_customer_version_after,m.from_lineage_version_before,m.from_lineage_version_after,m.to_lineage_version_before,m.to_lineage_version_after,m.rule,m.operator,m.reversible_status,m.reversed_at,e.evidence_type,e.strength,e.source,e.source_event_id,e.evidence_digest,e.policy_version FROM customer_merges m JOIN identity_link_evidence e ON e.id=m.evidence_id WHERE m.id=$1 FOR UPDATE OF m`, id).Scan(&m.ID, &m.CandidateID, &m.FromCustomerID, &m.ToCustomerID, &m.FromVersionBefore, &m.FromVersionAfter, &m.ToVersionBefore, &m.ToVersionAfter, &m.FromLineageBefore, &m.FromLineageAfter, &m.ToLineageBefore, &m.ToLineageAfter, &m.Rule, &m.Operator, &status, &reversedAt, &m.Evidence.Type, &strength, &m.Evidence.Source, &m.Evidence.EventID, &m.Evidence.Digest, &m.Evidence.PolicyVersion)
	if err != nil {
		return identityapp.MergeRecord{}, "", err
	}
	m.Evidence.Strength = identitydomain.EvidenceStrength(strength)
	m.Reversed = status == "reversed"
	return m, status, nil
}
func createConflict(ctx context.Context, tx pgx.Tx, left, right int64, reason string, e identitydomain.LinkEvidence) (identityapp.Conflict, error) {
	// canonicalization is only for pair de-duplication, never a survivor choice.
	if right < left {
		left, right = right, left
	}
	var existingID int64
	err := tx.QueryRow(ctx, `SELECT id FROM customer_identity_conflicts WHERE status='open' AND LEAST(left_customer_id,right_customer_id)=LEAST($1::bigint,$2::bigint) AND GREATEST(left_customer_id,right_customer_id)=GREATEST($1::bigint,$2::bigint) AND reason=$3 FOR UPDATE`, left, right, reason).Scan(&existingID)
	if err == nil {
		return readConflict(ctx, tx, existingID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return identityapp.Conflict{}, err
	}
	evidenceID, err := writeEvidence(ctx, tx, left, right, 0, 0, e)
	if err != nil {
		return identityapp.Conflict{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO customer_identity_conflicts(left_customer_id,right_customer_id,evidence_id,reason) VALUES($1,$2,$3,$4) RETURNING id`, left, right, evidenceID, reason).Scan(&id)
	if err != nil {
		return identityapp.Conflict{}, err
	}
	return identityapp.Conflict{ID: id, LeftCustomerID: customerdomain.CustomerID(left), RightCustomerID: customerdomain.CustomerID(right), Reason: reason, Evidence: e}, nil
}

func performMerge(ctx context.Context, tx pgx.Tx, candidate identityapp.MergeCandidate, loser, survivor int64, operator string) (identityapp.MergeRecord, error) {
	from, err := activeCustomerLocked(ctx, tx, loser)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	to, err := activeCustomerLocked(ctx, tx, survivor)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	// Members are locked only after candidate and roots. Snapshot every active
	// member before moving it, so reversal cannot move later identities.
	rows, err := tx.Query(ctx, `SELECT id,version FROM customer_identities WHERE customer_id=$1 AND status='active' ORDER BY id FOR UPDATE`, loser)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	type member struct{ id, version int64 }
	members := []member{}
	for rows.Next() {
		var m member
		if err = rows.Scan(&m.id, &m.version); err != nil {
			rows.Close()
			return identityapp.MergeRecord{}, err
		}
		members = append(members, m)
	}
	rows.Close()
	if rows.Err() != nil {
		return identityapp.MergeRecord{}, rows.Err()
	}
	var evidenceID int64
	err = tx.QueryRow(ctx, `SELECT evidence_id FROM customer_merge_candidates WHERE id=$1`, candidate.ID).Scan(&evidenceID)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	var mergeID int64
	err = tx.QueryRow(ctx, `INSERT INTO customer_merges(candidate_id,candidate_left_customer_id,candidate_right_customer_id,from_customer_id,to_customer_id,evidence_id,from_customer_version_before,from_customer_version_after,to_customer_version_before,to_customer_version_after,from_lineage_version_before,from_lineage_version_after,to_lineage_version_before,to_lineage_version_after,rule,operator,source) VALUES($1::bigint,$2::bigint,$3::bigint,$4::bigint,$5::bigint,$6::bigint,$7::bigint,$7::bigint+1,$8::bigint,$8::bigint+1,$9::bigint,$9::bigint+1,$10::bigint,$10::bigint+1,'confirmed_candidate',$11,'identity') RETURNING id`, candidate.ID, candidate.LeftCustomerID, candidate.RightCustomerID, loser, survivor, evidenceID, from.Version, to.Version, from.Lineage, to.Lineage, operator).Scan(&mergeID)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	for _, m := range members {
		tag, e := tx.Exec(ctx, `UPDATE customer_identities SET customer_id=$2,version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND customer_id=$3 AND version=$4`, m.id, survivor, loser, m.version)
		if e != nil {
			return identityapp.MergeRecord{}, persistenceFailure(e)
		}
		if tag.RowsAffected() != 1 {
			return identityapp.MergeRecord{}, errCustomerRootChanged
		}
		if _, e = tx.Exec(ctx, `INSERT INTO customer_merge_identity_members(merge_id,identity_id,from_customer_id,to_customer_id,identity_version_before,identity_version_after) VALUES($1::bigint,$2::bigint,$3::bigint,$4::bigint,$5::bigint,$5::bigint+1)`, mergeID, m.id, loser, survivor, m.version); e != nil {
			return identityapp.MergeRecord{}, e
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=CURRENT_TIMESTAMP,version=version+1,lineage_version=lineage_version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='active' AND version=$3 AND lineage_version=$4`, loser, survivor, from.Version, from.Lineage)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	if tag.RowsAffected() != 1 {
		return identityapp.MergeRecord{}, errCustomerRootChanged
	}
	tag, err = tx.Exec(ctx, `UPDATE customers SET version=version+1,lineage_version=lineage_version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='active' AND version=$2 AND lineage_version=$3`, survivor, to.Version, to.Lineage)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	if tag.RowsAffected() != 1 {
		return identityapp.MergeRecord{}, errCustomerRootChanged
	}
	return identityapp.MergeRecord{ID: mergeID, CandidateID: candidate.ID, FromCustomerID: customerdomain.CustomerID(loser), ToCustomerID: customerdomain.CustomerID(survivor), FromVersionBefore: from.Version, FromVersionAfter: from.Version + 1, ToVersionBefore: to.Version, ToVersionAfter: to.Version + 1, FromLineageBefore: from.Lineage, FromLineageAfter: from.Lineage + 1, ToLineageBefore: to.Lineage, ToLineageAfter: to.Lineage + 1, Evidence: candidate.Evidence, Rule: "confirmed_candidate", Operator: operator}, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func tokenHash(token string) string {
	d := sha256.Sum256([]byte(token))
	return base64.RawStdEncoding.EncodeToString(d[:])
}
func consumptionFingerprint(command identityapp.ConsumeLinkIntentCommand) string {
	r := command.Target.Reference()
	fields := []string{string(r.Kind), r.Scope, r.NormalizedValue, string(r.Assurance), r.Source, strconv.FormatInt(int64(r.NormalizerVersion), 10), command.Evidence.Type, string(command.Evidence.Strength), command.Evidence.Source, command.Evidence.EventID, command.Evidence.Digest, command.Evidence.PolicyVersion}
	h := sha256.New()
	for _, f := range fields {
		_, _ = h.Write([]byte(strconv.Itoa(len(f))))
		_, _ = h.Write([]byte{':'})
		_, _ = h.Write([]byte(f))
	}
	return base64.RawStdEncoding.EncodeToString(h.Sum(nil))
}
