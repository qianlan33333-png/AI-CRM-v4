package channel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQLCatalogStore struct{}

func NewPostgreSQLCatalogStore() *PostgreSQLCatalogStore { return &PostgreSQLCatalogStore{} }

func (*PostgreSQLCatalogStore) Get(ctx context.Context, id int64) (channeldomain.Channel, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return channeldomain.Channel{}, err
	}
	return getCatalogChannel(ctx, tx, id)
}

func (*PostgreSQLCatalogStore) List(ctx context.Context, filter channelport.CatalogFilter) ([]channeldomain.Channel, int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, 0, err
	}
	status := string(filter.Status)
	keyword := strings.ToLower(filter.Keyword)
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM channels c JOIN channel_config_versions v ON v.channel_id=c.id AND v.config_version=c.current_config_version WHERE ($1='' OR c.status=$1) AND ($2 OR c.status<>'archived') AND ($3='' OR lower(c.code) LIKE '%'||$3||'%' OR lower(v.name) LIKE '%'||$3||'%')`, status, filter.IncludeArchived, keyword).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT c.id FROM channels c JOIN channel_config_versions v ON v.channel_id=c.id AND v.config_version=c.current_config_version WHERE c.id>$1 AND ($2='' OR c.status=$2) AND ($3 OR c.status<>'archived') AND ($4='' OR lower(c.code) LIKE '%'||$4||'%' OR lower(v.name) LIKE '%'||$4||'%') ORDER BY c.id LIMIT $5`, filter.AfterID, status, filter.IncludeArchived, keyword, filter.Limit)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]int64, 0, filter.Limit)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	items := make([]channeldomain.Channel, 0, len(ids))
	for _, id := range ids {
		item, getErr := getCatalogChannel(ctx, tx, id)
		if getErr != nil {
			return nil, 0, getErr
		}
		items = append(items, item)
	}
	return items, total, nil
}

func (*PostgreSQLCatalogStore) Create(ctx context.Context, channel channeldomain.Channel, actorID int64) (channeldomain.Channel, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return channeldomain.Channel{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO channels(code,status,current_config_version,version,created_at,updated_at,archived_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, channel.Code, channel.Status, channel.ConfigVersion, channel.Version, channel.CreatedAt, channel.UpdatedAt, archivedAt(channel)).Scan(&channel.ID)
	if err != nil {
		return channeldomain.Channel{}, mapCatalogPostgresError(err)
	}
	if err = insertCatalogConfig(ctx, tx, channel, actorID); err != nil {
		return channeldomain.Channel{}, err
	}
	return getCatalogChannel(ctx, tx, channel.ID)
}

func (*PostgreSQLCatalogStore) Update(ctx context.Context, channel channeldomain.Channel, actorID int64) (channeldomain.Channel, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return channeldomain.Channel{}, err
	}
	if err = insertCatalogConfig(ctx, tx, channel, actorID); err != nil {
		return channeldomain.Channel{}, err
	}
	command, err := tx.Exec(ctx, `UPDATE channels SET status=$1,current_config_version=$2,version=$3,updated_at=$4,archived_at=$5 WHERE id=$6 AND version=$7`, channel.Status, channel.ConfigVersion, channel.Version, channel.UpdatedAt, archivedAt(channel), channel.ID, channel.Version-1)
	if err != nil {
		return channeldomain.Channel{}, mapCatalogPostgresError(err)
	}
	if command.RowsAffected() != 1 {
		return channeldomain.Channel{}, ErrCatalogConflict
	}
	return getCatalogChannel(ctx, tx, channel.ID)
}

func (*PostgreSQLCatalogStore) ReferenceCount(ctx context.Context, id int64) (int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_acquisition_state_bindings WHERE channel_id=$1)+(SELECT count(*) FROM channel_acquisition_entrant_receipts r JOIN channel_acquisition_state_bindings b ON b.id=r.binding_id WHERE b.channel_id=$1)`, id).Scan(&count)
	return count, err
}

func (*PostgreSQLCatalogStore) Reserve(ctx context.Context, input channelport.OperationReceipt) (channelport.OperationReceipt, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return channelport.OperationReceipt{}, false, err
	}
	now := time.Now().UTC()
	err = tx.QueryRow(ctx, `INSERT INTO channel_operation_receipts(operation,actor_admin_user_id,operation_key_digest,payload_digest,state,created_at) VALUES($1,$2,$3,$4,'in_progress',$5) ON CONFLICT(operation,actor_admin_user_id,operation_key_digest) DO NOTHING RETURNING id,state,created_at`, input.Operation, input.ActorID, input.KeyDigest[:], input.PayloadDigest[:], now).Scan(&input.ID, &input.State, &now)
	if err == nil {
		return input, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return channelport.OperationReceipt{}, false, mapCatalogPostgresError(err)
	}
	var keyBytes, payloadBytes []byte
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_admin_user_id,operation_key_digest,payload_digest,state,COALESCE(channel_id,0),COALESCE(channel_version,0),completed_at FROM channel_operation_receipts WHERE operation=$1 AND actor_admin_user_id=$2 AND operation_key_digest=$3 FOR UPDATE`, input.Operation, input.ActorID, input.KeyDigest[:]).Scan(&input.ID, &input.Operation, &input.ActorID, &keyBytes, &payloadBytes, &input.State, &input.ChannelID, &input.Version, &input.CompletedAt)
	if err != nil {
		return channelport.OperationReceipt{}, false, err
	}
	if len(keyBytes) != 32 || len(payloadBytes) != 32 {
		return channelport.OperationReceipt{}, false, ErrCatalogConflict
	}
	copy(input.KeyDigest[:], keyBytes)
	copy(input.PayloadDigest[:], payloadBytes)
	return input, false, nil
}

func (*PostgreSQLCatalogStore) Complete(ctx context.Context, id, channelID, version int64, completedAt time.Time) (channelport.OperationReceipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return channelport.OperationReceipt{}, err
	}
	var result channelport.OperationReceipt
	var keyBytes, payloadBytes []byte
	err = tx.QueryRow(ctx, `UPDATE channel_operation_receipts SET state='completed',channel_id=$1,channel_version=$2,completed_at=$3 WHERE id=$4 AND state='in_progress' RETURNING id,operation,actor_admin_user_id,operation_key_digest,payload_digest,state,channel_id,channel_version,completed_at`, channelID, version, completedAt.UTC(), id).Scan(&result.ID, &result.Operation, &result.ActorID, &keyBytes, &payloadBytes, &result.State, &result.ChannelID, &result.Version, &result.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return channelport.OperationReceipt{}, ErrCatalogConflict
	}
	if err != nil {
		return channelport.OperationReceipt{}, err
	}
	copy(result.KeyDigest[:], keyBytes)
	copy(result.PayloadDigest[:], payloadBytes)
	return result, nil
}

func insertCatalogConfig(ctx context.Context, tx pgx.Tx, channel channeldomain.Channel, actorID int64) error {
	configJSON, err := json.Marshal(channel.Config)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(configJSON)
	var tagID any
	if channel.Config.EntryTagID > 0 {
		tagID = channel.Config.EntryTagID
	}
	_, err = tx.Exec(ctx, `INSERT INTO channel_config_versions(channel_id,config_version,channel_type,carrier_type,name,scene_value,qrcode_url,customer_channel,link_url,final_url,welcome_message,welcome_image_ids,welcome_miniprogram_ids,welcome_attachment_ids,welcome_group_invite_ids,auto_accept_friend,entry_tag_id,entry_tag_name,entry_tag_group_name,assignment_mode,assignment_strategy,overflow_policy,config_digest,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)`, channel.ID, channel.ConfigVersion, channel.Config.Type, channel.Config.Carrier, channel.Config.Name, channel.Config.SceneValue, channel.Config.QRCodeURL, channel.Config.CustomerChannel, channel.Config.LinkURL, channel.Config.FinalURL, channel.Config.WelcomeMessage, nonNilCatalogIDs(channel.Config.Media.Images), nonNilCatalogIDs(channel.Config.Media.MiniPrograms), nonNilCatalogIDs(channel.Config.Media.Attachments), nonNilCatalogIDs(channel.Config.Media.GroupInvites), channel.Config.AutoAcceptFriend, tagID, channel.Config.EntryTagName, channel.Config.EntryTagGroupName, channel.Config.Assignment.Mode, channel.Config.Assignment.Strategy, channel.Config.Assignment.OverflowPolicy, digest[:], actorID, channel.UpdatedAt)
	if err != nil {
		return mapCatalogPostgresError(err)
	}
	for _, assignee := range channel.Config.Assignment.Assignees {
		var ratio, cap any
		if assignee.Ratio > 0 {
			ratio = assignee.Ratio
		}
		if assignee.MaxScans24h > 0 {
			cap = assignee.MaxScans24h
		}
		if _, err = tx.Exec(ctx, `INSERT INTO channel_assignees(channel_id,config_version,staff_id,priority,ratio_percent,max_scans_24h,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, channel.ID, channel.ConfigVersion, assignee.StaffID, assignee.Priority, ratio, cap, channel.UpdatedAt); err != nil {
			return mapCatalogPostgresError(err)
		}
	}
	return nil
}

func getCatalogChannel(ctx context.Context, tx pgx.Tx, id int64) (channeldomain.Channel, error) {
	var channel channeldomain.Channel
	var archived *time.Time
	var imageIDs, miniProgramIDs, attachmentIDs, groupInviteIDs []int64
	var entryTagID *int64
	err := tx.QueryRow(ctx, `SELECT c.id,c.code,c.status,c.current_config_version,c.version,c.created_at,c.updated_at,c.archived_at,v.channel_type,v.carrier_type,v.name,v.scene_value,v.qrcode_url,v.customer_channel,v.link_url,v.final_url,v.welcome_message,v.welcome_image_ids,v.welcome_miniprogram_ids,v.welcome_attachment_ids,v.welcome_group_invite_ids,v.auto_accept_friend,v.entry_tag_id,v.entry_tag_name,v.entry_tag_group_name,v.assignment_mode,v.assignment_strategy,v.overflow_policy FROM channels c JOIN channel_config_versions v ON v.channel_id=c.id AND v.config_version=c.current_config_version WHERE c.id=$1`, id).Scan(&channel.ID, &channel.Code, &channel.Status, &channel.ConfigVersion, &channel.Version, &channel.CreatedAt, &channel.UpdatedAt, &archived, &channel.Config.Type, &channel.Config.Carrier, &channel.Config.Name, &channel.Config.SceneValue, &channel.Config.QRCodeURL, &channel.Config.CustomerChannel, &channel.Config.LinkURL, &channel.Config.FinalURL, &channel.Config.WelcomeMessage, &imageIDs, &miniProgramIDs, &attachmentIDs, &groupInviteIDs, &channel.Config.AutoAcceptFriend, &entryTagID, &channel.Config.EntryTagName, &channel.Config.EntryTagGroupName, &channel.Config.Assignment.Mode, &channel.Config.Assignment.Strategy, &channel.Config.Assignment.OverflowPolicy)
	if errors.Is(err, pgx.ErrNoRows) {
		return channeldomain.Channel{}, ErrCatalogNotFound
	}
	if err != nil {
		return channeldomain.Channel{}, err
	}
	channel.Config.Media = channeldomain.MediaReferences{Images: imageIDs, MiniPrograms: miniProgramIDs, Attachments: attachmentIDs, GroupInvites: groupInviteIDs}
	if entryTagID != nil {
		channel.Config.EntryTagID = *entryTagID
	}
	rows, err := tx.Query(ctx, `SELECT staff_id,priority,COALESCE(ratio_percent,0),COALESCE(max_scans_24h,0) FROM channel_assignees WHERE channel_id=$1 AND config_version=$2 ORDER BY priority`, channel.ID, channel.ConfigVersion)
	if err != nil {
		return channeldomain.Channel{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item channeldomain.Assignee
		if err = rows.Scan(&item.StaffID, &item.Priority, &item.Ratio, &item.MaxScans24h); err != nil {
			return channeldomain.Channel{}, err
		}
		channel.Config.Assignment.Assignees = append(channel.Config.Assignment.Assignees, item)
	}
	if err = rows.Err(); err != nil {
		return channeldomain.Channel{}, err
	}
	if err = validateReadableCatalogChannel(ctx, tx, channel); err != nil {
		return channeldomain.Channel{}, err
	}
	return channel, nil
}

func validateReadableCatalogChannel(ctx context.Context, tx pgx.Tx, channel channeldomain.Channel) error {
	validationErr := channeldomain.ValidateChannel(channel)
	if validationErr == nil {
		return nil
	}
	// A semantic repair may faithfully preserve an incomplete or invalid V1
	// assignee snapshot. Such a channel must remain visible so an administrator
	// can repair it, but it must never become publishable. Normal creates and
	// updates still use the strict domain validator; this read exception is
	// limited to an inactive, migration-owned config with an explicit blocker.
	if channel.Status == channeldomain.StatusActive {
		return validationErr
	}
	structurallyValid := channel
	structurallyValid.Config.Assignment = channeldomain.Assignment{
		Mode: channeldomain.AssignmentSingle, Strategy: channeldomain.StrategyRatio,
		Assignees: []channeldomain.Assignee{{StaffID: 1, Priority: 1, Ratio: 100}},
	}
	if channeldomain.ValidateChannel(structurallyValid) != nil {
		return validationErr
	}
	var migrationBlocked bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM channel_semantic_repaired_configs
		WHERE channel_id=$1 AND config_version=$2 AND activated_at IS NULL
		  AND blockers ?| ARRAY['assignees_missing','assignee_provider_ineligible','assignment_priority_invalid','assignment_ratio_invalid']
	)`, channel.ID, channel.ConfigVersion).Scan(&migrationBlocked)
	if err != nil {
		return err
	}
	if !migrationBlocked {
		return validationErr
	}
	return nil
}

func archivedAt(channel channeldomain.Channel) any {
	if channel.Status == channeldomain.StatusArchived {
		return channel.UpdatedAt
	}
	return nil
}

func nonNilCatalogIDs(values []int64) []int64 {
	if values == nil {
		return []int64{}
	}
	return values
}

func mapCatalogPostgresError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "40001", "40P01":
			return errors.Join(ErrCatalogConflict, err)
		case "23503", "23514", "22001":
			return errors.Join(ErrInvalidCatalogCommand, err)
		}
	}
	return err
}

var _ channelport.CatalogStore = (*PostgreSQLCatalogStore)(nil)
var _ channelport.OperationReceiptStore = (*PostgreSQLCatalogStore)(nil)
