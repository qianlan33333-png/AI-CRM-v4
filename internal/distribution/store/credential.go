package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

const credentialColumns = `id,distributor_id,product_id,product_type,token_digest,status,created_at,expires_at,revoked_at`

func scanPromotionCredential(row rowScanner) (distributiondomain.PromotionCredential, error) {
	var credential distributiondomain.PromotionCredential
	var productType, status string
	var raw []byte
	err := row.Scan(&credential.ID, &credential.DistributorID, &credential.ProductID, &productType, &raw, &status, &credential.CreatedAt, &credential.ExpiresAt, &credential.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return distributiondomain.PromotionCredential{}, distributionport.ErrNotFound
	}
	if err != nil {
		return distributiondomain.PromotionCredential{}, mapError(err)
	}
	if len(raw) != 32 {
		return distributiondomain.PromotionCredential{}, distributionport.ErrUnavailable
	}
	credential.ProductType = distributiondomain.ProductType(productType)
	credential.Status = distributiondomain.CredentialStatus(status)
	copy(credential.TokenDigest[:], raw)
	if !credential.Valid() {
		return distributiondomain.PromotionCredential{}, distributionport.ErrUnavailable
	}
	return credential, nil
}

func (r *Repository) InsertPromotionCredentialWithin(ctx context.Context, credential distributiondomain.PromotionCredential) (distributiondomain.PromotionCredential, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.PromotionCredential{}, err
	}
	if !credential.ValidForInsert() {
		return distributiondomain.PromotionCredential{}, ErrInvalid
	}
	return scanPromotionCredential(tx.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+credentialColumns, credential.DistributorID, credential.ProductID, string(credential.ProductType), credential.TokenDigest[:], string(credential.Status), credential.CreatedAt.UTC(), credential.ExpiresAt.UTC()))
}

func (r *Repository) ReadPromotionCredentialByDigestWithin(ctx context.Context, digest [32]byte, lock bool) (distributiondomain.PromotionCredential, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.PromotionCredential{}, err
	}
	query := `SELECT ` + credentialColumns + ` FROM distribution_promotion_credentials WHERE token_digest=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanPromotionCredential(tx.QueryRow(ctx, query, digest[:]))
}

func (r *Repository) ReadPromotionCredentialWithin(ctx context.Context, credentialID int64, lock bool) (distributiondomain.PromotionCredential, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.PromotionCredential{}, err
	}
	if credentialID < 1 {
		return distributiondomain.PromotionCredential{}, ErrInvalid
	}
	query := `SELECT ` + credentialColumns + ` FROM distribution_promotion_credentials WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanPromotionCredential(tx.QueryRow(ctx, query, credentialID))
}

func (r *Repository) ExpirePromotionCredentialWithin(ctx context.Context, credential distributiondomain.PromotionCredential, at time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if credential.ID < 1 || credential.Status != distributiondomain.CredentialActive || at.IsZero() || at.Before(credential.ExpiresAt) {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `UPDATE distribution_promotion_credentials SET status='expired' WHERE id=$1 AND status='active'`, credential.ID)
	return mapError(err)
}
