package channel

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrEntrantActionUnavailable = errors.New("channel entrant action unavailable")

type EntrantActionStore struct {
	effects       effectport.TransactionalAccepter
	materials     channelport.WelcomeMaterialSnapshotResolver
	tagCommands   customerport.TagCommandSubmitter
	displayNames  customerport.DirectoryDisplayNameReader
	messageCipher channelport.WelcomeMessageCipher
}

func NewEntrantActionStore(effects effectport.TransactionalAccepter, materials channelport.WelcomeMaterialSnapshotResolver) *EntrantActionStore {
	return &EntrantActionStore{effects: effects, materials: materials}
}

// SetTagCommandSubmitter is installed by composition after Customer is built. Existing accepted channel_entry_tag effects remain on the legacy KindChannelEntryTag provider path; only new callbacks use this Customer-owned command.
func (store *EntrantActionStore) SetTagCommandSubmitter(submitter customerport.TagCommandSubmitter) error {
	if store == nil || submitter == nil {
		return ErrEntrantActionUnavailable
	}
	store.tagCommands = submitter
	return nil
}

// SetWelcomeMessageDependencies is installed by composition after Customer and
// callback crypto exist. The reader is transaction-bound and only exposes the
// canonical Customer's presentation-safe display name; it never resolves or
// provisions an identity.
func (store *EntrantActionStore) SetWelcomeMessageDependencies(names customerport.DirectoryDisplayNameReader, cipher channelport.WelcomeMessageCipher) error {
	if store == nil || names == nil || cipher == nil {
		return ErrEntrantActionUnavailable
	}
	store.displayNames, store.messageCipher = names, cipher
	return nil
}

// AcceptCallbackWelcome freezes and accepts the welcome path while the
// authenticated callback transaction is still open. It deliberately does not
// allocate a customer or an assignee: the Inbox lifecycle owns both operations
// later, and can only attach its canonical customer to this immutable intent.
func (store *EntrantActionStore) AcceptCallbackWelcome(ctx context.Context, command channelport.CallbackWelcomeCommand) error {
	if store == nil || store.effects == nil || command.CallbackID == "" || command.CorpID == "" || command.WelcomeGrantRef == "" ||
		command.OccurredAt.IsZero() || command.FirstReceivedAt.IsZero() || command.SendDeadlineAt.IsZero() ||
		!command.SendDeadlineAt.Equal(command.FirstReceivedAt.UTC().Add(20*time.Second)) || !command.Resolution.Valid() {
		return ErrEntrantActionUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	// Serialize one callback's first acceptance before evaluating mutable
	// availability.  The first committed receipt therefore owns the immutable
	// deadline; a concurrent Provider replay waits and then reads that record.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "channel.welcome.callback.v1:"+command.CallbackID); err != nil {
		return err
	}
	existing, err := callbackWelcomeIntentExists(ctx, tx, command.CallbackID)
	if err != nil {
		return err
	}
	if existing {
		// The first accepted callback fixes its 20-second deadline. A provider
		// replay must never re-evaluate configuration or extend that window.
		return nil
	}
	source := effectport.Hash("channel.welcome.intent.source.v2", command.CallbackID)
	if command.Resolution.Status != channeldomain.StateAttributed {
		reason := "state_unmatched"
		if command.Resolution.Status == channeldomain.StateAmbiguous {
			reason = "state_ambiguous"
		}
		return store.recordWelcomeIntent(ctx, tx, callbackWelcomeIntent{command: command, source: source, state: reason, resultReason: reason})
	}

	status, err := lockedChannelStatus(ctx, tx, command.Resolution.Asset.ChannelID)
	if err != nil {
		return err
	}
	config, err := readWelcomeConfig(ctx, tx, command.Resolution)
	if errors.Is(err, ErrEntrantActionUnavailable) {
		return store.recordWelcomeIntent(ctx, tx, callbackWelcomeIntent{command: command, source: source, state: "channel_unavailable", resultReason: "channel_unavailable"})
	}
	if err != nil {
		return err
	}
	if inactiveChannelStatus(status) {
		return store.recordWelcomeIntent(ctx, tx, callbackWelcomeIntent{command: command, source: source, state: "channel_unavailable", resultReason: "channel_unavailable"})
	}
	materialConfigured := len(config.WelcomeMaterials.ImageIDs)+len(config.WelcomeMaterials.MiniProgramIDs)+len(config.WelcomeMaterials.AttachmentIDs)+len(config.WelcomeMaterials.GroupInviteIDs) > 0
	if config.WelcomeMessage == "" && !materialConfigured {
		return store.recordWelcomeIntent(ctx, tx, callbackWelcomeIntent{command: command, source: source, channelID: config.ChannelID, configVersion: config.ConfigVersion, state: "welcome_not_configured", resultReason: "welcome_not_configured"})
	}

	snapshot := json.RawMessage(`{"schema_version":2,"node_kind":"message","attachments":[]}`)
	materialDigest := string(effectport.Hash("channel.welcome.material.empty.v1"))
	if materialConfigured {
		if store.materials == nil {
			return store.recordWelcomeIntent(ctx, tx, callbackWelcomeIntent{command: command, source: source, channelID: config.ChannelID, configVersion: config.ConfigVersion, state: "welcome_material_unavailable", resultReason: "welcome_material_unavailable"})
		}
		snapshot, materialDigest, err = store.materials.ResolveWelcomeMaterialSnapshot(ctx, config.WelcomeMaterials, command.SendDeadlineAt.UTC())
		if err != nil || len(snapshot) == 0 || !effectport.ValidDigest(effectport.Digest(materialDigest)) {
			return store.recordWelcomeIntent(ctx, tx, callbackWelcomeIntent{command: command, source: source, channelID: config.ChannelID, configVersion: config.ConfigVersion, state: "welcome_material_unavailable", resultReason: "welcome_material_unavailable"})
		}
	}

	payload := effectport.Hash("channel.welcome.intent.payload.v2", strconv.FormatInt(config.ChannelID, 10), strconv.FormatInt(config.ConfigVersion, 10), config.WelcomeMessage, materialDigest)
	target := effectport.Hash("channel.welcome.intent.target.v2", command.WelcomeGrantRef)
	policy := effectport.Hash("channel.welcome.intent.policy.v2")
	envelope := effectport.Envelope{
		Owner: effectport.OwnerOutbound, Kind: effectport.KindChannelWelcome,
		SourceRefDigest: source, TargetRefDigest: target, PayloadDigest: payload, PolicyVersionHash: policy,
	}
	projection, receipt, err := store.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{
		ReceiptKey: effectport.Hash("channel.welcome.intent.accept.v2", command.CallbackID),
		Envelope:   envelope,
	})
	if err != nil {
		return err
	}
	return store.recordWelcomeIntent(ctx, tx, callbackWelcomeIntent{
		command: command, source: source, channelID: config.ChannelID, configVersion: config.ConfigVersion,
		materialSnapshot: snapshot, effectRef: projection.ID, acceptReceiptRef: receipt.ID, queueReceiptRef: receipt.QueueReceiptID, state: string(projection.State),
		effectTargetDigest: target, effectPayloadDigest: payload, effectPolicyDigest: policy, effectEnvelopeFingerprint: envelope.Fingerprint(),
	})
}

type callbackWelcomeIntent struct {
	command                                                                                channelport.CallbackWelcomeCommand
	source                                                                                 effectport.Digest
	channelID, configVersion                                                               int64
	materialSnapshot                                                                       json.RawMessage
	effectRef, acceptReceiptRef, queueReceiptRef, state, resultReason                      string
	effectTargetDigest, effectPayloadDigest, effectPolicyDigest, effectEnvelopeFingerprint effectport.Digest
}

func (store *EntrantActionStore) recordWelcomeIntent(ctx context.Context, tx pgx.Tx, intent callbackWelcomeIntent) error {
	digest := callbackWelcomeIntentDigest(intent)
	resultDigest := ""
	if intent.resultReason != "" {
		resultDigest = string(effectport.Hash("channel.welcome.not-sent.v1", intent.resultReason, string(intent.source)))
	}
	_, err := tx.Exec(ctx, `INSERT INTO channel_welcome_intents(callback_id,channel_id,config_version,welcome_grant_ref,welcome_material_snapshot,source_ref_digest,intent_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,first_received_at,send_deadline_at,state,result_digest,result_reason,effect_target_digest,effect_payload_digest,effect_policy_digest,effect_envelope_fingerprint)
		VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT(callback_id) DO NOTHING`,
		intent.command.CallbackID, nullableInt64(intent.channelID), nullableInt64(intent.configVersion), intent.command.WelcomeGrantRef, nullableJSON(intent.materialSnapshot), intent.source, digest[:], nullableString(intent.effectRef), nullableString(intent.acceptReceiptRef), nullableString(intent.queueReceiptRef), intent.command.FirstReceivedAt.UTC(), intent.command.SendDeadlineAt.UTC(), intent.state, nullableString(resultDigest), nullableString(intent.resultReason), nullableDigest(intent.effectTargetDigest), nullableDigest(intent.effectPayloadDigest), nullableDigest(intent.effectPolicyDigest), nullableDigest(intent.effectEnvelopeFingerprint))
	if err != nil {
		return err
	}
	return nil
}

func callbackWelcomeIntentExists(ctx context.Context, tx pgx.Tx, callbackID string) (bool, error) {
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM channel_welcome_intents WHERE callback_id=$1)`, callbackID).Scan(&found)
	return found, err
}

func callbackWelcomeIntentDigest(intent callbackWelcomeIntent) [32]byte {
	return sha256.Sum256([]byte(string(effectport.Hash(
		"channel.welcome.intent.record.v2", intent.command.CallbackID, intent.command.CorpID, intent.command.WelcomeGrantRef,
		strconv.FormatInt(intent.channelID, 10), strconv.FormatInt(intent.configVersion, 10), string(intent.source), intent.state, intent.resultReason,
		intent.command.FirstReceivedAt.UTC().Format(time.RFC3339Nano), intent.command.SendDeadlineAt.UTC().Format(time.RFC3339Nano),
	))))
}

type entrantActionConfig struct {
	ChannelID, ConfigVersion, EntryTagID int64
	WelcomeMessage, Strategy             string
	WelcomeMaterials                     channelport.WelcomeMaterialPlan
	Assignees                            []channeldomain.Assignee
}

func (store *EntrantActionStore) AcceptEntrantActions(ctx context.Context, command channelport.EntrantActionCommand) error {
	if store == nil || store.effects == nil || command.CallbackID == "" || command.CustomerID < 1 || command.OccurredAt.IsZero() || command.Resolution.Status != channeldomain.StateAttributed || !command.Resolution.Valid() {
		return ErrEntrantActionUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	status, err := lockedChannelStatus(ctx, tx, command.Resolution.Asset.ChannelID)
	if err != nil {
		return err
	}
	config, err := readEntrantActionConfig(ctx, tx, command.Resolution)
	if err != nil {
		return err
	}
	staffID, err := chooseEntrantStaff(ctx, tx, config, command.CallbackID, command.OccurredAt)
	if err != nil {
		return err
	}
	if inactiveChannelStatus(status) {
		return channelport.ErrEntrantActionsSkippedInactiveChannel
	}
	assignmentDigest := sha256.Sum256([]byte(command.CallbackID + "\x00" + strconv.FormatInt(config.ChannelID, 10) + "\x00" + strconv.FormatInt(config.ConfigVersion, 10) + "\x00" + strconv.FormatInt(int64(command.CustomerID), 10) + "\x00" + strconv.FormatInt(staffID, 10) + "\x00" + config.Strategy))
	var assignmentID int64
	err = tx.QueryRow(ctx, `INSERT INTO channel_entrant_assignments(callback_id,channel_id,config_version,customer_id,staff_id,strategy,assignment_digest,assigned_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(callback_id) DO NOTHING RETURNING id`, command.CallbackID, config.ChannelID, config.ConfigVersion, command.CustomerID, staffID, config.Strategy, assignmentDigest[:], command.OccurredAt.UTC()).Scan(&assignmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existing []byte
		err = tx.QueryRow(ctx, `SELECT id,assignment_digest FROM channel_entrant_assignments WHERE callback_id=$1`, command.CallbackID).Scan(&assignmentID, &existing)
		if err != nil || len(existing) != sha256.Size || string(existing) != string(assignmentDigest[:]) {
			return ErrEntrantActionUnavailable
		}
	} else if err != nil {
		return err
	}
	if err = linkWelcomeIntentCustomer(ctx, tx, command.CallbackID, command.CustomerID); err != nil {
		return err
	}
	if config.EntryTagID > 0 {
		if store.tagCommands == nil {
			return store.acceptAction(ctx, tx, assignmentID, command, config, staffID, "entry_tag", "", config.EntryTagID, nil, "")
		}
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM channel_entrant_actions WHERE callback_id=$1 AND action_kind='entry_tag')`, command.CallbackID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		accepted, acceptErr := store.tagCommands.SubmitTagCommandWithin(ctx, customerport.TagCommand{Source: "channel_entry_tag", SourceRef: command.CallbackID, IdempotencyKey: "entry_tag", OccurredAt: command.OccurredAt, Targets: []customerport.TagCommandTarget{{CustomerID: command.CustomerID, StaffID: staffID, AddTagIDs: []int64{config.EntryTagID}}}})
		if acceptErr != nil {
			// A pending generic tag command must not roll back the independently
			// valid entrant assignment. Record the blocked entry-tag action with
			// no effect so a callback replay cannot silently mint one later.
			if errors.Is(acceptErr, customerport.ErrTagCommandConflict) {
				return store.recordRejectedEntryTag(ctx, tx, assignmentID, command, config, staffID, "customer_tag_busy")
			}
			return acceptErr
		}
		if len(accepted.Lines) != 1 {
			return ErrEntrantActionUnavailable
		}
		// Customer persists a rejected line for an invalid current relationship or binding. It must not roll back the already-valid entrant assignment/welcome path or manufacture a channel EER.
		if accepted.Lines[0].EffectRef == "" {
			return nil
		}
		source := effectport.Hash("channel.entrant.action.source.v1", command.CallbackID, "entry_tag")
		_, err = tx.Exec(ctx, `INSERT INTO channel_entrant_actions(callback_id,assignment_id,channel_id,config_version,customer_id,staff_id,action_kind,local_tag_id,welcome_material_snapshot,source_ref_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state) VALUES($1,$2,$3,$4,$5,$6,'entry_tag',$7,$8::jsonb,$9,$10,$11,$12,$13) ON CONFLICT(callback_id,action_kind) DO NOTHING`, command.CallbackID, assignmentID, config.ChannelID, config.ConfigVersion, command.CustomerID, staffID, config.EntryTagID, json.RawMessage(`{"schema_version":2,"node_kind":"message","attachments":[]}`), source, accepted.Lines[0].EffectRef, accepted.Lines[0].AcceptReceiptRef, accepted.Lines[0].QueueReceiptRef, accepted.Lines[0].State)
		if err != nil {
			return err
		}
	}
	return nil
}

type welcomeConfig struct {
	ChannelID, ConfigVersion int64
	WelcomeMessage           string
	WelcomeMaterials         channelport.WelcomeMaterialPlan
}

// readWelcomeConfig intentionally does not inspect assignees. A configured
// welcome is eligible as soon as its verified State resolves; staff assignment
// remains part of the later normal entrant lifecycle.

func (*EntrantActionStore) recordRejectedEntryTag(ctx context.Context, tx pgx.Tx, assignmentID int64, command channelport.EntrantActionCommand, config entrantActionConfig, staffID int64, reason string) error {
	if reason == "" {
		return ErrEntrantActionUnavailable
	}
	source := effectport.Hash("channel.entrant.action.source.v1", command.CallbackID, "entry_tag")
	_, err := tx.Exec(ctx, `INSERT INTO channel_entrant_actions(callback_id,assignment_id,channel_id,config_version,customer_id,staff_id,action_kind,local_tag_id,welcome_material_snapshot,source_ref_digest,state,result_reason)
		VALUES($1,$2,$3,$4,$5,$6,'entry_tag',$7,$8::jsonb,$9,'rejected',$10) ON CONFLICT(callback_id,action_kind) DO NOTHING`,
		command.CallbackID, assignmentID, config.ChannelID, config.ConfigVersion, command.CustomerID, staffID, config.EntryTagID,
		json.RawMessage(`{"schema_version":2,"node_kind":"message","attachments":[]}`), source, reason)
	return err
}

func readWelcomeConfig(ctx context.Context, tx pgx.Tx, resolution channeldomain.StateResolution) (welcomeConfig, error) {
	providerKind := string(AcquisitionAssetQRCode)
	if resolution.Asset.Kind == "link" {
		providerKind = string(AcquisitionAssetLink)
	}
	var config welcomeConfig
	err := tx.QueryRow(ctx, `SELECT a.channel_id,a.config_version,v.welcome_message,v.welcome_image_ids,v.welcome_miniprogram_ids,v.welcome_attachment_ids,v.welcome_group_invite_ids
		FROM channel_acquisition_assets a JOIN channel_config_versions v ON v.channel_id=a.channel_id AND v.config_version=a.config_version
		WHERE a.channel_id=$1 AND a.kind=$2 AND a.asset_version=$3 AND a.state IN ('executed','reconciled')`,
		resolution.Asset.ChannelID, providerKind, resolution.Asset.AssetVersion,
	).Scan(&config.ChannelID, &config.ConfigVersion, &config.WelcomeMessage, &config.WelcomeMaterials.ImageIDs, &config.WelcomeMaterials.MiniProgramIDs, &config.WelcomeMaterials.AttachmentIDs, &config.WelcomeMaterials.GroupInviteIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT c.id,c.current_config_version,v.welcome_message,v.welcome_image_ids,v.welcome_miniprogram_ids,v.welcome_attachment_ids,v.welcome_group_invite_ids
			FROM channels c JOIN channel_config_versions v ON v.channel_id=c.id AND v.config_version=c.current_config_version
			WHERE c.id=$1 AND EXISTS(SELECT 1 FROM channel_legacy_acquisition_assets l WHERE l.channel_id=c.id AND l.kind=$2 AND l.verification_status='legacy_verified_active' AND l.retired_at IS NULL)`,
			resolution.Asset.ChannelID, providerKind,
		).Scan(&config.ChannelID, &config.ConfigVersion, &config.WelcomeMessage, &config.WelcomeMaterials.ImageIDs, &config.WelcomeMaterials.MiniProgramIDs, &config.WelcomeMaterials.AttachmentIDs, &config.WelcomeMaterials.GroupInviteIDs)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return welcomeConfig{}, ErrEntrantActionUnavailable
	}
	return config, err
}

func readEntrantActionConfig(ctx context.Context, tx pgx.Tx, resolution channeldomain.StateResolution) (entrantActionConfig, error) {
	providerKind := string(AcquisitionAssetQRCode)
	if resolution.Asset.Kind == "link" {
		providerKind = string(AcquisitionAssetLink)
	}
	var config entrantActionConfig
	var entryTagID *int64
	err := tx.QueryRow(ctx, `SELECT a.channel_id,a.config_version,v.welcome_message,v.entry_tag_id,v.assignment_strategy,v.welcome_image_ids,v.welcome_miniprogram_ids,v.welcome_attachment_ids,v.welcome_group_invite_ids FROM channel_acquisition_assets a JOIN channel_config_versions v ON v.channel_id=a.channel_id AND v.config_version=a.config_version WHERE a.channel_id=$1 AND a.kind=$2 AND a.asset_version=$3 AND a.state IN ('executed','reconciled')`, resolution.Asset.ChannelID, providerKind, resolution.Asset.AssetVersion).Scan(&config.ChannelID, &config.ConfigVersion, &config.WelcomeMessage, &entryTagID, &config.Strategy, &config.WelcomeMaterials.ImageIDs, &config.WelcomeMaterials.MiniProgramIDs, &config.WelcomeMaterials.AttachmentIDs, &config.WelcomeMaterials.GroupInviteIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		// Legacy bindings use synthetic immutable asset versions. They are only
		// inserted after a successful Provider readback, and this fallback also
		// requires a currently verified legacy asset before using current config.
		err = tx.QueryRow(ctx, `SELECT c.id,c.current_config_version,v.welcome_message,v.entry_tag_id,v.assignment_strategy,v.welcome_image_ids,v.welcome_miniprogram_ids,v.welcome_attachment_ids,v.welcome_group_invite_ids
			FROM channels c JOIN channel_config_versions v ON v.channel_id=c.id AND v.config_version=c.current_config_version
			WHERE c.id=$1 AND EXISTS(SELECT 1 FROM channel_legacy_acquisition_assets l WHERE l.channel_id=c.id AND l.kind=$2 AND l.verification_status='legacy_verified_active' AND l.retired_at IS NULL)`, resolution.Asset.ChannelID, providerKind).Scan(&config.ChannelID, &config.ConfigVersion, &config.WelcomeMessage, &entryTagID, &config.Strategy, &config.WelcomeMaterials.ImageIDs, &config.WelcomeMaterials.MiniProgramIDs, &config.WelcomeMaterials.AttachmentIDs, &config.WelcomeMaterials.GroupInviteIDs)
		if errors.Is(err, pgx.ErrNoRows) {
			return entrantActionConfig{}, ErrEntrantActionUnavailable
		}
	}
	if err != nil {
		return entrantActionConfig{}, err
	}
	if entryTagID != nil {
		config.EntryTagID = *entryTagID
	}
	rows, err := tx.Query(ctx, `SELECT staff_id,priority,COALESCE(ratio_percent,0),COALESCE(max_scans_24h,0) FROM channel_assignees WHERE channel_id=$1 AND config_version=$2 ORDER BY priority`, config.ChannelID, config.ConfigVersion)
	if err != nil {
		return entrantActionConfig{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var assignee channeldomain.Assignee
		if err = rows.Scan(&assignee.StaffID, &assignee.Priority, &assignee.Ratio, &assignee.MaxScans24h); err != nil {
			return entrantActionConfig{}, err
		}
		config.Assignees = append(config.Assignees, assignee)
	}
	if err = rows.Err(); err != nil || len(config.Assignees) == 0 {
		return entrantActionConfig{}, ErrEntrantActionUnavailable
	}
	return config, nil
}

// lockedChannelStatus serializes status changes with acceptance. It is read
// before either asset path, but inactive/archived is classified as no-action
// only after that path and entrant preconditions are verified.
func lockedChannelStatus(ctx context.Context, tx pgx.Tx, channelID int64) (string, error) {
	var status string
	// Hold a shared row lock through acceptance. A concurrent configuration
	// transition must serialize before or after this callback; it cannot archive
	// a channel after this check while the callback creates an effect.
	err := tx.QueryRow(ctx, `SELECT status FROM channels WHERE id=$1 FOR SHARE`, channelID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrEntrantActionUnavailable
	}
	if err != nil {
		return "", err
	}
	switch status {
	case "active", "inactive", "archived":
		return status, nil
	default:
		return "", ErrEntrantActionUnavailable
	}
}

func inactiveChannelStatus(status string) bool { return status == "inactive" || status == "archived" }

func chooseEntrantStaff(ctx context.Context, tx pgx.Tx, config entrantActionConfig, callbackID string, occurredAt time.Time) (int64, error) {
	switch channeldomain.AssignmentStrategy(config.Strategy) {
	case channeldomain.StrategyRatio:
		digest := sha256.Sum256([]byte("channel.assignment.ratio.v1\x00" + callbackID))
		point, cumulative := int(binary.BigEndian.Uint64(digest[:8])%100)+1, 0
		for _, item := range config.Assignees {
			cumulative += item.Ratio
			if point <= cumulative {
				return item.StaffID, nil
			}
		}
	case channeldomain.StrategyCapSwitch:
		for _, item := range config.Assignees {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM channel_entrant_assignments WHERE channel_id=$1 AND config_version=$2 AND staff_id=$3 AND assigned_at>=$4 AND assigned_at<$5`, config.ChannelID, config.ConfigVersion, item.StaffID, occurredAt.UTC().Add(-24*time.Hour), occurredAt.UTC()).Scan(&count); err != nil {
				return 0, err
			}
			if count < item.MaxScans24h {
				return item.StaffID, nil
			}
		}
	}
	return 0, ErrEntrantActionUnavailable
}

func (store *EntrantActionStore) acceptAction(ctx context.Context, tx pgx.Tx, assignmentID int64, command channelport.EntrantActionCommand, config entrantActionConfig, staffID int64, kind, grant string, tagID int64, materialSnapshot json.RawMessage, materialDigest string) error {
	source := effectport.Hash("channel.entrant.action.source.v1", command.CallbackID, kind)
	target := effectport.Hash("channel.entrant.action.target.v1", strconv.FormatInt(int64(command.CustomerID), 10), strconv.FormatInt(staffID, 10))
	payload := effectport.Hash("channel.entrant.action.payload.v2", strconv.FormatInt(config.ChannelID, 10), strconv.FormatInt(config.ConfigVersion, 10), kind, config.WelcomeMessage, strconv.FormatInt(tagID, 10), materialDigest)
	effectKind := effectport.KindChannelWelcome
	if kind == "entry_tag" {
		effectKind = effectport.KindChannelEntryTag
	}
	projection, receipt, err := store.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("channel.entrant.action.accept.v1", command.CallbackID, kind), Envelope: effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectKind, SourceRefDigest: source, TargetRefDigest: target, PayloadDigest: payload, PolicyVersionHash: effectport.Hash("channel.entrant.action.policy", "v1")}})
	if err != nil {
		return err
	}
	if len(materialSnapshot) == 0 {
		materialSnapshot = json.RawMessage(`{"schema_version":2,"node_kind":"message","attachments":[]}`)
	}
	_, err = tx.Exec(ctx, `INSERT INTO channel_entrant_actions(callback_id,assignment_id,channel_id,config_version,customer_id,staff_id,action_kind,welcome_grant_ref,local_tag_id,welcome_material_snapshot,source_ref_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,$13,$14,$15) ON CONFLICT(callback_id,action_kind) DO NOTHING`, command.CallbackID, assignmentID, config.ChannelID, config.ConfigVersion, command.CustomerID, staffID, kind, nullableString(grant), nullableInt64(tagID), materialSnapshot, source, projection.ID, receipt.ID, receipt.QueueReceiptID, projection.State)
	return err
}

func linkWelcomeIntentCustomer(ctx context.Context, tx pgx.Tx, callbackID string, customerID customerdomain.CustomerID) error {
	if customerID < 1 {
		return ErrEntrantActionUnavailable
	}
	_, err := tx.Exec(ctx, `UPDATE channel_welcome_intents SET customer_id=$2,updated_at=clock_timestamp() WHERE callback_id=$1 AND customer_id IS NULL`, callbackID, customerID)
	return err
}

func (*EntrantActionStore) ReadPublishedEntrantAction(ctx context.Context, source string) (channelport.PublishedEntrantAction, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return channelport.PublishedEntrantAction{}, err
	}
	var result channelport.PublishedEntrantAction
	err = tx.QueryRow(ctx, `SELECT i.id,i.channel_id,i.config_version,COALESCE(i.customer_id,0),0,'welcome',i.effect_ref,i.welcome_grant_ref,0,i.welcome_material_snapshot,v.welcome_message,i.first_received_at,i.send_deadline_at
		FROM channel_welcome_intents i JOIN channel_config_versions v ON v.channel_id=i.channel_id AND v.config_version=i.config_version
		WHERE i.source_ref_digest=$1 AND i.effect_ref IS NOT NULL`, source,
	).Scan(&result.ActionID, &result.ChannelID, &result.ConfigVersion, &result.CustomerID, &result.StaffID, &result.Kind, &result.EffectRef, &result.WelcomeGrantRef, &result.LocalTagID, &result.WelcomeMaterialSnapshot, &result.WelcomeMessage, &result.FirstReceivedAt, &result.SendDeadlineAt)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return channelport.PublishedEntrantAction{}, err
	}
	var grant *string
	var tag *int64
	err = tx.QueryRow(ctx, `SELECT a.id,a.channel_id,a.config_version,a.customer_id,a.staff_id,a.action_kind,a.effect_ref,a.welcome_grant_ref,a.local_tag_id,a.welcome_material_snapshot,v.welcome_message FROM channel_entrant_actions a JOIN channel_config_versions v ON v.channel_id=a.channel_id AND v.config_version=a.config_version WHERE a.source_ref_digest=$1`, source).Scan(&result.ActionID, &result.ChannelID, &result.ConfigVersion, &result.CustomerID, &result.StaffID, &result.Kind, &result.EffectRef, &grant, &tag, &result.WelcomeMaterialSnapshot, &result.WelcomeMessage)
	if grant != nil {
		result.WelcomeGrantRef = *grant
	}
	if tag != nil {
		result.LocalTagID = *tag
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return channelport.PublishedEntrantAction{}, ErrEntrantActionUnavailable
	}
	return result, err
}

// FreezePublishedWelcomeMessage is the only point where a Channel welcome
// template becomes Provider text. It runs in a short local transaction: the
// callback has already accepted the EER, Customer lifecycle may already have
// attached a canonical id, and the Provider call happens only after this
// function has committed.
func (store *EntrantActionStore) FreezePublishedWelcomeMessage(ctx context.Context, request channelport.WelcomeMessageFreezeRequest) (string, error) {
	if store == nil || store.messageCipher == nil || request.EffectRef == "" || request.Envelope.Kind != effectport.KindChannelWelcome || !request.Envelope.Valid() || request.Envelope.Fingerprint() == "" {
		return "", channelport.ErrWelcomeMessageUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", err
	}
	type welcomeIntent struct {
		id          int64
		customerID  customerdomain.CustomerID
		template    string
		source      effectport.Digest
		effectRef   string
		target      effectport.Digest
		payload     effectport.Digest
		policy      effectport.Digest
		fingerprint effectport.Digest
	}
	var intent welcomeIntent
	err = tx.QueryRow(ctx, `SELECT i.id,COALESCE(i.customer_id,0),v.welcome_message,i.source_ref_digest,i.effect_ref,
		i.effect_target_digest,i.effect_payload_digest,i.effect_policy_digest,i.effect_envelope_fingerprint
		FROM channel_welcome_intents i
		JOIN channel_config_versions v ON v.channel_id=i.channel_id AND v.config_version=i.config_version
		WHERE i.effect_ref=$1 AND i.source_ref_digest=$2
		FOR UPDATE OF i`, request.EffectRef, request.Envelope.SourceRefDigest).Scan(
		&intent.id, &intent.customerID, &intent.template, &intent.source, &intent.effectRef,
		&intent.target, &intent.payload, &intent.policy, &intent.fingerprint,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", channelport.ErrWelcomeMessageUnavailable
	}
	if err != nil {
		return "", err
	}
	if intent.id < 1 || intent.effectRef != request.EffectRef || intent.source != request.Envelope.SourceRefDigest ||
		intent.target != request.Envelope.TargetRefDigest || intent.payload != request.Envelope.PayloadDigest ||
		intent.policy != request.Envelope.PolicyVersionHash || intent.fingerprint != request.Envelope.Fingerprint() {
		// Intents accepted before this schema carried no immutable envelope
		// fingerprint. They must fail closed rather than bind fresh rendered
		// text to an unverifiable external effect.
		return "", channelport.ErrWelcomeMessageUnavailable
	}
	if err = channeldomain.ValidateWelcomeMessageTemplate(intent.template); err != nil {
		return "", channelport.ErrWelcomeMessageTemplateInvalid
	}

	templateDigest := sha256.Sum256([]byte(intent.template))
	var storedTemplateDigest, renderedDigest, ciphertext []byte
	var version int16
	var snapshotFingerprint effectport.Digest
	err = tx.QueryRow(ctx, `SELECT template_digest,rendered_message_digest,ciphertext,cipher_version,envelope_fingerprint
		FROM channel_welcome_message_snapshots WHERE welcome_intent_id=$1`, intent.id).Scan(
		&storedTemplateDigest, &renderedDigest, &ciphertext, &version, &snapshotFingerprint,
	)
	if err == nil {
		if len(storedTemplateDigest) != sha256.Size || string(storedTemplateDigest) != string(templateDigest[:]) ||
			len(renderedDigest) != sha256.Size || snapshotFingerprint != request.Envelope.Fingerprint() {
			return "", channelport.ErrWelcomeMessageUnavailable
		}
		plain, decryptErr := store.messageCipher.DecryptChannelWelcomeMessage(ciphertext, version, channelWelcomeMessageAAD(intent.id, intent.effectRef, request.Envelope))
		if decryptErr != nil || sha256.Sum256(plain) != sha256Array(renderedDigest) {
			return "", channelport.ErrWelcomeMessageUnavailable
		}
		if err = channeldomain.ValidateRenderedWelcomeMessage(string(plain)); err != nil {
			return "", channelport.ErrWelcomeMessageTooLong
		}
		return string(plain), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}

	displayName := ""
	if intent.customerID > 0 && strings.Contains(intent.template, channeldomain.WelcomeCustomerNameVariable) {
		if store.displayNames == nil {
			return "", channelport.ErrWelcomeMessageCustomerNameUnavailable
		}
		names, readErr := store.displayNames.DisplayNames(ctx, []customerdomain.CustomerID{intent.customerID})
		if readErr != nil {
			return "", errors.Join(channelport.ErrWelcomeMessageCustomerNameUnavailable, readErr)
		}
		displayName = names[intent.customerID]
	}
	rendered, renderErr := channeldomain.RenderWelcomeMessage(intent.template, displayName)
	if renderErr != nil {
		return "", channelport.ErrWelcomeMessageTemplateInvalid
	}
	if renderErr = channeldomain.ValidateRenderedWelcomeMessage(rendered); renderErr != nil {
		return "", channelport.ErrWelcomeMessageTooLong
	}
	renderedHash := sha256.Sum256([]byte(rendered))
	ciphertext, version, err = store.messageCipher.EncryptChannelWelcomeMessage([]byte(rendered), channelWelcomeMessageAAD(intent.id, intent.effectRef, request.Envelope))
	if err != nil || version != 1 || len(ciphertext) < 28 {
		return "", channelport.ErrWelcomeMessageUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO channel_welcome_message_snapshots(welcome_intent_id,template_digest,rendered_message_digest,ciphertext,cipher_version,envelope_fingerprint,created_at)
		VALUES($1,$2,$3,$4,$5,$6,clock_timestamp())`, intent.id, templateDigest[:], renderedHash[:], ciphertext, version, request.Envelope.Fingerprint())
	if err != nil {
		return "", err
	}
	return rendered, nil
}

func channelWelcomeMessageAAD(intentID int64, effectRef string, envelope effectport.Envelope) []byte {
	return []byte(effectport.Hash("channel.welcome.message.snapshot.aad.v1", strconv.FormatInt(intentID, 10), effectRef,
		string(envelope.SourceRefDigest), string(envelope.TargetRefDigest), string(envelope.PayloadDigest), string(envelope.PolicyVersionHash)))
}

func sha256Array(value []byte) [sha256.Size]byte {
	var out [sha256.Size]byte
	copy(out[:], value)
	return out
}

func (*EntrantActionStore) CompleteEntrantAction(ctx context.Context, completion channelport.EntrantActionCompletion) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE channel_welcome_intents SET state=$2,result_digest=$3,result_reason=$4,provider_error_code=$5,attempt_count=GREATEST(attempt_count,$6),updated_at=$7 WHERE effect_ref=$1 AND state IN ('queued','attempted','outcome_unknown','retryable_failed')`, completion.EffectRef, completion.State, completion.ResultDigest, nullableString(completion.ResultReason), nullableInt64(completion.ProviderErrorCode), completion.Attempt, completion.CompletedAt.UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	result, err = tx.Exec(ctx, `UPDATE channel_entrant_actions SET state=$2,result_digest=$3,updated_at=$4 WHERE effect_ref=$1 AND state IN ('queued','attempted','outcome_unknown','retryable_failed')`, completion.EffectRef, completion.State, completion.ResultDigest, completion.CompletedAt.UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var state, digest string
	err = tx.QueryRow(ctx, `SELECT state,result_digest FROM channel_entrant_actions WHERE effect_ref=$1`, completion.EffectRef).Scan(&state, &digest)
	if err == nil && state == completion.State && digest == completion.ResultDigest {
		return nil
	}
	return ErrEntrantActionUnavailable
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableDigest(value effectport.Digest) any {
	if value == "" {
		return nil
	}
	return string(value)
}
func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
