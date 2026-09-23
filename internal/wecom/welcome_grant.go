package wecom

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var ErrWelcomeGrantUnavailable = errors.New("welcome grant unavailable")

type WelcomeGrantStore interface {
	Seal(context.Context, string, string, time.Time) (string, error)
}
type WelcomeGrantRedeemer interface {
	Redeem(context.Context, string, string) (string, error)
}
type WelcomeGrantCipher struct{ aead cipher.AEAD }

func NewWelcomeGrantCipher(secret string) (*WelcomeGrantCipher, error) {
	if len(secret) < 32 {
		return nil, ErrWelcomeGrantUnavailable
	}
	key := sha256.Sum256([]byte("aicrm.wecom.welcome-grant.v1\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, ErrWelcomeGrantUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrWelcomeGrantUnavailable
	}
	return &WelcomeGrantCipher{aead: aead}, nil
}
func (ciphertext *WelcomeGrantCipher) encrypt(value string, associatedData []byte) ([]byte, error) {
	nonce := make([]byte, ciphertext.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return ciphertext.aead.Seal(nonce, nonce, []byte(value), associatedData), nil
}
func (ciphertext *WelcomeGrantCipher) decrypt(value, associatedData []byte) (string, error) {
	size := ciphertext.aead.NonceSize()
	if len(value) <= size {
		return "", ErrWelcomeGrantUnavailable
	}
	plain, err := ciphertext.aead.Open(nil, value[:size], value[size:], associatedData)
	if err != nil {
		return "", ErrWelcomeGrantUnavailable
	}
	return string(plain), nil
}

type PostgreSQLWelcomeGrantStore struct{ cipher *WelcomeGrantCipher }

func NewPostgreSQLWelcomeGrantStore(cipher *WelcomeGrantCipher) *PostgreSQLWelcomeGrantStore {
	return &PostgreSQLWelcomeGrantStore{cipher: cipher}
}
func (store *PostgreSQLWelcomeGrantStore) Seal(ctx context.Context, callbackKey, value string, expires time.Time) (string, error) {
	if store == nil || store.cipher == nil || value == "" || len(value) > 4096 || strings.TrimSpace(value) != value || expires.Before(time.Now().UTC()) {
		return "", ErrWelcomeGrantUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", err
	}
	// A duplicated Provider delivery may race before its callback intent row is
	// visible. Serialize grant sealing by the opaque callback key so both paths
	// reuse the first sealed grant rather than surfacing a unique-key error.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "wecom.welcome-grant.v1:"+callbackKey); err != nil {
		return "", err
	}
	callback := sha256.Sum256([]byte(callbackKey))
	valueDigest := sha256.Sum256([]byte(value))
	var id int64
	var existingDigest []byte
	err = tx.QueryRow(ctx, `SELECT id,value_digest FROM wecom_welcome_grants WHERE callback_digest=$1`, callback[:]).Scan(&id, &existingDigest)
	if err == nil {
		if len(existingDigest) != sha256.Size || string(existingDigest) != string(valueDigest[:]) {
			return "", ErrWelcomeGrantUnavailable
		}
		return "wgrant_" + formatGrantID(id), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	encrypted, err := store.cipher.encrypt(value, callback[:])
	if err != nil {
		return "", err
	}
	err = tx.QueryRow(ctx, `INSERT INTO wecom_welcome_grants(callback_digest,value_digest,ciphertext,expires_at) VALUES($1,$2,$3,$4) RETURNING id`, callback[:], valueDigest[:], encrypted, expires.UTC()).Scan(&id)
	if err != nil {
		return "", err
	}
	return "wgrant_" + formatGrantID(id), nil
}
func (store *PostgreSQLWelcomeGrantStore) Redeem(ctx context.Context, reference, effectRef string) (string, error) {
	id, ok := parseGrantRef(reference)
	if !ok || !strings.HasPrefix(effectRef, "eer_") {
		return "", ErrWelcomeGrantUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", err
	}
	var encrypted, callbackDigest, valueDigest []byte
	err = tx.QueryRow(ctx, `UPDATE wecom_welcome_grants SET consumed_at=clock_timestamp(),consumer_effect_ref=$2 WHERE id=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp() RETURNING ciphertext,callback_digest,value_digest`, id, effectRef).Scan(&encrypted, &callbackDigest, &valueDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		// A retry of the same durable effect may redeem its own grant again only
		// while it is still live. A different effect always fails closed.
		err = tx.QueryRow(ctx, `SELECT ciphertext,callback_digest,value_digest FROM wecom_welcome_grants WHERE id=$1 AND consumer_effect_ref=$2 AND expires_at>clock_timestamp()`, id, effectRef).Scan(&encrypted, &callbackDigest, &valueDigest)
	}
	if err != nil {
		var expiresAt time.Time
		lookupErr := tx.QueryRow(ctx, `SELECT expires_at FROM wecom_welcome_grants WHERE id=$1`, id).Scan(&expiresAt)
		if lookupErr == nil && !expiresAt.After(time.Now().UTC()) {
			return "", wecomport.ErrWelcomeGrantExpired
		}
		return "", ErrWelcomeGrantUnavailable
	}
	plain, err := store.cipher.decrypt(encrypted, callbackDigest)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(plain))
	if len(valueDigest) != sha256.Size || string(valueDigest) != string(digest[:]) {
		return "", ErrWelcomeGrantUnavailable
	}
	return plain, nil
}
func formatGrantID(id int64) string {
	return strings.TrimLeft(hex.EncodeToString([]byte{byte(id >> 56), byte(id >> 48), byte(id >> 40), byte(id >> 32), byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}), "0")
}
func parseGrantRef(value string) (int64, bool) {
	raw := strings.TrimPrefix(value, "wgrant_")
	if raw == "" || raw == value {
		return 0, false
	}
	decoded, err := hex.DecodeString(func() string {
		if len(raw)%2 == 1 {
			return "0" + raw
		}
		return raw
	}())
	if err != nil || len(decoded) > 8 {
		return 0, false
	}
	var id int64
	for _, b := range decoded {
		id = id<<8 | int64(b)
	}
	return id, id > 0
}
