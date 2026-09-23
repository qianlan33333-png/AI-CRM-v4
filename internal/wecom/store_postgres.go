package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQLFollowRelationshipStore struct{}

var (
	ErrInvalidFollowRelationship    = errors.New("invalid follow relationship event")
	ErrFollowRelationshipConflict   = errors.New("follow relationship event conflicts with durable state")
	ErrFollowRelationshipConcurrent = errors.New("follow relationship changed concurrently")
)

type followRelationshipEvent struct {
	CallbackID string
	CorpID     string
	EmployeeID string
	CustomerID customerdomain.CustomerID
	Active     bool
	OccurredAt time.Time
	Digest     [32]byte
}

type externalContactEventCursor struct {
	CallbackID             string
	CorpID                 string
	EmployeeID             string
	ExternalIdentityDigest [32]byte
	Active                 bool
	OccurredAt             time.Time
	Digest                 [32]byte
}

type followRelationshipApplyResult struct {
	Active         bool
	Version        int64
	LastEventAt    time.Time
	LastCallbackID string
	Applied        bool
	Replay         bool
	StaleIgnored   bool
}

var _ CallbackFollowRelationshipStore = (*PostgreSQLFollowRelationshipStore)(nil)

func NewPostgreSQLFollowRelationshipStore() *PostgreSQLFollowRelationshipStore {
	return &PostgreSQLFollowRelationshipStore{}
}

// AdmitExternalContactEvent serializes lifecycle events before OneID lookup.
// It persists deletes even when the external identity has no local customer,
// preventing a delayed older add from creating and activating a ghost customer.
// Equal provider timestamps use deletion-wins instead of callback hash order.
func (*PostgreSQLFollowRelationshipStore) AdmitExternalContactEvent(ctx context.Context, input CallbackExternalContactEvent) (ExternalContactEventAdmission, error) {
	event := externalContactEventCursor{
		CallbackID: input.CallbackID, CorpID: input.CorpID, EmployeeID: input.EmployeeID,
		ExternalIdentityDigest: input.ExternalIdentityDigest, Active: input.Active,
		OccurredAt: databaseTimestamp(input.OccurredAt),
	}
	event.Digest = externalContactEventDigest(event)
	if !validExternalContactEventCursor(event) {
		return ExternalContactEventAdmission{}, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return ExternalContactEventAdmission{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(
		'wecom.external.cursor:' || $1 || ':' || encode($2::bytea, 'hex') || ':' || $3, 0
	))`, event.CorpID, event.ExternalIdentityDigest[:], event.EmployeeID); err != nil {
		return ExternalContactEventAdmission{}, err
	}
	var currentActive bool
	var currentAt time.Time
	var currentCallbackID string
	var currentDigest []byte
	err = tx.QueryRow(ctx, `
		SELECT active, last_event_at, last_callback_id, last_event_digest
		FROM wecom_external_contact_event_cursors
		WHERE corp_id=$1 AND external_identity_digest=$2 AND employee_id=$3
		FOR UPDATE`, event.CorpID, event.ExternalIdentityDigest[:], event.EmployeeID,
	).Scan(&currentActive, &currentAt, &currentCallbackID, &currentDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
			INSERT INTO wecom_external_contact_event_cursors (
				corp_id, external_identity_digest, employee_id, active,
				last_event_at, last_callback_id, last_event_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7)`, event.CorpID, event.ExternalIdentityDigest[:],
			event.EmployeeID, event.Active, event.OccurredAt, event.CallbackID, event.Digest[:])
		if err != nil {
			return ExternalContactEventAdmission{}, err
		}
		return ExternalContactEventAdmission{Admitted: true, Advanced: true, Active: event.Active}, nil
	}
	if err != nil {
		return ExternalContactEventAdmission{}, err
	}
	if len(currentDigest) != sha256.Size {
		return ExternalContactEventAdmission{}, ErrFollowRelationshipConflict
	}
	if currentCallbackID == event.CallbackID {
		if !currentAt.UTC().Equal(event.OccurredAt) || currentActive != event.Active || !equalDigest(currentDigest, event.Digest) {
			return ExternalContactEventAdmission{}, ErrFollowRelationshipConflict
		}
		return ExternalContactEventAdmission{Admitted: false, Active: currentActive}, nil
	}
	switch compareFollowEvent(event.OccurredAt, event.Active, currentAt.UTC(), currentActive) {
	case followEventStale:
		return ExternalContactEventAdmission{Admitted: false, Active: currentActive}, nil
	case followEventEquivalent:
		// Same-second callbacks asserting the same state may carry an
		// independent entrant State, so admit without rewriting chronology.
		return ExternalContactEventAdmission{Admitted: true, Active: currentActive}, nil
	case followEventNewer:
		_, err = tx.Exec(ctx, `
			UPDATE wecom_external_contact_event_cursors
			SET active=$4, last_event_at=$5, last_callback_id=$6,
				last_event_digest=$7, version=version+1, updated_at=clock_timestamp()
			WHERE corp_id=$1 AND external_identity_digest=$2 AND employee_id=$3`,
			event.CorpID, event.ExternalIdentityDigest[:], event.EmployeeID, event.Active,
			event.OccurredAt, event.CallbackID, event.Digest[:])
		if err != nil {
			return ExternalContactEventAdmission{}, err
		}
		return ExternalContactEventAdmission{Admitted: true, Advanced: true, Active: event.Active}, nil
	default:
		return ExternalContactEventAdmission{}, ErrFollowRelationshipConflict
	}
}

// Upsert remains the compatibility method used by the existing port. Callers
// must provide the authenticated event time in UpdatedAt; zero time fails
// closed. New callback orchestration uses the identity cursor plus
// ApplyCallbackEvent and never treats CallbackID hashes as chronology.
func (store *PostgreSQLFollowRelationshipStore) Upsert(ctx context.Context, relationship FollowRelationship) error {
	if relationship.UpdatedAt.IsZero() {
		return ErrInvalidFollowRelationship
	}
	relationship.UpdatedAt = databaseTimestamp(relationship.UpdatedAt)
	digest := followRelationshipDigest(relationship)
	_, err := store.apply(ctx, followRelationshipEvent{
		CallbackID: "legacy:" + hex.EncodeToString(digest[:]),
		CorpID:     relationship.CorpID, EmployeeID: relationship.EmployeeID,
		CustomerID: relationship.CustomerID, Active: relationship.Active,
		OccurredAt: relationship.UpdatedAt, Digest: digest,
	})
	return err
}

// ApplyCallbackEvent is idempotent for an exact callback event and ignores an
// older event cursor. Equal cursors with different authenticated digests fail
// closed instead of guessing whether activation or deactivation is newer.
func (store *PostgreSQLFollowRelationshipStore) ApplyCallbackEvent(ctx context.Context, relationship CallbackFollowRelationship) (FollowRelationshipApplication, error) {
	event := followRelationshipEvent{
		CallbackID: relationship.CallbackID, CorpID: relationship.CorpID,
		EmployeeID: relationship.EmployeeID, CustomerID: relationship.CustomerID,
		Active: relationship.Active, OccurredAt: databaseTimestamp(relationship.OccurredAt),
	}
	event.Digest = callbackFollowRelationshipDigest(event)
	result, err := store.apply(ctx, event)
	if err != nil {
		return FollowRelationshipApplication{}, err
	}
	return FollowRelationshipApplication{Applied: result.Applied, Active: result.Active}, nil
}

func (*PostgreSQLFollowRelationshipStore) apply(ctx context.Context, event followRelationshipEvent) (followRelationshipApplyResult, error) {
	if !validCallbackFollowRelationshipEvent(event) {
		return followRelationshipApplyResult{}, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return followRelationshipApplyResult{}, err
	}
	if err = lockFollowCustomer(ctx, tx, event.CorpID, event.CustomerID); err != nil {
		return followRelationshipApplyResult{}, err
	}

	current, found, err := loadFollowRelationshipForUpdate(ctx, tx, event.CorpID, event.EmployeeID, event.CustomerID)
	if err != nil {
		return followRelationshipApplyResult{}, err
	}
	if !found {
		var result followRelationshipApplyResult
		err = tx.QueryRow(ctx, `
			INSERT INTO wecom_follow_relationships (
				corp_id, employee_id, customer_id, active, version,
				last_event_at, last_callback_id, last_event_digest
			) VALUES ($1, $2, $3, $4, 1, $5, $6, $7)
			RETURNING active, version, last_event_at, last_callback_id`,
			event.CorpID, event.EmployeeID, event.CustomerID, event.Active,
			event.OccurredAt, event.CallbackID, event.Digest[:],
		).Scan(&result.Active, &result.Version, &result.LastEventAt, &result.LastCallbackID)
		if err != nil {
			return followRelationshipApplyResult{}, err
		}
		result.LastEventAt = result.LastEventAt.UTC()
		result.Applied = true
		return result, nil
	}

	if current.EventDigest == event.Digest {
		if current.Active != event.Active || !current.LastEventAt.Equal(event.OccurredAt) || current.LastCallbackID != event.CallbackID {
			return followRelationshipApplyResult{}, ErrFollowRelationshipConflict
		}
		current.Replay = true
		return current.followRelationshipApplyResult, nil
	}
	if compareFollowEvent(event.OccurredAt, event.Active, current.LastEventAt, current.Active) != followEventNewer {
		if current.LastEventAt.Equal(event.OccurredAt) && current.LastCallbackID == event.CallbackID {
			return followRelationshipApplyResult{}, ErrFollowRelationshipConflict
		}
		current.StaleIgnored = true
		return current.followRelationshipApplyResult, nil
	}

	var result followRelationshipApplyResult
	err = tx.QueryRow(ctx, `
		UPDATE wecom_follow_relationships
		SET active = $4,
			version = version + 1,
			last_event_at = $5,
			last_callback_id = $6,
			last_event_digest = $7,
			updated_at = clock_timestamp()
		WHERE corp_id = $1 AND employee_id = $2 AND customer_id = $3 AND version = $8
		RETURNING active, version, last_event_at, last_callback_id`,
		event.CorpID, event.EmployeeID, event.CustomerID, event.Active,
		event.OccurredAt, event.CallbackID, event.Digest[:], current.Version,
	).Scan(&result.Active, &result.Version, &result.LastEventAt, &result.LastCallbackID)
	if errors.Is(err, pgx.ErrNoRows) {
		return followRelationshipApplyResult{}, ErrFollowRelationshipConcurrent
	}
	if err != nil {
		return followRelationshipApplyResult{}, err
	}
	result.LastEventAt = result.LastEventAt.UTC()
	result.Applied = true
	return result, nil
}

func (*PostgreSQLFollowRelationshipStore) IsActive(ctx context.Context, corpID, employeeID string, customerID customerdomain.CustomerID) (bool, error) {
	if !validFollowRelationshipKey(corpID, employeeID, customerID) {
		return false, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT active FROM wecom_follow_relationships WHERE corp_id=$1 AND employee_id=$2 AND customer_id=$3`, corpID, employeeID, customerID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, err
}

type followRelationshipRow struct {
	followRelationshipApplyResult
	EventDigest [32]byte
}

func loadFollowRelationshipForUpdate(ctx context.Context, tx pgx.Tx, corpID, employeeID string, customerID customerdomain.CustomerID) (followRelationshipRow, bool, error) {
	var row followRelationshipRow
	var lastEventAt *time.Time
	var lastCallbackID *string
	var eventDigest []byte
	err := tx.QueryRow(ctx, `
		SELECT active, version, last_event_at, last_callback_id, last_event_digest
		FROM wecom_follow_relationships
		WHERE corp_id = $1 AND employee_id = $2 AND customer_id = $3
		FOR UPDATE`, corpID, employeeID, customerID,
	).Scan(&row.Active, &row.Version, &lastEventAt, &lastCallbackID, &eventDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return followRelationshipRow{}, false, nil
	}
	if err != nil {
		return followRelationshipRow{}, false, err
	}
	if lastEventAt == nil || len(eventDigest) != len(row.EventDigest) {
		// A pre-migration row is a valid projection but has no ordering cursor.
		// Treat it as older than the first authenticated callback event.
		return row, true, nil
	}
	row.LastEventAt = lastEventAt.UTC()
	if lastCallbackID != nil {
		row.LastCallbackID = *lastCallbackID
	}
	copy(row.EventDigest[:], eventDigest)
	return row, true, nil
}

func lockFollowCustomer(ctx context.Context, tx pgx.Tx, corpID string, customerID customerdomain.CustomerID) error {
	_, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended(
			'wecom.follow.customer:' || $1 || ':' || $2::bigint::text, 0
		))`, corpID, customerID)
	return err
}

func validCallbackFollowRelationshipEvent(event followRelationshipEvent) bool {
	return validCallbackID(event.CallbackID) &&
		validFollowRelationshipKey(event.CorpID, event.EmployeeID, event.CustomerID) &&
		!event.OccurredAt.IsZero() && digestPresent(event.Digest)
}

func validExternalContactEventCursor(event externalContactEventCursor) bool {
	return validCallbackID(event.CallbackID) && validFollowText(event.CorpID, 256) &&
		validFollowText(event.EmployeeID, 1024) && event.ExternalIdentityDigest != ([32]byte{}) &&
		!event.OccurredAt.IsZero() && digestPresent(event.Digest)
}

func validFollowRelationshipKey(corpID, employeeID string, customerID customerdomain.CustomerID) bool {
	return validCorpCustomer(corpID, customerID) && validFollowText(employeeID, 1024)
}

func validCorpCustomer(corpID string, customerID customerdomain.CustomerID) bool {
	return validFollowText(corpID, 256) && customerID > 0
}

func validCallbackID(value string) bool { return validFollowText(value, 512) }

func validFollowText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

type followEventDecision uint8

const (
	followEventStale followEventDecision = iota
	followEventEquivalent
	followEventNewer
)

func compareFollowEvent(incomingAt time.Time, incomingActive bool, currentAt time.Time, currentActive bool) followEventDecision {
	if currentAt.IsZero() {
		return followEventNewer
	}
	if incomingAt.After(currentAt) {
		return followEventNewer
	}
	if incomingAt.Before(currentAt) {
		return followEventStale
	}
	if incomingActive == currentActive {
		return followEventEquivalent
	}
	if !incomingActive {
		return followEventNewer
	}
	return followEventStale
}

func equalDigest(value []byte, expected [32]byte) bool {
	return len(value) == len(expected) && string(value) == string(expected[:])
}

func followRelationshipDigest(relationship FollowRelationship) [32]byte {
	value := strings.Join([]string{
		relationship.CorpID,
		relationship.EmployeeID,
		strconv.FormatInt(int64(relationship.CustomerID), 10),
		strconv.FormatBool(relationship.Active),
		relationship.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	return sha256.Sum256([]byte(value))
}

func callbackFollowRelationshipDigest(event followRelationshipEvent) [32]byte {
	value := strings.Join([]string{
		"callback-follow-v1",
		event.CallbackID,
		event.CorpID,
		event.EmployeeID,
		strconv.FormatInt(int64(event.CustomerID), 10),
		strconv.FormatBool(event.Active),
		event.OccurredAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	return sha256.Sum256([]byte(value))
}

func externalContactEventDigest(event externalContactEventCursor) [32]byte {
	value := strings.Join([]string{
		"callback-external-contact-cursor-v1", event.CallbackID, event.CorpID,
		event.EmployeeID, hex.EncodeToString(event.ExternalIdentityDigest[:]),
		strconv.FormatBool(event.Active), event.OccurredAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	return sha256.Sum256([]byte(value))
}

func databaseTimestamp(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}

type OAuthState struct {
	Purpose   OAuthPurpose
	Redirect  string
	ExpiresAt time.Time
}

type OAuthStateStore interface {
	Create(context.Context, OAuthState, [32]byte, [32]byte) error
	Consume(context.Context, OAuthPurpose, [32]byte, [32]byte, time.Time) (OAuthState, error)
}

type PostgreSQLOAuthStateStore struct{}

func NewPostgreSQLOAuthStateStore() *PostgreSQLOAuthStateStore { return &PostgreSQLOAuthStateStore{} }

func (*PostgreSQLOAuthStateStore) Create(ctx context.Context, state OAuthState, digest, nonceDigest [32]byte) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wecom_oauth_states (purpose, state_digest, nonce_digest, redirect_path, expires_at) VALUES ($1, $2, $3, $4, $5)`, state.Purpose, digest[:], nonceDigest[:], state.Redirect, state.ExpiresAt)
	return err
}

func (*PostgreSQLOAuthStateStore) Consume(ctx context.Context, purpose OAuthPurpose, stateDigest, nonceDigest [32]byte, now time.Time) (OAuthState, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return OAuthState{}, err
	}
	var state OAuthState
	err = tx.QueryRow(ctx, `
		UPDATE wecom_oauth_states
		SET used_at = clock_timestamp()
		WHERE purpose = $1 AND state_digest = $2 AND nonce_digest = $3 AND used_at IS NULL AND expires_at >= $4
		RETURNING purpose, redirect_path, expires_at`, purpose, stateDigest[:], nonceDigest[:], now).Scan(&state.Purpose, &state.Redirect, &state.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthState{}, ErrInvalidOAuth
	}
	return state, err
}

func oauthDigest(state string) [32]byte { return sha256.Sum256([]byte(state)) }
