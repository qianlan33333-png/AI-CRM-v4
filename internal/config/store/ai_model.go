package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (r *Repository) ReadAIModelRecord(ctx context.Context, lock bool) (configapp.AIModelRecord, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configapp.AIModelRecord{}, err
	}
	if lock {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('config.ai-model.v1'))`); err != nil {
			return configapp.AIModelRecord{}, err
		}
	}
	var row configapp.AIModelRecord
	err = tx.QueryRow(ctx, `SELECT provider,model,key_ciphertext,version FROM config_ai_model WHERE id=TRUE`).Scan(&row.Provider, &row.Model, &row.Ciphertext, &row.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return configapp.AIModelRecord{}, nil
	}
	return row, err
}
func (r *Repository) SaveAIModelRecord(ctx context.Context, row configapp.AIModelRecord, expected, actor int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `INSERT INTO config_ai_model(id,provider,model,key_ciphertext,version) VALUES(TRUE,$1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET provider=EXCLUDED.provider,model=EXCLUDED.model,key_ciphertext=EXCLUDED.key_ciphertext,version=EXCLUDED.version,updated_at=clock_timestamp() WHERE config_ai_model.version=$5`, row.Provider, row.Model, row.Ciphertext, row.Version, expected)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return configapp.ErrAIModelConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO config_ai_model_audits(actor_id,version) VALUES($1,$2)`, actor, row.Version)
	return err
}
