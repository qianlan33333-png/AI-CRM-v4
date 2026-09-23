package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
)

type options struct {
	mode, snapshot, report, manifestDigest, unionIDScope, wecomCorpID, sourceStream, sourceHost string
	actorID                                                                                     int64
	confirm                                                                                     bool
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "channel history migration failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	migrationConfig, err := platformconfig.LoadChannelHistoryMigration()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("migrate-channel-history", flag.ContinueOnError)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "inspect|inspect-stream|validate|dry-run|import|reconcile|replay-check|rollback|semantic-validate|sync-wecom-staff|semantic-repair|semantic-reconcile|verify-legacy-assets|activate-repaired")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "encrypted snapshot path")
	flags.StringVar(&cfg.report, "report", "", "optional schema discovery report path")
	flags.StringVar(&cfg.sourceStream, "source-stream", "", "trusted read-only psql stream path for inspect-stream")
	flags.StringVar(&cfg.sourceHost, "source-host", "", "source hostname used only to derive the snapshot host digest")
	flags.StringVar(&cfg.manifestDigest, "manifest-sha256", "", "required manifest digest for mutating modes")
	flags.StringVar(&cfg.unionIDScope, "unionid-scope", "", "verified OneID scope, for example wechat-open-platform:main")
	flags.StringVar(&cfg.wecomCorpID, "wecom-corp-id", migrationConfig.WeComCorpID, "WeCom corp scope used only to resolve existing historical external_userid identities")
	flags.Int64Var(&cfg.actorID, "actor-id", 0, "active migration administrator id; zero selects the first active superadmin")
	flags.BoolVar(&cfg.confirm, "confirm", false, "confirm import or rollback")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if cfg.mode == "inspect" {
		return inspectSource(ctx, cfg)
	}
	if cfg.mode == "inspect-stream" {
		return inspectSourceStream(cfg)
	}
	if cfg.snapshot == "" {
		return errors.New("--snapshot is required")
	}
	key, err := snapshotKeyFromEnvironment()
	if err != nil {
		return err
	}
	manifest, err := loadEncryptedSnapshot(cfg.snapshot, key)
	if err != nil {
		return err
	}
	if err = manifest.Validate(); err != nil {
		return err
	}
	if cfg.mode == "validate" {
		return printJSON(map[string]any{"mode": cfg.mode, "valid": true, "snapshot_id": manifest.SnapshotID, "manifest_sha256": manifest.DigestHex(), "summary": manifest.Summary()})
	}
	if cfg.mode == "dry-run" {
		return printJSON(map[string]any{"mode": cfg.mode, "eligible": true, "provider_calls": 0, "provider_effects": 0, "snapshot_id": manifest.SnapshotID, "manifest_sha256": manifest.DigestHex(), "summary": manifest.Summary()})
	}
	if cfg.mode == "semantic-validate" {
		result, validationErr := validateSemantics(manifest)
		if validationErr != nil {
			return validationErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "valid": true, "result": result, "provider_calls": 0, "provider_effects": 0})
	}
	if !strings.EqualFold(cfg.manifestDigest, manifest.DigestHex()) {
		return errors.New("manifest digest confirmation mismatch")
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 10, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	mediaRepository, err := mediastore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	runner := importRunner{Pool: pool, UOW: uow, Resolver: identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, UnionIDScope: cfg.unionIDScope, WeComCorpID: cfg.wecomCorpID, ActorID: cfg.actorID, Media: mediaRepository, States: channelstore.NewPostgreSQLStore()}
	switch cfg.mode {
	case "sync-wecom-staff":
		if !cfg.confirm {
			return errors.New("sync-wecom-staff requires --confirm")
		}
		if !migrationConfig.ProviderEnabled || !migrationConfig.ProviderReadEnabled {
			return errors.New("channel provider directory read is disabled")
		}
		provider, providerErr := wecomadapter.NewDirectory(wecomadapter.Config{
			Enabled: true, CorpID: migrationConfig.WeComCorpID, ContactSecret: migrationConfig.WeComContactSecret,
		})
		if providerErr != nil {
			return providerErr
		}
		audit, auditErr := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
		if auditErr != nil {
			return auditErr
		}
		projector, projectorErr := accessapp.NewWeComStaffProjector(accessstore.NewPostgreSQL(), credential.PasswordHasher{}, audit)
		if projectorErr != nil {
			return projectorErr
		}
		runKey := "migration-" + manifest.SnapshotID
		service := wecom.StaffDirectoryRefreshService{Enabled: true, Provider: provider, Projector: projector, DisplayNames: sourceStaffDisplayNames(manifest), Store: wecom.NewPostgreSQLStaffDirectoryRefreshStore(), Audit: audit, UOW: uow}
		if refreshErr := service.Refresh(ctx, runKey, "manual", true); refreshErr != nil {
			return refreshErr
		}
		var state string
		var discovered, created, existing, inactive int64
		if queryErr := pool.Native().QueryRow(ctx, `SELECT state,discovered_count,created_count,existing_count,inactive_count FROM wecom_staff_directory_refresh_runs WHERE run_key=$1`, runKey).Scan(&state, &discovered, &created, &existing, &inactive); queryErr != nil {
			return queryErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "state": state, "discovered": discovered, "created": created, "existing": existing, "inactive": inactive, "provider_reads": 1, "provider_writes": 0, "provider_effects": 0})
	case "import":
		if !cfg.confirm {
			return errors.New("import requires --confirm")
		}
		result, applyErr := runner.Import(ctx, manifest)
		if applyErr != nil {
			return applyErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "reconcile":
		result, reconcileErr := runner.Reconcile(ctx, manifest)
		if reconcileErr != nil {
			return reconcileErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "replay-check":
		result, replayErr := runner.ReplayCheck(ctx, manifest)
		if replayErr != nil {
			return replayErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "rollback":
		if !cfg.confirm {
			return errors.New("rollback requires --confirm")
		}
		result, rollbackErr := runner.Rollback(ctx, manifest)
		if rollbackErr != nil {
			return rollbackErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "semantic-repair":
		if !cfg.confirm {
			return errors.New("semantic-repair requires --confirm")
		}
		if _, err = runner.Import(ctx, manifest); err != nil {
			return err
		}
		result, repairErr := runner.SemanticRepair(ctx, manifest)
		if repairErr != nil {
			return repairErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "verify-legacy-assets":
		if !cfg.confirm {
			return errors.New("verify-legacy-assets requires --confirm")
		}
		if !migrationConfig.ProviderEnabled || !migrationConfig.ProviderReadEnabled {
			return errors.New("channel provider readback is disabled")
		}
		corpID := migrationConfig.WeComCorpID
		digester, digestErr := wecom.NewHMACStateDigester([]byte(migrationConfig.ChannelStateHMACKey))
		if digestErr != nil {
			return digestErr
		}
		client, clientErr := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: corpID, ContactSecret: migrationConfig.WeComContactSecret})
		if clientErr != nil {
			return clientErr
		}
		result, verifyErr := runner.VerifyLegacyAssets(ctx, manifest, client, digester, corpID)
		if verifyErr != nil {
			return verifyErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "semantic-reconcile":
		result, reconcileErr := runner.SemanticReconcile(ctx, manifest)
		if reconcileErr != nil {
			return reconcileErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "activate-repaired":
		if !cfg.confirm {
			return errors.New("activate-repaired requires --confirm")
		}
		result, activateErr := runner.ActivateRepaired(ctx, manifest)
		if activateErr != nil {
			return activateErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	default:
		return errors.New("unsupported mode")
	}
}

func sourceStaffDisplayNames(manifest snapshotManifest) map[string]string {
	result := map[string]string{}
	for _, row := range mustTable(manifest, "automation_channel_assignee").Rows {
		providerID := firstString(row.Payload, "staff_id")
		displayName := firstString(row.Payload, "display_name_snapshot")
		if providerID != "" && displayName != "" {
			if _, exists := result[providerID]; !exists {
				result[providerID] = displayName
			}
		}
	}
	return result
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
