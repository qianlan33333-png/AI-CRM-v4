// Package channel owns local acquisition correlation and entrant receipts.
// Its PostgreSQL API accepts keyed digests only; raw callback State is never a
// field, parameter, column, or query result in this package.
package channel

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var (
	ErrInvalidStateBinding       = errors.New("invalid channel acquisition state binding")
	ErrStateBindingConflict      = errors.New("channel acquisition state binding conflicts with durable state")
	ErrStateBindingNotFound      = errors.New("channel acquisition state binding not found")
	ErrStateBindingConcurrent    = errors.New("channel acquisition state binding changed concurrently")
	ErrInvalidEntrantReceipt     = errors.New("invalid channel acquisition entrant receipt")
	ErrEntrantReceiptConflict    = errors.New("channel acquisition entrant receipt conflicts with durable fact")
	ErrEntrantReceiptNotFound    = errors.New("channel acquisition entrant receipt not found")
	ErrEntrantReconcileForbidden = errors.New("channel acquisition entrant receipt cannot be reconciled")
)

type AcquisitionAssetKind string

const (
	AcquisitionAssetQRCode AcquisitionAssetKind = "contact_way_qrcode"
	AcquisitionAssetLink   AcquisitionAssetKind = "customer_acquisition_link"
)

type StateBinding struct {
	ID               int64
	CorpID           string
	DigestKeyVersion int16
	StateDigest      [32]byte
	ChannelID        int64
	AssetKind        AcquisitionAssetKind
	AssetVersion     int64
	BindingDigest    [32]byte
	ActiveFrom       time.Time
	ActiveUntil      *time.Time
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type StateDigestCardinality string

const (
	StateDigestZero     StateDigestCardinality = "zero"
	StateDigestOne      StateDigestCardinality = "one"
	StateDigestMultiple StateDigestCardinality = "multiple"
)

type StateDigestResolution struct {
	Cardinality StateDigestCardinality
	Match       StateBinding
}

type EndStateBinding struct {
	BindingID       int64
	ExpectedVersion int64
	ActiveUntil     time.Time
}

type PostgreSQLStore struct{}

var (
	_ channelport.StateResolver          = (*PostgreSQLStore)(nil)
	_ channelport.EntrantReceiptRecorder = (*PostgreSQLStore)(nil)
)

func NewPostgreSQLStore() *PostgreSQLStore { return &PostgreSQLStore{} }

// PutBinding stores a keyed digest only. The unique asset version makes an
// exact retry deterministic, while intentionally permitting two different
// assets to share an overlapping digest so ResolveStateDigest can fail closed
// with cardinality=multiple.
func (*PostgreSQLStore) PutBinding(ctx context.Context, binding StateBinding) (StateBinding, bool, error) {
	binding.ActiveFrom = databaseTime(binding.ActiveFrom)
	if binding.ActiveUntil != nil {
		value := databaseTime(*binding.ActiveUntil)
		binding.ActiveUntil = &value
	}
	if !validStateBinding(binding) {
		return StateBinding{}, false, ErrInvalidStateBinding
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return StateBinding{}, false, err
	}
	// The asset tuple is also unique. Lock it before the digest so two
	// concurrent registrations cannot surface a driver-specific unique error
	// when they disagree about the digest for one immutable asset version.
	if err = lockStateAsset(ctx, tx, binding.ChannelID, binding.AssetKind, binding.AssetVersion); err != nil {
		return StateBinding{}, false, err
	}
	if err = lockStateDigest(ctx, tx, binding.CorpID, binding.DigestKeyVersion, binding.StateDigest); err != nil {
		return StateBinding{}, false, err
	}

	existing, err := scanStateBinding(tx.QueryRow(ctx, stateBindingByAssetForUpdateSQL, binding.ChannelID, binding.AssetKind, binding.AssetVersion))
	if err == nil {
		if !sameStateBinding(existing, binding) {
			return StateBinding{}, false, ErrStateBindingConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return StateBinding{}, false, err
	}

	existing, err = scanStateBinding(tx.QueryRow(ctx, `
		INSERT INTO channel_acquisition_state_bindings (
			corp_id, digest_key_version, state_digest, channel_id,
			asset_kind, asset_version, binding_digest, active_from,
			active_until
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+stateBindingColumns,
		binding.CorpID, binding.DigestKeyVersion, binding.StateDigest[:],
		binding.ChannelID, binding.AssetKind, binding.AssetVersion,
		binding.BindingDigest[:], binding.ActiveFrom, binding.ActiveUntil,
	))
	if err != nil {
		return StateBinding{}, false, err
	}
	return existing, true, nil
}

// ResolveStateDigest takes no raw State. The advisory transaction lock is also
// taken by binding writes, so a one/multiple decision remains stable until the
// caller records its entrant receipt and commits the surrounding UnitOfWork.
func (store *PostgreSQLStore) ResolveStateDigest(ctx context.Context, corpID string, digest [32]byte, occurredAt time.Time) (channeldomain.StateResolution, error) {
	if store == nil {
		return channeldomain.StateResolution{}, ErrInvalidStateBinding
	}
	resolution, err := store.ResolveStateDigestAt(ctx, corpID, digest, 1, occurredAt)
	if err != nil {
		return channeldomain.StateResolution{}, err
	}
	switch resolution.Cardinality {
	case StateDigestZero:
		return channeldomain.StateResolution{Status: channeldomain.StateUnmatched}, nil
	case StateDigestMultiple:
		return channeldomain.StateResolution{Status: channeldomain.StateAmbiguous}, nil
	case StateDigestOne:
		kind, mapErr := domainAssetKind(resolution.Match.AssetKind)
		if mapErr != nil {
			return channeldomain.StateResolution{}, mapErr
		}
		return channeldomain.StateResolution{
			Status: channeldomain.StateAttributed,
			Asset: channeldomain.AcquisitionAsset{
				ChannelID: resolution.Match.ChannelID, Kind: kind,
				AssetVersion: resolution.Match.AssetVersion,
			},
		}, nil
	default:
		return channeldomain.StateResolution{}, ErrStateBindingConflict
	}
}

// ResolveStateDigestAt is the persistence-level key-version variant. The v1
// lifecycle port fixes the HMAC key version at 1 while always supplying the
// authenticated callback occurrence time.
func (*PostgreSQLStore) ResolveStateDigestAt(ctx context.Context, corpID string, digest [32]byte, digestKeyVersion int16, occurredAt time.Time) (StateDigestResolution, error) {
	occurredAt = databaseTime(occurredAt)
	if !validCorpID(corpID) || !digestPresent(digest) || digestKeyVersion < 1 || occurredAt.IsZero() {
		return StateDigestResolution{}, ErrInvalidStateBinding
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return StateDigestResolution{}, err
	}
	if err = lockStateDigest(ctx, tx, corpID, digestKeyVersion, digest); err != nil {
		return StateDigestResolution{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT `+stateBindingColumns+`
		FROM channel_acquisition_state_bindings
		WHERE corp_id = $1 AND digest_key_version = $2 AND state_digest = $3
		  AND active_from <= $4
		  AND (active_until IS NULL OR $4 < active_until)
		ORDER BY id
		LIMIT 2
		FOR SHARE`, corpID, digestKeyVersion, digest[:], occurredAt)
	if err != nil {
		return StateDigestResolution{}, err
	}
	defer rows.Close()
	matches := make([]StateBinding, 0, 2)
	for rows.Next() {
		binding, scanErr := scanStateBinding(rows)
		if scanErr != nil {
			return StateDigestResolution{}, scanErr
		}
		matches = append(matches, binding)
	}
	if err = rows.Err(); err != nil {
		return StateDigestResolution{}, err
	}
	switch len(matches) {
	case 0:
		return StateDigestResolution{Cardinality: StateDigestZero}, nil
	case 1:
		return StateDigestResolution{Cardinality: StateDigestOne, Match: matches[0]}, nil
	default:
		return StateDigestResolution{Cardinality: StateDigestMultiple}, nil
	}
}

// EndBinding uses version CAS and the same digest lock as ResolveStateDigest.
// Historical events before ActiveUntil continue to resolve to the binding.
func (*PostgreSQLStore) EndBinding(ctx context.Context, command EndStateBinding) (StateBinding, bool, error) {
	command.ActiveUntil = databaseTime(command.ActiveUntil)
	if command.BindingID < 1 || command.ExpectedVersion < 1 || command.ActiveUntil.IsZero() {
		return StateBinding{}, false, ErrInvalidStateBinding
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return StateBinding{}, false, err
	}
	initial, err := scanStateBinding(tx.QueryRow(ctx, stateBindingByIDSQL, command.BindingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return StateBinding{}, false, ErrStateBindingNotFound
	}
	if err != nil {
		return StateBinding{}, false, err
	}
	if err = lockStateDigest(ctx, tx, initial.CorpID, initial.DigestKeyVersion, initial.StateDigest); err != nil {
		return StateBinding{}, false, err
	}
	current, err := scanStateBinding(tx.QueryRow(ctx, stateBindingByIDForUpdateSQL, command.BindingID))
	if err != nil {
		return StateBinding{}, false, err
	}
	if current.ActiveUntil != nil {
		if current.Version == command.ExpectedVersion+1 && current.ActiveUntil.Equal(command.ActiveUntil) {
			return current, false, nil
		}
		return StateBinding{}, false, ErrStateBindingConflict
	}
	if current.Version != command.ExpectedVersion || !command.ActiveUntil.After(current.ActiveFrom) {
		return StateBinding{}, false, ErrStateBindingConflict
	}
	usedAfterEnd, err := bindingUsedAtOrAfter(ctx, tx, command.BindingID, command.ActiveUntil)
	if err != nil {
		return StateBinding{}, false, err
	}
	if usedAfterEnd {
		// An append-only entrant fact has already proved this binding valid at
		// or after the requested exclusive end. Do not rewrite that history.
		return StateBinding{}, false, ErrStateBindingConflict
	}
	updated, err := scanStateBinding(tx.QueryRow(ctx, `
		UPDATE channel_acquisition_state_bindings
		SET active_until = $2, version = version + 1,
			updated_at = clock_timestamp()
		WHERE id = $1 AND version = $3 AND active_until IS NULL
		RETURNING `+stateBindingColumns,
		command.BindingID, command.ActiveUntil, command.ExpectedVersion,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return StateBinding{}, false, ErrStateBindingConcurrent
	}
	if err != nil {
		return StateBinding{}, false, err
	}
	return updated, true, nil
}

// EndBindingForAssetID is the Channel-internal bridge used when an executed
// Provider mutation supersedes or deletes a published acquisition asset. The
// binding CAS and asset completion remain in the caller's current UoW.
func (store *PostgreSQLStore) EndBindingForAssetID(ctx context.Context, assetID int64, activeUntil time.Time) error {
	if store == nil || assetID < 1 || activeUntil.IsZero() {
		return ErrInvalidStateBinding
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	binding, err := scanStateBinding(tx.QueryRow(ctx, `SELECT b.id,b.corp_id,b.digest_key_version,b.state_digest,b.channel_id,b.asset_kind,b.asset_version,b.binding_digest,b.active_from,b.active_until,b.version,b.created_at,b.updated_at FROM channel_acquisition_state_bindings b JOIN channel_acquisition_assets a ON a.channel_id=b.channel_id AND a.kind=b.asset_kind AND a.asset_version=b.asset_version WHERE a.id=$1`, assetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if binding.ActiveUntil != nil {
		return nil
	}
	_, _, err = store.EndBinding(ctx, EndStateBinding{BindingID: binding.ID, ExpectedVersion: binding.Version, ActiveUntil: activeUntil})
	return err
}

const stateBindingColumns = `id, corp_id, digest_key_version, state_digest,
	channel_id, asset_kind, asset_version, binding_digest, active_from,
	active_until, version, created_at, updated_at`

const stateBindingByAssetForUpdateSQL = `
	SELECT ` + stateBindingColumns + `
	FROM channel_acquisition_state_bindings
	WHERE channel_id = $1 AND asset_kind = $2 AND asset_version = $3
	FOR UPDATE`

const stateBindingByIDSQL = `
	SELECT ` + stateBindingColumns + `
	FROM channel_acquisition_state_bindings
	WHERE id = $1`

const stateBindingByIDForUpdateSQL = stateBindingByIDSQL + ` FOR UPDATE`

type stateBindingScanner interface{ Scan(...any) error }

func scanStateBinding(scanner stateBindingScanner) (StateBinding, error) {
	var binding StateBinding
	var stateDigest, bindingDigest []byte
	var assetKind string
	err := scanner.Scan(
		&binding.ID, &binding.CorpID, &binding.DigestKeyVersion, &stateDigest,
		&binding.ChannelID, &assetKind, &binding.AssetVersion, &bindingDigest,
		&binding.ActiveFrom, &binding.ActiveUntil, &binding.Version,
		&binding.CreatedAt, &binding.UpdatedAt,
	)
	if err != nil {
		return StateBinding{}, err
	}
	if len(stateDigest) != len(binding.StateDigest) || len(bindingDigest) != len(binding.BindingDigest) {
		return StateBinding{}, ErrStateBindingConflict
	}
	copy(binding.StateDigest[:], stateDigest)
	copy(binding.BindingDigest[:], bindingDigest)
	binding.AssetKind = AcquisitionAssetKind(assetKind)
	binding.ActiveFrom = binding.ActiveFrom.UTC()
	if binding.ActiveUntil != nil {
		value := binding.ActiveUntil.UTC()
		binding.ActiveUntil = &value
	}
	binding.CreatedAt = binding.CreatedAt.UTC()
	binding.UpdatedAt = binding.UpdatedAt.UTC()
	return binding, nil
}

func lockStateDigest(ctx context.Context, tx pgx.Tx, corpID string, keyVersion int16, digest [32]byte) error {
	_, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended(
			'channel.state-digest:' || $1 || ':' || $2::smallint::text || ':' || encode($3::bytea, 'hex'), 0
		))`, corpID, keyVersion, digest[:])
	return err
}

func lockStateAsset(ctx context.Context, tx pgx.Tx, channelID int64, kind AcquisitionAssetKind, assetVersion int64) error {
	_, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended(
			'channel.state-asset:' || $1::bigint::text || ':' || $2 || ':' || $3::bigint::text, 0
		))`, channelID, kind, assetVersion)
	return err
}

func sameStateBinding(left, right StateBinding) bool {
	return left.CorpID == right.CorpID && left.DigestKeyVersion == right.DigestKeyVersion &&
		left.StateDigest == right.StateDigest && left.ChannelID == right.ChannelID &&
		left.AssetKind == right.AssetKind && left.AssetVersion == right.AssetVersion &&
		left.BindingDigest == right.BindingDigest && left.ActiveFrom.Equal(right.ActiveFrom) &&
		sameOptionalTime(left.ActiveUntil, right.ActiveUntil)
}

func validStateBinding(binding StateBinding) bool {
	return validCorpID(binding.CorpID) && binding.DigestKeyVersion > 0 &&
		digestPresent(binding.StateDigest) && binding.ChannelID > 0 &&
		(binding.AssetKind == AcquisitionAssetQRCode || binding.AssetKind == AcquisitionAssetLink) &&
		binding.AssetVersion > 0 && digestPresent(binding.BindingDigest) &&
		!binding.ActiveFrom.IsZero() && (binding.ActiveUntil == nil || binding.ActiveUntil.After(binding.ActiveFrom))
}

type EntrantStatus string

const (
	EntrantChannelAttributed EntrantStatus = "channel_attributed"
	EntrantChannelUnmatched  EntrantStatus = "channel_unmatched"
	EntrantChannelAmbiguous  EntrantStatus = "channel_ambiguous"
	EntrantIdentityConflict  EntrantStatus = "identity_conflict"
	EntrantIgnored           EntrantStatus = "ignored"
	EntrantReconciled        EntrantStatus = "reconciled"
)

// EntrantReceipt is the safe admin projection. It excludes CorpID, input and
// command digests, State digest/key version, operation key, and free-form
// reconciliation reason.
type EntrantReceipt struct {
	ID           int64
	InboxID      int64
	ChangeType   string
	Status       EntrantStatus
	PriorStatus  EntrantStatus
	BindingID    int64
	ChannelID    int64
	AssetKind    AcquisitionAssetKind
	AssetVersion int64
	CustomerID   customerdomain.CustomerID
	OccurredAt   time.Time
	ReconciledAt *time.Time
	CreatedAt    time.Time
}

type AppendEntrantReceipt struct {
	CallbackID    string
	InboxID       int64
	CorpID        string
	InputDigest   [32]byte
	CommandDigest [32]byte
	ChangeType    string
	Status        EntrantStatus
	BindingID     int64
	CustomerID    customerdomain.CustomerID
	OccurredAt    time.Time
}

type EntrantReceiptPage struct {
	BeforeID int64
	Limit    int
}

type ReconcileEntrantReceipt struct {
	ReceiptID          int64
	ExpectedStatus     EntrantStatus
	BindingID          int64
	CustomerID         customerdomain.CustomerID
	ActorAdminUserID   int64
	Reason             string
	OperationKeyDigest [32]byte
	CommandDigest      [32]byte
	ReconciledAt       time.Time
}

// RecordEntrantReceipt implements the lifecycle port without accepting raw
// State. CallbackID is the safe durable Inbox key. The content digests below
// cover only typed local IDs and the already-safe correlation resolution.
func (store *PostgreSQLStore) RecordEntrantReceipt(ctx context.Context, receipt channelport.EntrantReceipt) error {
	if store == nil || !receipt.Valid() || !validCallbackID(receipt.CallbackID) ||
		!validCorpID(receipt.CorpID) {
		return ErrInvalidEntrantReceipt
	}
	receipt.OccurredAt = databaseTime(receipt.OccurredAt)
	command := AppendEntrantReceipt{
		CallbackID: receipt.CallbackID, InboxID: receipt.InboxID,
		CorpID: receipt.CorpID, ChangeType: receipt.ChangeType,
		CustomerID: receipt.CustomerID, OccurredAt: receipt.OccurredAt,
	}
	switch receipt.Status {
	case channelport.EntrantReceiptAttributed:
		command.Status = EntrantChannelAttributed
		tx, err := platformpostgres.RequireTransaction(ctx)
		if err != nil {
			return err
		}
		kind, err := persistenceAssetKind(receipt.Resolution.Asset.Kind)
		if err != nil {
			return err
		}
		binding, err := bindingByAssetForEntrant(
			ctx, tx, receipt.CorpID, receipt.Resolution.Asset.ChannelID,
			kind, receipt.Resolution.Asset.AssetVersion, receipt.OccurredAt,
		)
		if err != nil {
			return err
		}
		command.BindingID = binding.ID
	case channelport.EntrantReceiptUnmatched:
		command.Status = EntrantChannelUnmatched
	case channelport.EntrantReceiptAmbiguous:
		command.Status = EntrantChannelAmbiguous
	case channelport.EntrantReceiptIdentityConflict:
		command.Status = EntrantIdentityConflict
	default:
		return ErrInvalidEntrantReceipt
	}
	command.InputDigest = entrantPortDigest("input-v1", receipt)
	command.CommandDigest = entrantPortDigest("command-v1", receipt)
	_, _, err := store.PutEntrant(ctx, command)
	return err
}

func (*PostgreSQLStore) PutEntrant(ctx context.Context, command AppendEntrantReceipt) (EntrantReceipt, bool, error) {
	command.OccurredAt = databaseTime(command.OccurredAt)
	if !validEntrantCommand(command) {
		return EntrantReceipt{}, false, ErrInvalidEntrantReceipt
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return EntrantReceipt{}, false, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('channel.entrant:' || $1, 0))`, command.CallbackID); err != nil {
		return EntrantReceipt{}, false, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('channel.entrant.inbox:' || $1::bigint::text, 0))`, command.InboxID); err != nil {
		return EntrantReceipt{}, false, err
	}
	var inboxProvider, inboxKey string
	err = tx.QueryRow(ctx, `SELECT provider, idempotency_key FROM webhook_inbox WHERE id = $1 FOR SHARE`, command.InboxID).Scan(&inboxProvider, &inboxKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return EntrantReceipt{}, false, ErrEntrantReceiptNotFound
	}
	if err != nil {
		return EntrantReceipt{}, false, err
	}
	if inboxProvider != "wecom.external_contact" || inboxKey != command.CallbackID {
		return EntrantReceipt{}, false, ErrEntrantReceiptConflict
	}
	existing, err := scanEntrantFact(tx.QueryRow(ctx, entrantFactByCallbackForUpdateSQL, command.CallbackID))
	if err == nil {
		if !sameEntrantFact(existing, command) {
			return EntrantReceipt{}, false, ErrEntrantReceiptConflict
		}
		receipt, getErr := getEntrant(ctx, tx, existing.ID)
		return receipt, false, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return EntrantReceipt{}, false, err
	}
	// CallbackID is the primary idempotency key. A second CallbackID for the
	// same Inbox is always command drift, never another entrant.
	if _, inboxErr := scanEntrantFact(tx.QueryRow(ctx, entrantFactByInboxForUpdateSQL, command.InboxID)); inboxErr == nil {
		return EntrantReceipt{}, false, ErrEntrantReceiptConflict
	} else if !errors.Is(inboxErr, pgx.ErrNoRows) {
		return EntrantReceipt{}, false, inboxErr
	}
	if command.BindingID > 0 {
		if _, err = bindingForEntrant(ctx, tx, command.BindingID, command.CorpID, command.OccurredAt); err != nil {
			return EntrantReceipt{}, false, err
		}
	}
	var bindingID, customerID *int64
	if command.BindingID > 0 {
		bindingID = &command.BindingID
	}
	if command.CustomerID > 0 {
		value := int64(command.CustomerID)
		customerID = &value
	}
	var receiptID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO channel_acquisition_entrant_receipts (
			callback_id, inbox_id, corp_id, input_digest, command_digest, change_type,
			status, binding_id, customer_id, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`, command.CallbackID, command.InboxID, command.CorpID,
		command.InputDigest[:], command.CommandDigest[:], command.ChangeType,
		command.Status, bindingID, customerID, command.OccurredAt,
	).Scan(&receiptID)
	if err != nil {
		return EntrantReceipt{}, false, err
	}
	receipt, err := getEntrant(ctx, tx, receiptID)
	return receipt, true, err
}

func (*PostgreSQLStore) GetEntrant(ctx context.Context, receiptID int64) (EntrantReceipt, error) {
	if receiptID < 1 {
		return EntrantReceipt{}, ErrInvalidEntrantReceipt
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return EntrantReceipt{}, err
	}
	return getEntrant(ctx, tx, receiptID)
}

func (*PostgreSQLStore) ListUnassigned(ctx context.Context, page EntrantReceiptPage) ([]EntrantReceipt, error) {
	if page.BeforeID < 0 || page.Limit < 1 || page.Limit > 100 {
		return nil, ErrInvalidEntrantReceipt
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, entrantSafeSelectSQL+`
		WHERE entrant.status IN ('channel_unmatched', 'channel_ambiguous', 'identity_conflict')
		  AND reconciliation.id IS NULL
		  AND ($1::bigint = 0 OR entrant.id < $1)
		ORDER BY entrant.id DESC
		LIMIT $2`, page.BeforeID, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	receipts := make([]EntrantReceipt, 0, page.Limit)
	for rows.Next() {
		receipt, scanErr := scanEntrantSafe(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}

// Reconcile appends a before/after receipt; it never updates the original
// entrant fact. SuperAdmin/session/CSRF authorization remains an application
// boundary. The Store enforces exact expected status and idempotency-key drift.
func (*PostgreSQLStore) Reconcile(ctx context.Context, command ReconcileEntrantReceipt) (EntrantReceipt, bool, error) {
	command.ReconciledAt = databaseTime(command.ReconciledAt)
	if !validReconcileCommand(command) {
		return EntrantReceipt{}, false, ErrInvalidEntrantReceipt
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return EntrantReceipt{}, false, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('channel.entrant.reconcile:' || $1::bigint::text || ':' || encode($2::bytea, 'hex'), 0))`, command.ActorAdminUserID, command.OperationKeyDigest[:]); err != nil {
		return EntrantReceipt{}, false, err
	}
	existing, err := scanReconciliationFact(tx.QueryRow(ctx, reconciliationByKeyForUpdateSQL, command.ActorAdminUserID, command.OperationKeyDigest[:]))
	if err == nil {
		if !sameReconciliation(existing, command) {
			return EntrantReceipt{}, false, ErrEntrantReceiptConflict
		}
		receipt, getErr := getEntrant(ctx, tx, existing.EntrantReceiptID)
		return receipt, false, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return EntrantReceipt{}, false, err
	}
	entrant, err := scanEntrantFact(tx.QueryRow(ctx, entrantFactByIDForUpdateSQL, command.ReceiptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return EntrantReceipt{}, false, ErrEntrantReceiptNotFound
	}
	if err != nil {
		return EntrantReceipt{}, false, err
	}
	if entrant.Status != command.ExpectedStatus || !reconcilableEntrantStatus(entrant.Status) {
		return EntrantReceipt{}, false, ErrEntrantReconcileForbidden
	}
	if entrant.CustomerID > 0 && entrant.CustomerID != command.CustomerID {
		return EntrantReceipt{}, false, ErrEntrantReceiptConflict
	}
	var priorReconciliationID int64
	err = tx.QueryRow(ctx, `SELECT id FROM channel_acquisition_entrant_reconciliation_receipts WHERE entrant_receipt_id = $1 FOR UPDATE`, command.ReceiptID).Scan(&priorReconciliationID)
	if err == nil {
		return EntrantReceipt{}, false, ErrEntrantReconcileForbidden
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return EntrantReceipt{}, false, err
	}
	if _, err = bindingForEntrant(ctx, tx, command.BindingID, entrant.CorpID, entrant.OccurredAt); err != nil {
		return EntrantReceipt{}, false, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO channel_acquisition_entrant_reconciliation_receipts (
			entrant_receipt_id, actor_admin_user_id, operation_key_digest,
			command_digest, prior_status, binding_id, customer_id, reason,
			reconciled_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		command.ReceiptID, command.ActorAdminUserID, command.OperationKeyDigest[:],
		command.CommandDigest[:], command.ExpectedStatus, command.BindingID,
		command.CustomerID, command.Reason, command.ReconciledAt,
	)
	if err != nil {
		return EntrantReceipt{}, false, err
	}
	receipt, err := getEntrant(ctx, tx, command.ReceiptID)
	return receipt, true, err
}

const entrantFactColumns = `id, callback_id, inbox_id, corp_id, input_digest, command_digest,
	change_type, status, binding_id, customer_id, occurred_at, created_at`

const entrantFactByCallbackForUpdateSQL = `
	SELECT ` + entrantFactColumns + `
	FROM channel_acquisition_entrant_receipts
	WHERE callback_id = $1
	FOR UPDATE`

const entrantFactByIDForUpdateSQL = `
	SELECT ` + entrantFactColumns + `
	FROM channel_acquisition_entrant_receipts
	WHERE id = $1
	FOR UPDATE`

const entrantFactByInboxForUpdateSQL = `
	SELECT ` + entrantFactColumns + `
	FROM channel_acquisition_entrant_receipts
	WHERE inbox_id = $1
	FOR UPDATE`

const entrantSafeSelectSQL = `
	SELECT entrant.id, COALESCE(entrant.inbox_id, 0), entrant.change_type,
		CASE WHEN reconciliation.id IS NULL THEN entrant.status ELSE reconciliation.resulting_status END,
		entrant.status,
		COALESCE(reconciliation.binding_id, entrant.binding_id),
		COALESCE(reconciled_binding.channel_id, original_binding.channel_id),
		COALESCE(reconciled_binding.asset_kind, original_binding.asset_kind),
		COALESCE(reconciled_binding.asset_version, original_binding.asset_version),
		COALESCE(reconciliation.customer_id, entrant.customer_id),
		entrant.occurred_at, reconciliation.reconciled_at, entrant.created_at
	FROM channel_acquisition_entrant_receipts AS entrant
	LEFT JOIN channel_acquisition_entrant_reconciliation_receipts AS reconciliation
		ON reconciliation.entrant_receipt_id = entrant.id
	LEFT JOIN channel_acquisition_state_bindings AS original_binding
		ON original_binding.id = entrant.binding_id
	LEFT JOIN channel_acquisition_state_bindings AS reconciled_binding
		ON reconciled_binding.id = reconciliation.binding_id`

const reconciliationColumns = `id, entrant_receipt_id, actor_admin_user_id,
	operation_key_digest, command_digest, prior_status, resulting_status,
	binding_id, customer_id, reason, reconciled_at, created_at`

const reconciliationByKeyForUpdateSQL = `
	SELECT ` + reconciliationColumns + `
	FROM channel_acquisition_entrant_reconciliation_receipts
	WHERE actor_admin_user_id = $1 AND operation_key_digest = $2
	FOR UPDATE`

type entrantFact struct {
	ID            int64
	CallbackID    string
	InboxID       int64
	CorpID        string
	InputDigest   [32]byte
	CommandDigest [32]byte
	ChangeType    string
	Status        EntrantStatus
	BindingID     int64
	CustomerID    customerdomain.CustomerID
	OccurredAt    time.Time
	CreatedAt     time.Time
}

type entrantScanner interface{ Scan(...any) error }

func scanEntrantFact(scanner entrantScanner) (entrantFact, error) {
	var fact entrantFact
	var inputDigest, commandDigest []byte
	var status string
	var inboxID, bindingID, customerID *int64
	err := scanner.Scan(
		&fact.ID, &fact.CallbackID, &inboxID, &fact.CorpID, &inputDigest, &commandDigest,
		&fact.ChangeType, &status, &bindingID, &customerID,
		&fact.OccurredAt, &fact.CreatedAt,
	)
	if err != nil {
		return entrantFact{}, err
	}
	if len(inputDigest) != len(fact.InputDigest) || len(commandDigest) != len(fact.CommandDigest) {
		return entrantFact{}, ErrEntrantReceiptConflict
	}
	copy(fact.InputDigest[:], inputDigest)
	copy(fact.CommandDigest[:], commandDigest)
	fact.Status = EntrantStatus(status)
	if inboxID != nil {
		fact.InboxID = *inboxID
	}
	if bindingID != nil {
		fact.BindingID = *bindingID
	}
	if customerID != nil {
		fact.CustomerID = customerdomain.CustomerID(*customerID)
	}
	fact.OccurredAt = fact.OccurredAt.UTC()
	fact.CreatedAt = fact.CreatedAt.UTC()
	return fact, nil
}

func getEntrant(ctx context.Context, tx pgx.Tx, receiptID int64) (EntrantReceipt, error) {
	receipt, err := scanEntrantSafe(tx.QueryRow(ctx, entrantSafeSelectSQL+` WHERE entrant.id = $1`, receiptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return EntrantReceipt{}, ErrEntrantReceiptNotFound
	}
	return receipt, err
}

func scanEntrantSafe(scanner entrantScanner) (EntrantReceipt, error) {
	var receipt EntrantReceipt
	var status, priorStatus string
	var bindingID, channelID, assetVersion, customerID *int64
	var assetKind *string
	err := scanner.Scan(
		&receipt.ID, &receipt.InboxID, &receipt.ChangeType, &status, &priorStatus,
		&bindingID, &channelID, &assetKind, &assetVersion, &customerID,
		&receipt.OccurredAt, &receipt.ReconciledAt, &receipt.CreatedAt,
	)
	if err != nil {
		return EntrantReceipt{}, err
	}
	receipt.Status = EntrantStatus(status)
	receipt.PriorStatus = EntrantStatus(priorStatus)
	if bindingID != nil {
		receipt.BindingID = *bindingID
	}
	if channelID != nil {
		receipt.ChannelID = *channelID
	}
	if assetKind != nil {
		receipt.AssetKind = AcquisitionAssetKind(*assetKind)
	}
	if assetVersion != nil {
		receipt.AssetVersion = *assetVersion
	}
	if customerID != nil {
		receipt.CustomerID = customerdomain.CustomerID(*customerID)
	}
	receipt.OccurredAt = receipt.OccurredAt.UTC()
	receipt.CreatedAt = receipt.CreatedAt.UTC()
	if receipt.ReconciledAt != nil {
		value := receipt.ReconciledAt.UTC()
		receipt.ReconciledAt = &value
	}
	return receipt, nil
}

type reconciliationFact struct {
	ID                 int64
	EntrantReceiptID   int64
	ActorAdminUserID   int64
	OperationKeyDigest [32]byte
	CommandDigest      [32]byte
	PriorStatus        EntrantStatus
	ResultingStatus    EntrantStatus
	BindingID          int64
	CustomerID         customerdomain.CustomerID
	Reason             string
	ReconciledAt       time.Time
	CreatedAt          time.Time
}

func scanReconciliationFact(scanner entrantScanner) (reconciliationFact, error) {
	var fact reconciliationFact
	var operationKeyDigest, commandDigest []byte
	var priorStatus, resultingStatus string
	err := scanner.Scan(
		&fact.ID, &fact.EntrantReceiptID, &fact.ActorAdminUserID,
		&operationKeyDigest, &commandDigest, &priorStatus, &resultingStatus,
		&fact.BindingID, &fact.CustomerID, &fact.Reason,
		&fact.ReconciledAt, &fact.CreatedAt,
	)
	if err != nil {
		return reconciliationFact{}, err
	}
	if len(operationKeyDigest) != len(fact.OperationKeyDigest) || len(commandDigest) != len(fact.CommandDigest) {
		return reconciliationFact{}, ErrEntrantReceiptConflict
	}
	copy(fact.OperationKeyDigest[:], operationKeyDigest)
	copy(fact.CommandDigest[:], commandDigest)
	fact.PriorStatus = EntrantStatus(priorStatus)
	fact.ResultingStatus = EntrantStatus(resultingStatus)
	fact.ReconciledAt = fact.ReconciledAt.UTC()
	fact.CreatedAt = fact.CreatedAt.UTC()
	return fact, nil
}

func bindingForEntrant(ctx context.Context, tx pgx.Tx, bindingID int64, corpID string, occurredAt time.Time) (StateBinding, error) {
	binding, err := scanStateBinding(tx.QueryRow(ctx, `
		SELECT `+stateBindingColumns+`
		FROM channel_acquisition_state_bindings
		WHERE id = $1 AND corp_id = $2 AND active_from <= $3
		  AND (active_until IS NULL OR $3 < active_until)
		FOR SHARE`, bindingID, corpID, occurredAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return StateBinding{}, ErrStateBindingConflict
	}
	return binding, err
}

func bindingByAssetForEntrant(ctx context.Context, tx pgx.Tx, corpID string, channelID int64, kind AcquisitionAssetKind, assetVersion int64, occurredAt time.Time) (StateBinding, error) {
	binding, err := scanStateBinding(tx.QueryRow(ctx, `
		SELECT `+stateBindingColumns+`
		FROM channel_acquisition_state_bindings
		WHERE corp_id = $1 AND channel_id = $2 AND asset_kind = $3
		  AND asset_version = $4 AND active_from <= $5
		  AND (active_until IS NULL OR $5 < active_until)
		FOR SHARE`, corpID, channelID, kind, assetVersion, occurredAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return StateBinding{}, ErrStateBindingConflict
	}
	return binding, err
}

func bindingUsedAtOrAfter(ctx context.Context, tx pgx.Tx, bindingID int64, activeUntil time.Time) (bool, error) {
	var used bool
	err := tx.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				FROM channel_acquisition_entrant_receipts
				WHERE binding_id = $1 AND occurred_at >= $2
			)
			OR EXISTS (
				SELECT 1
				FROM channel_acquisition_entrant_reconciliation_receipts AS reconciliation
				JOIN channel_acquisition_entrant_receipts AS entrant
					ON entrant.id = reconciliation.entrant_receipt_id
				WHERE reconciliation.binding_id = $1 AND entrant.occurred_at >= $2
			)`, bindingID, activeUntil).Scan(&used)
	return used, err
}

func sameEntrantFact(fact entrantFact, command AppendEntrantReceipt) bool {
	return fact.CallbackID == command.CallbackID && fact.InboxID == command.InboxID && fact.CorpID == command.CorpID &&
		fact.InputDigest == command.InputDigest && fact.CommandDigest == command.CommandDigest &&
		fact.ChangeType == command.ChangeType && fact.Status == command.Status &&
		fact.BindingID == command.BindingID && fact.CustomerID == command.CustomerID &&
		fact.OccurredAt.Equal(command.OccurredAt)
}

func sameReconciliation(fact reconciliationFact, command ReconcileEntrantReceipt) bool {
	return fact.EntrantReceiptID == command.ReceiptID && fact.ActorAdminUserID == command.ActorAdminUserID &&
		fact.OperationKeyDigest == command.OperationKeyDigest && fact.CommandDigest == command.CommandDigest &&
		fact.PriorStatus == command.ExpectedStatus && fact.ResultingStatus == EntrantReconciled &&
		fact.BindingID == command.BindingID && fact.CustomerID == command.CustomerID &&
		fact.Reason == command.Reason && fact.ReconciledAt.Equal(command.ReconciledAt)
}

func validEntrantCommand(command AppendEntrantReceipt) bool {
	if !validCallbackID(command.CallbackID) || command.InboxID < 1 || !validCorpID(command.CorpID) || !digestPresent(command.InputDigest) ||
		!digestPresent(command.CommandDigest) ||
		(command.ChangeType != "" && command.ChangeType != "add_external_contact" && command.ChangeType != "add_half_external_contact") ||
		command.OccurredAt.IsZero() {
		return false
	}
	switch command.Status {
	case EntrantChannelAttributed:
		return command.BindingID > 0 && command.CustomerID > 0
	case EntrantChannelUnmatched, EntrantChannelAmbiguous, EntrantIgnored:
		return command.BindingID == 0 && command.CustomerID > 0
	case EntrantIdentityConflict:
		return command.BindingID >= 0 && command.CustomerID == 0
	default:
		return false
	}
}

func validReconcileCommand(command ReconcileEntrantReceipt) bool {
	return command.ReceiptID > 0 && reconcilableEntrantStatus(command.ExpectedStatus) &&
		command.BindingID > 0 && command.CustomerID > 0 && command.ActorAdminUserID > 0 &&
		validReason(command.Reason) && digestPresent(command.OperationKeyDigest) &&
		digestPresent(command.CommandDigest) && !command.ReconciledAt.IsZero()
}

func reconcilableEntrantStatus(status EntrantStatus) bool {
	// Identity conflicts belong to OneID's explicit conflict workflow. A
	// channel operator must never be able to select an arbitrary customer and
	// thereby bypass canonical-root/merge review.
	return status == EntrantChannelUnmatched || status == EntrantChannelAmbiguous
}

func validCorpID(value string) bool {
	return validDatabaseText(value, 256)
}

func validCallbackID(value string) bool {
	return validDatabaseText(value, 512)
}

func validReason(value string) bool {
	return validDatabaseText(value, 500)
}

func validDatabaseText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func digestPresent(value [32]byte) bool { return value != [32]byte{} }

func databaseTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func persistenceAssetKind(value string) (AcquisitionAssetKind, error) {
	switch value {
	case "qrcode":
		return AcquisitionAssetQRCode, nil
	case "link":
		return AcquisitionAssetLink, nil
	default:
		return "", ErrStateBindingConflict
	}
}

func domainAssetKind(value AcquisitionAssetKind) (string, error) {
	switch value {
	case AcquisitionAssetQRCode:
		return "qrcode", nil
	case AcquisitionAssetLink:
		return "link", nil
	default:
		return "", ErrStateBindingConflict
	}
}

func entrantPortDigest(domain string, receipt channelport.EntrantReceipt) [32]byte {
	value := strings.Join([]string{
		domain,
		receipt.CallbackID,
		strconv.FormatInt(receipt.InboxID, 10),
		receipt.CorpID,
		receipt.ChangeType,
		string(receipt.Status),
		strconv.FormatInt(int64(receipt.CustomerID), 10),
		receipt.OccurredAt.UTC().Format(time.RFC3339Nano),
		string(receipt.Resolution.Status),
		strconv.FormatInt(receipt.Resolution.Asset.ChannelID, 10),
		receipt.Resolution.Asset.Kind,
		strconv.FormatInt(receipt.Resolution.Asset.AssetVersion, 10),
	}, "\x00")
	return sha256.Sum256([]byte(value))
}
