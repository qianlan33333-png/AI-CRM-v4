// Command migrate-identity-phone-vault encrypts legacy E.164 phone identities
// already owned by OneID. It never creates Customers or changes ownership.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var cn11 = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

type report struct {
	Mode                 string `json:"mode"`
	RunKey               string `json:"run_key"`
	LegacyPlaintext      int64  `json:"legacy_plaintext"`
	Protected            int64  `json:"protected"`
	Secrets              int64  `json:"secrets"`
	ProjectionMismatches int64  `json:"projection_mismatches"`
	Source               int64  `json:"source"`
	Migrated             int64  `json:"migrated"`
	CustomersBefore      int64  `json:"customers_before"`
	CustomersAfter       int64  `json:"customers_after"`
}

type row struct {
	id, customerID int64
	phone          string
	assurance      string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "phone vault migration failed")
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "inspect", "inspect|apply|reconcile")
	runKey := flag.String("run-key", "identity-phone-vault-v1", "stable non-secret run key")
	confirm := flag.Bool("confirm-apply", false, "required for apply")
	flag.Parse()
	if *mode != "inspect" && *mode != "apply" && *mode != "reconcile" {
		return errors.New("invalid mode")
	}
	if *runKey == "" || len(*runKey) > 128 {
		return errors.New("invalid run key")
	}
	if *mode == "apply" && !*confirm {
		return errors.New("confirm-apply is required")
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	phoneDataKey, err := platformconfig.IdentityPhoneDataKey()
	if err != nil {
		return err
	}
	vault, err := identitysecure.NewPhoneVault(phoneDataKey)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 2, ConnectTimeout: 5 * time.Second, HealthTimeout: 3 * time.Second})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	projection := customerstore.NewPostgreSQL()
	if *mode == "apply" {
		err = uow.Within(ctx, func(txCtx context.Context) error {
			tx, txErr := platformpostgres.RequireTransaction(txCtx)
			if txErr != nil {
				return txErr
			}
			var existingStatus string
			txErr = tx.QueryRow(txCtx, `SELECT status FROM identity_phone_vault_migration_runs WHERE run_key=$1 FOR UPDATE`, *runKey).Scan(&existingStatus)
			if txErr == nil && existingStatus == "applied" {
				return nil
			}
			if txErr != nil && !errors.Is(txErr, pgx.ErrNoRows) {
				return txErr
			}
			rows, queryErr := tx.Query(txCtx, `SELECT id,customer_id,normalized_value,assurance FROM customer_identities WHERE kind='phone' AND scope_key='phone:e164' AND status='active' ORDER BY id FOR UPDATE`)
			if queryErr != nil {
				return queryErr
			}
			items := []row{}
			for rows.Next() {
				var item row
				if queryErr = rows.Scan(&item.id, &item.customerID, &item.phone, &item.assurance); queryErr != nil {
					rows.Close()
					return queryErr
				}
				items = append(items, item)
			}
			queryErr = rows.Err()
			rows.Close()
			if queryErr != nil {
				return queryErr
			}
			var customersBefore int64
			if queryErr = tx.QueryRow(txCtx, `SELECT count(*) FROM customers`).Scan(&customersBefore); queryErr != nil {
				return queryErr
			}
			if errors.Is(txErr, pgx.ErrNoRows) {
				if _, queryErr = tx.Exec(txCtx, `INSERT INTO identity_phone_vault_migration_runs(run_key,source_count,customer_count_before,status) VALUES($1,$2,$3,'applying')`, *runKey, len(items), customersBefore); queryErr != nil {
					return queryErr
				}
			}
			migrated := int64(0)
			for _, item := range items {
				if len(item.phone) != 14 || item.phone[:3] != "+86" || !cn11.MatchString(item.phone[3:]) {
					return errors.New("legacy phone is not a valid mainland identity")
				}
				phone := item.phone[3:]
				digest := vault.LookupDigest(phone)
				var conflictID int64
				queryErr = tx.QueryRow(txCtx, `SELECT id FROM customer_identities WHERE kind='phone' AND scope_key='phone:cn11' AND normalized_value_digest=$1 AND status='active' AND id<>$2`, digest[:], item.id).Scan(&conflictID)
				if queryErr == nil {
					return errors.New("phone ownership conflict")
				}
				if !errors.Is(queryErr, pgx.ErrNoRows) {
					return queryErr
				}
				ciphertext, encryptErr := vault.Encrypt(phone)
				if encryptErr != nil {
					return encryptErr
				}
				if _, queryErr = tx.Exec(txCtx, `INSERT INTO identity_phone_secrets(identity_id,ciphertext,masked_value,key_version) VALUES($1,$2,$3,$4) ON CONFLICT(identity_id) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,masked_value=EXCLUDED.masked_value,key_version=EXCLUDED.key_version,updated_at=CURRENT_TIMESTAMP`, item.id, ciphertext, identitysecure.MaskPhone(phone), identitysecure.PhoneKeyVersion); queryErr != nil {
					return queryErr
				}
				if _, queryErr = tx.Exec(txCtx, `UPDATE customer_identities SET scope_key='phone:cn11',normalized_value='',normalized_value_digest=$2,normalizer_version=2,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, item.id, digest[:]); queryErr != nil {
					return queryErr
				}
				if queryErr = projection.UpdateDirectoryPhone(txCtx, customerdomain.CustomerID(item.customerID), identitysecure.MaskPhone(phone), assuranceValue(item.assurance), 1, time.Now().UTC()); queryErr != nil {
					return queryErr
				}
				migrated++
			}
			var customersAfter int64
			if queryErr = tx.QueryRow(txCtx, `SELECT count(*) FROM customers`).Scan(&customersAfter); queryErr != nil {
				return queryErr
			}
			if customersAfter != customersBefore {
				return errors.New("customer count changed during phone migration")
			}
			_, queryErr = tx.Exec(txCtx, `UPDATE identity_phone_vault_migration_runs SET migrated_count=$2,customer_count_after=$3,status='applied',completed_at=CURRENT_TIMESTAMP WHERE run_key=$1`, *runKey, migrated, customersAfter)
			return queryErr
		})
		if err != nil {
			return err
		}
	}
	result := report{Mode: *mode, RunKey: *runKey}
	err = uow.Within(ctx, func(txCtx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txCtx)
		if txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txCtx, `SELECT count(*) FROM customer_identities WHERE kind='phone' AND normalized_value<>''`).Scan(&result.LegacyPlaintext); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txCtx, `SELECT count(*) FROM customer_identities WHERE kind='phone' AND scope_key='phone:cn11' AND octet_length(normalized_value_digest)=32`).Scan(&result.Protected); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txCtx, `SELECT count(*) FROM identity_phone_secrets`).Scan(&result.Secrets); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txCtx, `SELECT count(*) FROM (SELECT DISTINCT i.customer_id FROM customer_identities i JOIN identity_phone_secrets s ON s.identity_id=i.id WHERE i.kind='phone' AND i.scope_key='phone:cn11' AND i.status='active') phones WHERE NOT EXISTS (SELECT 1 FROM customer_directory_projection p JOIN customer_identities i ON i.customer_id=p.customer_id JOIN identity_phone_secrets s ON s.identity_id=i.id WHERE i.customer_id=phones.customer_id AND i.kind='phone' AND i.status='active' AND p.phone_masked=s.masked_value)`).Scan(&result.ProjectionMismatches); txErr != nil {
			return txErr
		}
		_ = tx.QueryRow(txCtx, `SELECT source_count,migrated_count,customer_count_before,COALESCE(customer_count_after,0) FROM identity_phone_vault_migration_runs WHERE run_key=$1`, *runKey).Scan(&result.Source, &result.Migrated, &result.CustomersBefore, &result.CustomersAfter)
		return nil
	})
	if err != nil {
		return err
	}
	if *mode == "reconcile" && (result.LegacyPlaintext != 0 || result.Protected != result.Secrets || result.Source != result.Protected || result.Source != result.Migrated || result.ProjectionMismatches != 0 || result.CustomersBefore == 0 || result.CustomersBefore != result.CustomersAfter) {
		return errors.New("phone vault reconciliation failed")
	}
	encoded, _ := json.Marshal(result)
	fmt.Println(string(encoded))
	return nil
}

func assuranceValue(raw string) identitydomain.Assurance {
	if raw == "verified" {
		return identitydomain.AssuranceVerified
	}
	return identitydomain.AssuranceDeclared
}
