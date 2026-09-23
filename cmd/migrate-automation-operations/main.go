// Command migrate-automation-operations performs the controlled, encrypted
// legacy Automation Operations migration. It is intentionally outside the
// runtime composition root and never performs provider writes.
package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	segmentmigration "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/migration"
)

const snapshotMagic = "AICRM-AUTOMATION-OPERATIONS-V2-SNAPSHOT-1\n"

func main() {
	if err := execute(os.Args[1:], os.Stdout); err != nil {
		slog.Error("automation operations migration failed", "error", err)
		os.Exit(1)
	}
}

func execute(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("command required: keygen, inspect, extract, validate, dry-run, apply, replay-check, reconcile, shadow, rollback")
	}
	switch args[0] {
	case "keygen":
		flags := newFlags("keygen")
		keyFile := flags.String("key-file", "", "new key file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *keyFile == "" {
			return errors.New("key-file is required")
		}
		return generateKey(*keyFile)
	case "inspect":
		flags, sourceSystem, sourceEnv, timeout := sourceFlags("inspect")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return withPool(*sourceEnv, *timeout, func(ctx context.Context, pool *pgxpool.Pool) error {
			report, err := segmentmigration.Inspect(ctx, pool, *sourceSystem)
			if err != nil {
				return err
			}
			return writeJSON(output, report)
		})
	case "extract":
		flags, sourceSystem, sourceEnv, timeout := sourceFlags("extract")
		snapshotFile := flags.String("snapshot-file", "", "new encrypted snapshot file")
		keyFile := flags.String("key-file", "", "0600 AES-256 key file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *snapshotFile == "" || *keyFile == "" {
			return errors.New("snapshot-file and key-file are required")
		}
		return withPool(*sourceEnv, *timeout, func(ctx context.Context, pool *pgxpool.Pool) error {
			snapshot, report, err := segmentmigration.Extract(ctx, pool, *sourceSystem)
			if err != nil {
				return err
			}
			if err = writeEncryptedSnapshot(*snapshotFile, *keyFile, snapshot); err != nil {
				return err
			}
			return writeJSON(output, report)
		})
	case "validate":
		flags := newFlags("validate")
		snapshotFile := flags.String("snapshot-file", "", "encrypted snapshot file")
		keyFile := flags.String("key-file", "", "0600 AES-256 key file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		snapshot, err := readEncryptedSnapshot(*snapshotFile, *keyFile)
		if err != nil {
			return err
		}
		return writeJSON(output, map[string]any{"valid": true, "source_system": snapshot.Manifest.SourceSystem, "snapshot_at": snapshot.Manifest.SnapshotAt, "counts": snapshot.Manifest.Counts})
	case "dry-run", "apply", "replay-check":
		command := args[0]
		flags, snapshotFile, keyFile, targetEnv, actorID, timeout := importFlags(command)
		confirm := flags.Bool("confirm-import", false, "confirm durable import")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if command == "apply" && !*confirm {
			return errors.New("apply requires confirm-import")
		}
		snapshot, err := readEncryptedSnapshot(*snapshotFile, *keyFile)
		if err != nil {
			return err
		}
		dryRun := command != "apply"
		return withPool(*targetEnv, *timeout, func(ctx context.Context, pool *pgxpool.Pool) error {
			dependencies := Dependencies{ActorID: *actorID, Identity: identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, Access: accessstore.NewPostgreSQL()}
			report, importErr := Import(ctx, pool, snapshot, dependencies, dryRun)
			if importErr != nil {
				return importErr
			}
			if command == "replay-check" && (report.ProviderEffectsCreated != 0 || report.RiverJobsCreated != 0) {
				return errors.New("replay created side effects")
			}
			return writeJSON(output, report)
		})
	case "reconcile":
		flags, targetEnv, timeout := targetFlags("reconcile")
		batchKey := flags.String("batch-key", "", "migration batch key")
		snapshotFile := flags.String("snapshot-file", "", "encrypted snapshot file originally used for import")
		keyFile := flags.String("key-file", "", "0600 AES-256 key file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		snapshot, err := readEncryptedSnapshot(*snapshotFile, *keyFile)
		if err != nil {
			return err
		}
		return withPool(*targetEnv, *timeout, func(ctx context.Context, pool *pgxpool.Pool) error {
			report, err := Reconcile(ctx, pool, *batchKey, snapshot)
			if err != nil {
				return err
			}
			return writeJSON(output, report)
		})
	case "shadow":
		flags, targetEnv, timeout := targetFlags("shadow")
		batchKey := flags.String("batch-key", "", "migration batch key")
		snapshotFile := flags.String("snapshot-file", "", "encrypted snapshot file")
		keyFile := flags.String("key-file", "", "0600 AES-256 key file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		snapshot, err := readEncryptedSnapshot(*snapshotFile, *keyFile)
		if err != nil {
			return err
		}
		return withPool(*targetEnv, *timeout, func(ctx context.Context, pool *pgxpool.Pool) error {
			report, shadowErr := Shadow(ctx, pool, *batchKey, snapshot)
			if shadowErr != nil {
				return shadowErr
			}
			if err = writeJSON(output, report); err != nil {
				return err
			}
			if !report.ReadyForProbe {
				return errors.New("shadow comparison is not ready for a controlled probe")
			}
			return nil
		})
	case "rollback":
		flags, targetEnv, timeout := targetFlags("rollback")
		batchKey := flags.String("batch-key", "", "migration batch key")
		confirm := flags.Bool("confirm-rollback", false, "confirm non-destructive pause")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*batchKey) == "" || !*confirm {
			return errors.New("batch-key and confirm-rollback are required")
		}
		return withPool(*targetEnv, *timeout, func(ctx context.Context, pool *pgxpool.Pool) error {
			if err := Rollback(ctx, pool, *batchKey, *confirm); err != nil {
				return err
			}
			return writeJSON(output, map[string]any{"batch_key": *batchKey, "status": "rolled_back", "destructive_delete": false})
		})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func newFlags(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func sourceFlags(name string) (*flag.FlagSet, *string, *string, *time.Duration) {
	flags := newFlags(name)
	sourceSystem := flags.String("source-system", "", "stable source system name")
	sourceEnv := flags.String("source-url-env", "AICRM_V2_AUTOMATION_DATABASE_URL", "environment variable containing source database URL")
	timeout := flags.Duration("timeout", 10*time.Minute, "overall timeout")
	return flags, sourceSystem, sourceEnv, timeout
}

func targetFlags(name string) (*flag.FlagSet, *string, *time.Duration) {
	flags := newFlags(name)
	targetEnv := flags.String("target-url-env", "AICRM_DATABASE_URL", "environment variable containing target database URL")
	timeout := flags.Duration("timeout", 10*time.Minute, "overall timeout")
	return flags, targetEnv, timeout
}

func importFlags(name string) (*flag.FlagSet, *string, *string, *string, *int64, *time.Duration) {
	flags, targetEnv, timeout := targetFlags(name)
	snapshotFile := flags.String("snapshot-file", "", "encrypted snapshot file")
	keyFile := flags.String("key-file", "", "0600 AES-256 key file")
	actorID := flags.Int64("actor-id", 0, "active target administrator ID recorded on imported configuration")
	return flags, snapshotFile, keyFile, targetEnv, actorID, timeout
}

func withPool(urlEnv string, timeout time.Duration, fn func(context.Context, *pgxpool.Pool) error) error {
	if strings.TrimSpace(urlEnv) == "" || timeout <= 0 {
		return errors.New("database URL environment name and positive timeout are required")
	}
	databaseURL, err := platformconfig.NamedDatabaseURL(urlEnv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return errors.New("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["application_name"] = "aicrm-automation-operations-migration"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return errors.New("open database")
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return errors.New("connect database")
	}
	return fn(ctx, pool)
}

func generateKey(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("key-file is required")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return errors.New("generate snapshot key")
	}
	contents := []byte(base64.RawURLEncoding.EncodeToString(key) + "\n")
	return writeExclusive(path, contents)
}

func loadKey(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("key-file is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("read snapshot key metadata")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("snapshot key file must not be accessible by group or others")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read snapshot key")
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("snapshot key must contain one base64url AES-256 key")
	}
	return key, nil
}

func writeEncryptedSnapshot(path, keyPath string, snapshot segmentmigration.Snapshot) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("snapshot-file is required")
	}
	key, err := loadKey(keyPath)
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(snapshot)
	if err != nil {
		return errors.New("encode snapshot")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return errors.New("initialize snapshot encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return errors.New("initialize snapshot authentication")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return errors.New("generate snapshot nonce")
	}
	sealed := aead.Seal(nil, nonce, plaintext, []byte(snapshotMagic))
	payload := append([]byte(snapshotMagic), nonce...)
	payload = append(payload, sealed...)
	return writeExclusive(path, payload)
}

func readEncryptedSnapshot(path, keyPath string) (segmentmigration.Snapshot, error) {
	if strings.TrimSpace(path) == "" {
		return segmentmigration.Snapshot{}, errors.New("snapshot-file is required")
	}
	key, err := loadKey(keyPath)
	if err != nil {
		return segmentmigration.Snapshot{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return segmentmigration.Snapshot{}, errors.New("read encrypted snapshot")
	}
	if len(payload) <= len(snapshotMagic) || string(payload[:len(snapshotMagic)]) != snapshotMagic {
		return segmentmigration.Snapshot{}, errors.New("invalid encrypted snapshot header")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return segmentmigration.Snapshot{}, errors.New("initialize snapshot decryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return segmentmigration.Snapshot{}, errors.New("initialize snapshot authentication")
	}
	if len(payload) < len(snapshotMagic)+aead.NonceSize()+aead.Overhead() {
		return segmentmigration.Snapshot{}, errors.New("encrypted snapshot is truncated")
	}
	offset := len(snapshotMagic)
	plaintext, err := aead.Open(nil, payload[offset:offset+aead.NonceSize()], payload[offset+aead.NonceSize():], []byte(snapshotMagic))
	if err != nil {
		return segmentmigration.Snapshot{}, errors.New("authenticate encrypted snapshot")
	}
	var snapshot segmentmigration.Snapshot
	if err = json.Unmarshal(plaintext, &snapshot); err != nil {
		return segmentmigration.Snapshot{}, errors.New("decode snapshot")
	}
	if err = segmentmigration.ValidateSnapshot(snapshot); err != nil {
		return segmentmigration.Snapshot{}, err
	}
	return snapshot, nil
}

func writeExclusive(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errors.New("create output directory")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("create protected output file")
	}
	name := file.Name()
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(name)
		}
	}()
	if _, err = file.Write(contents); err != nil {
		return errors.New("write protected output file")
	}
	if err = file.Sync(); err != nil {
		return errors.New("sync protected output file")
	}
	if err = file.Close(); err != nil {
		return errors.New("close protected output file")
	}
	committed = true
	return nil
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
