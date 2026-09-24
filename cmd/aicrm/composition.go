package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/adminops"
	adminopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/app"
	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	adminopsprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/provider"
	adminopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/store"
	aiassistant "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiexcel "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/excel"
	aiassistanthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/http"
	aiassistantstore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	automation "github.com/qianlan33333-png/AI-CRM-v3/internal/automation"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/http"
	automationprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/provider"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/config/module"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	coupon "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/http"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/http"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributionhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/http"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupops "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	hxc "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard"
	hxcapp "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/app"
	hxchttp "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/http"
	hxcprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/provider"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	hxcworker "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/worker"
	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/http"
	identityprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/provider"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	media "github.com/qianlan33333-png/AI-CRM-v3/internal/media"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	groupopsmaterial "github.com/qianlan33333-png/AI-CRM-v3/internal/media/groupopsmaterial"
	mediahttp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/http"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	archiveapp "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	archivehttp "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/http"
	archivestore "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/store"
	openplatformhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/http"
	operationcycle "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	operationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/store"
	orderui "github.com/qianlan33333-png/AI-CRM-v3/internal/order"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/http"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	payment "github.com/qianlan33333-png/AI-CRM-v3/internal/payment"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymenth5oauth "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/h5oauth"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformdiagnostics "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/diagnostics"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/hostmaintenance"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	platformruntime "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/runtime"
	platformwebhook "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	productmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/product"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	producthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/http"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
	radarapp "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/app"
	radarmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/module"
	radarprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/provider"
	radarstore "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/store"
	referralapp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/app"
	referralhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/http"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
	releaseapp "github.com/qianlan33333-png/AI-CRM-v3/internal/release/app"
	releaseport "github.com/qianlan33333-png/AI-CRM-v3/internal/release/port"
	segment "github.com/qianlan33333-png/AI-CRM-v3/internal/segment"
	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/http"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/sidebar"
	surveymodule "github.com/qianlan33333-png/AI-CRM-v3/internal/survey"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/provider"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	tag "github.com/qianlan33333-png/AI-CRM-v3/internal/tag"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/webshell"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
)

type composedApplication struct {
	invitationService     *mediaapp.InvitationService
	catalogService        *groupopsapp.CatalogService
	pool                  *platformpostgres.Pool
	handler               http.Handler
	authentication        accessAuthentication
	management            *accessapp.Management
	weComProcessor        wecom.InboxProcessor
	weComArchiveProcessor wecom.ArchiveInboxProcessor
	effectsRuntime        *platformjobqueue.Runtime
	// paymentDistribution is the already-composed Payment completion sink. It
	// remains unexported and is retained so same-package PostgreSQL journeys can
	// exercise an EER terminal callback through the exact Production observer
	// wiring without contacting a Provider.
	paymentDistribution effectport.CompletionSink
	// paymentReconciliation is retained only for the explicit one-shot
	// payment-reconcile runtime role.  It is the same fully composed service as
	// the public callback and River worker path, rather than an operations-only
	// store shortcut.
	paymentReconciliation *paymentapp.Service
	// paymentSession remains private to the Composition Root. Same-package
	// PostgreSQL browser journeys use it only to issue a provider-verified test
	// session through the exact OneID-backed session service before exercising
	// public read paths.
	paymentSession        *paymentsession.Service
	channelEntrantActions *channelstore.EntrantActionStore
	customerSync          wecom.CustomerSyncService
	adminOps              *adminopsapp.ProjectionService
	release               *releaseapp.ObservationService
	diagnostics           *adminopsapp.DiagnosticsService
	hxcDashboard          hxcapp.Service
	hxcSource             *hxcprovider.MySQL
}

func compose(ctx context.Context, cfg platformconfig.Runtime) (*composedApplication, error) {
	return composeWithWeComClientFactoryAndSurveyCompletionHTTPClient(ctx, cfg, wecomadapter.New, nil, outbound.SurveyCompletionNetwork{})
}

func composeWithWeComClientFactory(ctx context.Context, cfg platformconfig.Runtime, providerFactory func(wecomadapter.Config) (*wecomadapter.Client, error)) (*composedApplication, error) {
	return composeWithWeComClientFactoryAndSurveyCompletionHTTPClient(ctx, cfg, providerFactory, nil, outbound.SurveyCompletionNetwork{})
}

func weComProviderConfig(cfg platformconfig.Runtime) wecomadapter.Config {
	return wecomadapter.Config{
		Enabled: cfg.WeCom.Enabled, CorpID: cfg.WeCom.CorpID, AgentID: cfg.WeCom.AgentID, Secret: cfg.WeCom.Secret, ContactSecret: cfg.WeCom.ContactSecret,
		AdminCallbackURI: cfg.PublicOrigin + "/auth/wecom/callback", SidebarCallbackURI: cfg.PublicOrigin + "/api/sidebar/oauth/callback",
		APIBase: cfg.WeCom.APIBase, HTTPClient: cfg.WeCom.HTTPClient, UploadTimeout: cfg.WeCom.MaterialUploadTimeout,
	}
}

// composeWithWeComClientFactoryAndSurveyCompletionHTTPClient keeps a supplied
// HTTPS client inside test Composition only. Production Composition passes nil
// and therefore retains the outbound provider's locked default transport.
func composeWithWeComClientFactoryAndSurveyCompletionHTTPClient(ctx context.Context, cfg platformconfig.Runtime, providerFactory func(wecomadapter.Config) (*wecomadapter.Client, error), surveyCompletionHTTPClient *http.Client, surveyCompletionNetwork outbound.SurveyCompletionNetwork) (*composedApplication, error) {
	if providerFactory == nil {
		return nil, errors.New("WeCom client factory is required")
	}
	// Direct composition fixtures omit policy values that Load supplies. Apply
	// the same documented defaults before the closed Config baseline is built.
	cfg = platformconfig.NormalizeRuntimePolicyDefaults(cfg)
	var hxcSource *hxcprovider.MySQL
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: cfg.DatabaseURL, MaxConnections: 20, MinConnections: 1})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*composedApplication, error) {
		if hxcSource != nil {
			_ = hxcSource.Close()
		}
		pool.Close()
		return nil, err
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return fail(err)
	}
	// Config is applied before any business adapter is constructed. The active
	// immutable release is therefore the startup snapshot for every role; no
	// HTTP action mutates a process environment or invokes a Provider.
	configRepository, err := configstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	aiModelSettings, err := configapp.NewAIModelService(uow, configRepository, []byte(cfg.Survey.DataKey))
	if err != nil {
		return fail(err)
	}
	storedModel, modelConfigured, err := aiModelSettings.ReadAIModelRuntime(ctx)
	if err != nil {
		return fail(err)
	}
	if modelConfigured {
		cfg.AIGeneration.BaseURL = storedModel.BaseURL
		cfg.AIGeneration.APIKey = storedModel.APIKey
		cfg.AIGeneration.Model = storedModel.Model
	}
	runtimeDefaults, err := runtimeConfigDefaults(cfg)
	if err != nil {
		return fail(err)
	}
	runtimeDefaultLimit := cfg.AutomationOperations.MaxRecipientsPerRun
	if runtimeDefaultLimit < 1 {
		runtimeDefaultLimit = 1
	}
	runtimeReleaseService, err := configapp.NewRuntimeReleaseService(uow, configRepository, configRepository, runtimeDefaultLimit, configapp.WithRuntimeDefaults(runtimeDefaults), configapp.WithProtectedReferencePresence(runtimeConfigProtectedReferencePresence(cfg)), configapp.WithRuntimeActivationGuards(runtimeConfigActivationGuards(cfg)))
	if err != nil {
		return fail(err)
	}
	runtimeSnapshot, err := runtimeReleaseService.EffectiveSnapshot(ctx)
	if err != nil {
		return fail(err)
	}
	cfg, err = applyRuntimeConfig(cfg, runtimeSnapshot)
	if err != nil {
		return fail(err)
	}
	if err = validateAppliedRuntimeConfig(cfg); err != nil {
		return fail(err)
	}
	auditService, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		return fail(err)
	}
	inboxService, err := platformwebhook.NewService(platformwebhook.NewPostgreSQLStore())
	if err != nil {
		return fail(err)
	}

	passwords := credential.PasswordHasher{}
	dummyHash, err := passwords.Hash("aicrm-dummy-password-never-valid")
	if err != nil {
		return fail(err)
	}
	accessRepository := accessstore.NewPostgreSQL()
	authentication, err := accessapp.NewAuthentication(accessRepository, uow, passwords, accessapp.AuthenticationConfig{DummyPHCHash: dummyHash})
	if err != nil {
		return fail(err)
	}
	management, err := accessapp.NewManagement(accessRepository, uow, passwords, nil)
	if err != nil {
		return fail(err)
	}
	if len(cfg.WeCom.ContextSigningKey) >= 32 {
		if err = management.SetGovernanceSigningKey([]byte(cfg.WeCom.ContextSigningKey)); err != nil {
			return fail(err)
		}
	}
	machineService, err := accessapp.NewMachineService(accessRepository, uow, passwords, accessapp.MachineConfig{SigningKey: []byte(cfg.OpenPlatform.JWTSigningKey), CorpID: cfg.WeCom.CorpID})
	if err != nil {
		return fail(err)
	}
	machineRateLimiter, err := accessapp.NewMachineRequestRateLimiter(accessRepository, uow, accessapp.MachineRequestRateLimitConfig{})
	if err != nil {
		return fail(err)
	}
	staffProjector, err := accessapp.NewWeComStaffProjector(accessRepository, passwords, auditService)
	if err != nil {
		return fail(err)
	}

	if cfg.Survey.IdentityPhoneDataKey == "" {
		return fail(errors.New("identity phone data encryption key is not configured"))
	}
	phoneVault, err := identitysecure.NewPhoneVault(cfg.Survey.IdentityPhoneDataKey)
	if err != nil {
		return fail(err)
	}
	identityRepository := identitystore.NewPostgresStore(phoneVault)
	if cfg.HXCDashboard.IdentityObservationVaultKey != "" {
		observationVault, vaultErr := identitysecure.NewObservationVault(cfg.HXCDashboard.IdentityObservationVaultKey)
		if vaultErr != nil {
			return fail(vaultErr)
		}
		identityRepository = identitystore.NewPostgresStoreWithObservation(phoneVault, observationVault)
	}
	customerStore := customerstore.NewPostgreSQL()
	oneID := identityapp.OneIDService{Store: identityRepository, ProvisionedCustomer: customerProvisionedDirectoryAdapter{writer: customerStore}}
	queries := identityquery.NewPostgreSQL(phoneVault)
	hxcIdentity := identityapp.HXCSourceService{Inspector: queries, Store: identityRepository, OneID: oneID, VerifiedIdentity: identityadapter.HXCVerifiedUnionIDFactory{Enabled: cfg.HXCDashboard.UnionIDVerified}}
	paymentRepository := paymentstore.NewPostgreSQL()
	overviewReadUoW, err := platformpostgres.NewReadOnlyRepeatableReadUnitOfWork(pool)
	if err != nil {
		return fail(err)
	}
	distributionRepository, err := distributionstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	referralRepository, err := referralstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	customerOverview, err := customerapp.NewOverviewReader(uow, identityRepository)
	if err != nil {
		return fail(err)
	}
	paymentOverview, err := paymentapp.NewOverviewReader(overviewReadUoW, paymentRepository, queries)
	if err != nil {
		return fail(err)
	}
	distributionOverview, err := distributionapp.NewOverviewReader(uow, distributionRepository)
	if err != nil {
		return fail(err)
	}
	distributionPolicyService := distributionapp.NewPolicyService(distributionRepository)
	sidebarProfiles, err := customerapp.NewSidebarProfileApplication(uow, customerStore, oneID, customerStore, auditService, platformoutbox.NewPostgreSQL())
	if err != nil {
		return fail(err)
	}
	requestSecurity := requestAccessSecurity{authentication: authentication}
	adminOverviewHandler, err := newAdminOverviewHandler(customerOverview, paymentOverview, distributionOverview, requestSecurity)
	if err != nil {
		return fail(err)
	}
	effectsModule := externaleffects.NewModuleRegistration()
	effectWorkers := river.NewWorkers()
	opsInspectionWorker := adminops.NewInspectionWorker()
	opsReportWorker := adminops.NewReportWorker()
	opsRetentionWorker := adminops.NewRetentionWorker()
	if err = river.AddWorkerSafely[adminops.InspectionJobArgs](effectWorkers, opsInspectionWorker); err != nil {
		return fail(err)
	}
	if err = river.AddWorkerSafely[adminops.OpsReportJobArgs](effectWorkers, opsReportWorker); err != nil {
		return fail(err)
	}
	if err = river.AddWorkerSafely[adminops.RetentionJobArgs](effectWorkers, opsRetentionWorker); err != nil {
		return fail(err)
	}

	if err = effectsModule.RegisterWorkers(effectWorkers); err != nil {
		return fail(err)
	}
	customerSyncWorker := wecom.NewCustomerSyncWorker()
	if err = river.AddWorkerSafely[wecom.CustomerSyncJobArgs](effectWorkers, customerSyncWorker); err != nil {
		return fail(err)
	}
	contactDescriptionCallbackWorker := wecom.NewContactDescriptionCallbackWorker()
	if err = river.AddWorkerSafely[wecom.ContactDescriptionCallbackJobArgs](effectWorkers, contactDescriptionCallbackWorker); err != nil {
		return fail(err)
	}
	staffDirectoryWorker := wecom.NewStaffDirectoryRefreshWorker()
	if err = river.AddWorkerSafely[wecom.StaffDirectoryRefreshJobArgs](effectWorkers, staffDirectoryWorker); err != nil {
		return fail(err)
	}
	hxcDashboardWorker := &hxcworker.Worker{}
	if err = river.AddWorkerSafely[hxcworker.Args](effectWorkers, hxcDashboardWorker); err != nil {
		return fail(err)
	}
	paymentReconciliationWorker := payment.NewReconciliationWorker()
	if err = river.AddWorkerSafely[payment.ReconciliationJobArgs](effectWorkers, paymentReconciliationWorker); err != nil {
		return fail(err)
	}
	distributionDueWorker := distributionapp.NewCommissionDueWorker()
	if err = river.AddWorkerSafely[distributionapp.CommissionDueJobArgs](effectWorkers, distributionDueWorker); err != nil {
		return fail(err)
	}
	distributionRefundWorker := distributionapp.NewRefundRecheckWorker()
	if err = river.AddWorkerSafely[distributionapp.RefundRecheckJobArgs](effectWorkers, distributionRefundWorker); err != nil {
		return fail(err)
	}
	coreSubmissionWorker := &segment.CoreSubmissionWorker{}
	if err = river.AddWorkerSafely[segment.CoreSubmissionArgs](effectWorkers, coreSubmissionWorker); err != nil {
		return fail(err)
	}
	referralCampaignCloseWorker := referralapp.NewCampaignCloseWorker()
	if err = river.AddWorkerSafely[referralapp.CampaignCloseJobArgs](effectWorkers, referralCampaignCloseWorker); err != nil {
		return fail(err)
	}
	audienceRefreshWorker := segment.NewAudienceRefreshWorker()
	if err = river.AddWorkerSafely[segment.AudienceRefreshJobArgs](effectWorkers, audienceRefreshWorker); err != nil {
		return fail(err)
	}
	audienceMemberEventWorker := segment.NewAudienceMemberEventDispatchWorker()
	if err = river.AddWorkerSafely[segment.AudienceMemberEventDispatchJobArgs](effectWorkers, audienceMemberEventWorker); err != nil {
		return fail(err)
	}
	audienceScheduleWorker := segment.NewAudienceScheduleScanWorker()
	if err = river.AddWorkerSafely[segment.AudienceScheduleScanJobArgs](effectWorkers, audienceScheduleWorker); err != nil {
		return fail(err)
	}
	audienceDirectPushReconcileWorker := automation.NewDirectPushReconcileWorker()
	if err = river.AddWorkerSafely[automation.DirectPushReconcileArgs](effectWorkers, audienceDirectPushReconcileWorker); err != nil {
		return fail(err)
	}
	audienceDirectPushObserveWorker := automation.NewDirectPushObserveWorker()
	if err = river.AddWorkerSafely[automation.DirectPushObserveArgs](effectWorkers, audienceDirectPushObserveWorker); err != nil {
		return fail(err)
	}
	groupOpsContinuationWorker := groupopsapp.NewContinuationWorker()
	if err = river.AddWorkerSafely[groupopsapp.ContinuationJobArgs](effectWorkers, groupOpsContinuationWorker); err != nil {
		return fail(err)
	}
	ownerHandoffBatchWorker := customer.NewOwnerHandoffBatchWorker()
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](effectWorkers, ownerHandoffBatchWorker); err != nil {
		return fail(err)
	}
	excelClient, err := aiexcel.NewClient(cfg.AIAssistant.ExcelBatchURL, cfg.AIAssistant.ExcelBatchToken)
	if err != nil {
		return fail(err)
	}
	excelWorker := &aiexcel.Worker{}
	if err = river.AddWorkerSafely[aiexcel.RefreshArgs](effectWorkers, excelWorker); err != nil {
		return fail(err)
	}
	materialScopeDigest := string(effectport.Hash("outbound.material.scope.v1", cfg.WeCom.CorpID, cfg.WeCom.AgentID, "application_media_upload"))
	materialRefreshWorker := outbound.NewMaterialRefreshWorker(nil, materialScopeDigest)
	if err = river.AddWorkerSafely[outbound.MaterialRefreshJobArgs](effectWorkers, materialRefreshWorker); err != nil {
		return fail(err)
	}
	catalogWorker := &groupops.CatalogWorker{}
	if err = river.AddWorkerSafely[groupops.CatalogArgs](effectWorkers, catalogWorker); err != nil {
		return fail(err)
	}
	catalogDetailWorker := &groupops.CatalogDetailWorker{}
	if err = river.AddWorkerSafely[groupops.CatalogDetailArgs](effectWorkers, catalogDetailWorker); err != nil {
		return fail(err)
	}
	catalogScheduleWorker := &groupops.CatalogScheduleWorker{}
	if err = river.AddWorkerSafely[groupops.CatalogScheduleArgs](effectWorkers, catalogScheduleWorker); err != nil {
		return fail(err)
	}
	invitationWorker := &media.InvitationRefreshWorker{}
	if err = river.AddWorkerSafely[media.InvitationRefreshArgs](effectWorkers, invitationWorker); err != nil {
		return fail(err)
	}
	effectClient, err := platformjobqueue.NewInsertClient(pool.Native(), effectWorkers)
	if err != nil {
		return fail(err)
	}
	audienceDirectPushScheduler, err := automation.NewRiverDirectPushObservationScheduler(effectClient)
	if err != nil {
		return fail(err)
	}
	groupOpsContinuationEnqueuer, err := groupopsapp.NewRiverContinuationEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	catalogEnqueuer := groupops.CatalogEnqueuer{Client: effectClient, UoW: uow}
	ownerHandoffBatchEnqueuer, err := customer.NewRiverOwnerHandoffEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	paymentReconciliationEnqueuer, err := payment.NewRiverReconciliationEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	distributionDueEnqueuer, err := distributionapp.NewRiverCommissionDueEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	distributionRefundEnqueuer, err := distributionapp.NewRiverRefundRecheckEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	referralCampaignCloseEnqueuer, err := referralapp.NewRiverCampaignCloseEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	customerSyncEnqueuer, err := wecom.NewRiverCustomerSyncEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	contactDescriptionCallbackEnqueuer, err := wecom.NewRiverContactDescriptionCallbackEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	hxcEnqueuer, err := hxcworker.NewEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	effectRepository, err := externaleffects.NewRepository(pool.Native(), effectClient)
	if err != nil {
		return fail(err)
	}
	mediaModule := media.NewModuleRegistration()
	mediaRepository, err := mediastore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	adminOpsProjectionStore, err := adminopsstore.NewProjectionPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	opsRetention, err := adminops.NewRetentionService(pool.Native(), mediaRepository, adminOpsProjectionStore, cfg.Ops.RetentionEnabled)
	if err != nil {
		return fail(err)
	}
	if err = bindOpsOwnerRetention(opsRetention, configRepository, archivestore.NewPostgreSQL()); err != nil {
		return fail(err)
	}
	effectsQueues := []string{platformjobqueue.OpsInspectionQueue, platformjobqueue.OpsNotificationQueue, platformjobqueue.OpsRetentionQueue, platformjobqueue.OutboundQueue, platformjobqueue.OutboundWelcomeQueue, platformjobqueue.OutboundExcelQueue, platformjobqueue.OutboundMediaQueue, wecom.CustomerSyncQueue, wecom.StaffDirectoryRefreshQueue, payment.ReconciliationQueue, distributionapp.DistributionSettlementQueue, referralapp.ReferralCampaignQueue, hxcworker.Queue, segment.AudienceRefreshQueue, customer.OwnerHandoffQueue, groupops.CatalogQueue}
	opsHostMaintenance := hostmaintenance.New()
	if err = opsRetention.BindHostReader(opsHostMaintenance); err != nil {
		return fail(err)
	}
	if err = opsRetention.BindReleaseReader(opsHostMaintenance); err != nil {
		return fail(err)
	}
	if err = opsRetentionWorker.BindHostMaintenance(opsHostMaintenance); err != nil {
		return fail(err)
	}
	var opsProbe *platformruntime.EndpointProbe
	if cfg.Ops.Enabled {
		// Unsupported listener topology stays explicitly uncovered; a disabled
		// observer must never stop an otherwise valid CRM deployment.
		opsProbe, _ = platformruntime.NewEndpointProbe(cfg.ListenAddress, cfg.ReleaseSHA)
	}
	opsInspections, err := adminops.NewInspectionService(pool.Native(), uow, opsInspectionCollectors(pool.Native(), effectRepository, opsRetention, opsProbe, overviewReadUoW, opsHostMaintenance, effectsQueues), effectRepository, adminops.InspectionOptions{ReleaseSHA: cfg.ReleaseSHA, NotificationTargetRef: cfg.Ops.TargetRef, NotificationEnabled: cfg.Ops.NotificationEnabled, DetailURL: cfg.PublicOrigin + "/admin/ops", WindowReader: effectRepository})
	if err != nil {
		return fail(err)
	}
	if err = opsInspectionWorker.BindService(opsInspections); err != nil {
		return fail(err)
	}
	if err = opsReportWorker.BindService(opsInspections); err != nil {
		return fail(err)
	}
	if err = opsRetentionWorker.BindService(opsRetention); err != nil {
		return fail(err)
	}
	opsInspectionHTTP, err := adminops.NewInspectionHandler(opsInspections, opsSecurity{requestSecurity})
	if err != nil {
		return fail(err)
	}
	opsManualEnqueuer, err := adminops.NewInspectionManualEnqueuer(uow, effectClient, nil)
	if err != nil {
		return fail(err)
	}
	if err = opsInspectionHTTP.BindManualEnqueuer(opsManualEnqueuer); err != nil {
		return fail(err)
	}
	opsGovernanceOutcomes, err := adminops.NewGovernanceOutcomesService(pool.Native(), uow, hostmaintenance.NewGovernanceReleaseReader(), adminops.GovernanceOutcomesOptions{})
	if err != nil {
		return fail(err)
	}
	opsGovernanceOutcomesHTTP, err := adminops.NewGovernanceOutcomesHandler(opsGovernanceOutcomes, opsSecurity{requestSecurity})
	if err != nil {
		return fail(err)
	}
	opsRetentionHTTP, err := adminops.NewRetentionHandler(opsRetention, opsSecurity{requestSecurity})
	if err != nil {
		return fail(err)
	}
	// CPU artifacts share the process-retention policy. Sampling is available
	// only when both observation and its physical cleanup are explicitly on.
	opsCPUProfiles, err := adminops.NewCPUProfileService(pool.Native(), platformdiagnostics.NewCPUProfiler(), cfg.ReleaseSHA, cfg.Role == platformconfig.RoleAPI && cfg.Ops.Enabled && cfg.Ops.RetentionEnabled)
	if err != nil {
		return fail(err)
	}
	opsCPUProfileHTTP, err := adminops.NewCPUProfileHandler(opsCPUProfiles, opsSecurity{requestSecurity})
	if err != nil {
		return fail(err)
	}
	opsProvider, err := adminopsprovider.NewFeishu(adminopsprovider.FeishuConfig{Enabled: cfg.Ops.NotificationEnabled, TargetRef: cfg.Ops.TargetRef, WebhookURL: cfg.Ops.WebhookURL, SigningSecret: cfg.Ops.SigningSecret}, opsInspections)
	if err != nil {
		return fail(err)
	}
	materialSources := excelMaterialSources{media: mediaRepository, excel: excelClient}
	materialPreparation, err := outbound.NewMaterialPreparationService(uow, effectRepository, pool.Native(), materialSources)
	if err != nil {
		return fail(err)
	}
	if err = mediaRepository.BindMaterialPreparationAccepter(materialPreparation, materialScopeDigest); err != nil {
		return fail(err)
	}
	materialRefreshEnqueuer, err := outbound.NewRiverMaterialRefreshEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	if err = materialPreparation.BindRefreshEnqueuer(materialRefreshEnqueuer); err != nil {
		return fail(err)
	}
	if err = materialRefreshWorker.Bind(materialPreparation); err != nil {
		return fail(err)
	}
	if err = materialPreparation.EnsureDailyRefresh(ctx); err != nil {
		return fail(err)
	}
	periodicJobs := []*river.PeriodicJob{segment.AudienceSchedulePeriodicJob(), automation.DirectPushReconcilePeriodicJob()}
	if cfg.Ops.Enabled {
		periodicJobs = append(periodicJobs, adminops.InspectionPeriodicJobs()...)
	}
	if cfg.Ops.RetentionEnabled {
		periodicJobs = append(periodicJobs, adminops.RetentionPeriodicJob())
	}
	periodicJobs = append(periodicJobs, outbound.MaterialRefreshPeriodicJob(), groupops.CatalogPeriodicJob(), media.InvitationPeriodicJob())
	if excelClient != nil {
		periodicJobs = append(periodicJobs, aiexcel.Periodic())
	}
	if cfg.WeCom.ChannelProviderReadEnabled {
		periodicJobs = append(periodicJobs, wecom.StaffDirectoryPeriodicJob(cfg.WeCom.StaffDirectoryRefreshInterval, nil))
	}
	opsRecord := func(c context.Context, o platformdiagnostics.Observation) error {
		return opsInspections.RecordDiagnosticObservation(c, adminopsport.DiagnosticObservation{Component: "runtime", Code: o.Code, Correlation: o.Correlation, RouteTemplate: o.RouteTemplate, JobRef: o.JobRef, EffectRef: o.EffectRef, JobAttempt: o.JobAttempt})
	}
	effectsRuntime, err := platformjobqueue.NewRuntimeWithDiagnostics(pool.Native(), effectWorkers, periodicJobs, cfg.ReleaseSHA, opsRecord, effectsQueues...)
	if err != nil {
		return fail(err)
	}
	effectsBindings, err := effectsModule.Bind(effectRepository, requestSecurity)
	if err != nil {
		return fail(err)
	}
	aiModule := aiassistant.NewModuleRegistration()
	aiRepository, err := aiassistantstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	aiCustomers := aiCustomerSnapshotAdapter{read: func(ctx context.Context, id customerdomain.CustomerID) (customerdomain.CustomerID, customerdomain.Status, string, string, error) {
		detail, readErr := customerStore.Detail(ctx, id)
		if readErr != nil {
			return 0, "", "", "", readErr
		}
		numbers, numberErr := queries.CustomerPublicNumbers(ctx, []customerdomain.CustomerID{detail.CustomerID})
		return detail.CustomerID, detail.CustomerStatus, detail.DisplayName, numbers[detail.CustomerID], numberErr
	}}
	aiService, err := aiassistantapp.NewService(uow, aiRepository, aiCustomers, aiStaffSnapshotAdapter{repository: accessRepository}, aiMaterialAdapter{capturer: mediaRepository, references: mediaRepository, legacy: mediaRepository}, oneID, queries)
	if err != nil {
		return fail(err)
	}
	privateWriter, err := outbound.NewPrivateMessageRepository(pool.Native(), effectRepository)
	if err != nil {
		return fail(err)
	}
	if err = aiService.BindOutbound(privateWriter, cfg.AIAssistant.DispatchEnabled); err != nil {
		return fail(err)
	}
	if err = aiService.BindReconciler(effectRepository); err != nil {
		return fail(err)
	}
	aiHandler, err := aiassistanthttp.NewHandler(aiassistanthttp.Config{Application: aiService, Security: requestSecurity, Authorizer: accessapp.AIAssistantAuthorizer{}, Integration: aiassistanthttp.IntegrationConfig{Enabled: cfg.AIAssistant.IntakeEnabled, Key: cfg.AIAssistant.IntegrationKey, Secret: cfg.AIAssistant.IntegrationSecret, ActorID: cfg.AIAssistant.IntegrationActorID, WeComCorpID: cfg.WeCom.CorpID, OpenPlatformID: cfg.Survey.OAuthOpenPlatformID}, DispatchReady: cfg.AIAssistant.DispatchEnabled})
	if err != nil {
		return fail(err)
	}
	contentDelivery := mediaapp.NewContentDeliveryService(uow, mediaRepository)
	mediaService, err := mediaapp.NewHTTPFacade(mediaRepository)
	if err != nil {
		return fail(err)
	}
	mediaLibrary := sidebarMediaLibrary{library: mediaService, sender: mediaapp.NewReadService(uow, mediaRepository)}
	radarModule := radarmodule.NewModuleRegistration()
	radarRepository := radarstore.NewPostgres()
	radarManager, err := radarapp.NewService(uow, radarRepository, radarRepository)
	if err != nil {
		return fail(err)
	}
	if err = radarManager.BindMediaValidator(radarMediaReferenceAdapter{media: mediaRepository}); err != nil {
		return fail(err)
	}
	radarQuery, err := radarapp.NewQueryService(uow, radarRepository)
	if err != nil {
		return fail(err)
	}
	if err = radarQuery.BindAdminVisitorPresentation(radarVisitorPresentationAdapter{numbers: queries, uow: uow, directory: customerStore, identities: queries, corpScope: "wecom-corp:" + cfg.WeCom.CorpID}); err != nil {
		return fail(err)
	}
	radarOAuth, err := radarprovider.NewWeChatOAuth(cfg.Survey.OAuthEnabled, cfg.Survey.OAuthAppID, cfg.Survey.OAuthSecret, cfg.Survey.OAuthOpenPlatformID, cfg.PublicOrigin+"/api/public/radar/oauth/callback")
	if err != nil {
		return fail(err)
	}
	radarPublic, err := radarapp.NewPublicAccessService(uow, radarRepository, radarRepository, radarQuery, radarOAuth, oneID, radarContentAdapter{media: mediaService})
	if err != nil {
		return fail(err)
	}
	radarBindings, err := radarModule.Bind(radarManager, radarQuery, radarPublic, requestSecurity, cfg.PublicOrigin)
	if err != nil {
		return fail(err)
	}
	mediaBindings, err := mediaModule.Bind(mediaService, requestSecurity)
	if err != nil {
		return fail(err)
	}
	if err = mediaBindings.MaterialPreparationAdmin.BindMaterialPreparation(mediaRepository, materialPreparation, materialPreparation, materialPreparation, materialScopeDigest); err != nil {
		return fail(err)
	}
	mediaContentBindings, err := mediaModule.BindContentDelivery(contentDelivery, mediaRepository)
	if err != nil {
		return fail(err)
	}
	automationModule := automation.NewModuleRegistration()
	automationRepository, err := automationstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	outboundMessages, err := outbound.NewMessageService(pool.Native(), uow, effectRepository, automationRepository)
	if err != nil {
		return fail(err)
	}
	automationService := automationapp.NewAgentServiceWithMediaReferences(uow, automationRepository, mediaRepository, mediaRepository, mediaRepository, mediaRepository, automationRepository)
	automationBindings, err := automationModule.Bind(automationService, requestSecurity)
	if err != nil {
		return fail(err)
	}
	segmentModule := segment.NewModuleRegistration()
	segmentRepository, err := segmentstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	segmentService := segmentapp.NewService(uow, segmentRepository)
	// Populate this composition-owned adapter as its Owner stores are built
	// below. The process has not started serving requests at this point.
	legacyAudienceSource := &segmentadapter.LegacyTemplateSource{Groups: wecom.PostgreSQLGroupMembershipFacts{}, GroupCandidates: wecom.GroupCandidateFacts{Identity: queries}, Radar: radarRepository, PrimaryOwnerCorpScope: "wecom-corp:" + cfg.WeCom.CorpID}
	segmentEvaluator, err := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, segmentapp.CoreSource{UOW: uow, Reader: segmentRepository, Fallback: segmentadapter.CustomerSource{UoW: uow, Customers: customerStore, Legacy: legacyAudienceSource}}, segmentadapter.CanonicalCustomers{UoW: uow, Resolver: canonicalCustomerAdapter{reader: queries}})
	if err != nil {
		return fail(err)
	}
	segmentEnqueuer, err := segment.NewRiverRefreshEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	segmentMemberEventEnqueuer, err := segment.NewRiverMemberEventEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	segmentSnapshots, err := segmentapp.NewSnapshotService(uow, segmentRepository, segmentEvaluator, segmentEnqueuer, segmentMemberEventEnqueuer)
	if err != nil {
		return fail(err)
	}
	if err = audienceRefreshWorker.BindService(segmentSnapshots); err != nil {
		return fail(err)
	}
	scheduledRefreshes, err := segmentapp.NewScheduledRefreshService(uow, segmentRepository, segmentSnapshots)
	if err != nil {
		return fail(err)
	}
	preparedAudienceSchedule := &audienceGroupPreparedSchedule{uow: uow, targets: segmentRepository, facts: wecom.GroupProviderFacts{}, next: scheduledRefreshes, corp: "wecom-corp:" + cfg.WeCom.CorpID}
	if err = audienceScheduleWorker.BindService(preparedAudienceSchedule); err != nil {
		return fail(err)
	}
	segmentStaff := automationOpsStaffAdapter{uow: uow, users: accessRepository}
	legacyAudienceSource.Owners = segmentStaff
	automationProviderReady := cfg.Effects.ProviderEnabled && cfg.WeCom.Enabled && cfg.AutomationOperations.ProviderEnabled()
	segmentExecution, err := segmentapp.NewExecutionService(uow, segmentRepository, automationService, segmentStaff, automationProviderReady)
	if err != nil {
		return fail(err)
	}
	automationRecipientLimit := cfg.AutomationOperations.MaxRecipientsPerRun
	if automationRecipientLimit == 0 {
		automationRecipientLimit = 1
	}
	automationRuntime, err := automationapp.NewRuntimeService(uow, automationRepository, segmentExecution, segmentSnapshots, automationRecipientLimit)
	if err != nil {
		return fail(err)
	}
	if err = automationRuntime.SetMessageAccepter(outboundMessages); err != nil {
		return fail(err)
	}
	if err = automationRuntime.SetReviewPlanIntake(aiService, automationService); err != nil {
		return fail(err)
	}
	if err = automationRuntime.SetOutboundContentFreezer(automationOutboundContentFreezer{capturer: mediaRepository, materials: mediaRepository}); err != nil {
		return fail(err)
	}
	if err = automationRuntime.SetEffectReconciler(effectRepository); err != nil {
		return fail(err)
	}
	audienceUnionScope := strings.TrimSpace(cfg.HXCDashboard.UnionIDScope)
	if audienceUnionScope == "" && strings.TrimSpace(cfg.Survey.OAuthOpenPlatformID) != "" {
		audienceUnionScope = "wechat-open-platform:" + strings.TrimSpace(cfg.Survey.OAuthOpenPlatformID)
	}
	audienceDirectPush, err := automationapp.NewDirectPushRuntime(
		uow,
		automationRepository,
		audienceDirectPushTargetResolver{uow: uow, identities: oneID, staff: accessRepository, unionScope: audienceUnionScope},
		directPushEligibilityAdapter{reader: segmentRepository},
		automationOutboundContentFreezer{capturer: mediaRepository, materials: mediaRepository},
		outboundMessages,
	)
	if err != nil {
		return fail(err)
	}
	if err = audienceDirectPushReconcileWorker.Bind(audienceDirectPush); err != nil {
		return fail(err)
	}
	if err = audienceDirectPushObserveWorker.Bind(audienceDirectPush); err != nil {
		return fail(err)
	}
	audienceDirectPushHandler, err := automationhttp.NewDirectPushHandler(audienceDirectPush, &audienceDirectPushAuthenticator{key: []byte(cfg.AutomationOperations.AudiencePushWebhookSecret), now: time.Now})
	if err != nil {
		return fail(err)
	}
	audienceDirectPushAdmin, err := automationhttp.NewDirectPushAdminHandler(audienceDirectPush, requestSecurity)
	if err != nil {
		return fail(err)
	}
	if err = audienceMemberEventWorker.Bind(segmentSnapshots, automationMemberEventSink{runtime: automationRuntime}); err != nil {
		return fail(err)
	}
	automationBindings.Runtime, err = automationModule.BindRuntime(automationRuntime, requestSecurity)
	if err != nil {
		return fail(err)
	}
	segmentRuntime := segmentapp.NewRuntimeFacade(segmentService, segmentSnapshots, segmentExecution)
	segmentBindings, err := segmentModule.BindRuntimeWithOwnerReferences(segmentRuntime, segmentRuntime, requestSecurity, segmentStaff, segmentStaff)
	if err != nil {
		return fail(err)
	}
	coreOperations := segmentapp.NewCoreOperations(segmentService, segmentRepository, segmentadapter.CanonicalCustomers{UoW: uow, Resolver: canonicalCustomerAdapter{reader: queries}}, segmentSnapshots)
	coreSubmissionWorker.Service = coreOperations
	coreOperations.BindReevaluation(segment.CoreSubmissionEnqueuer{Client: effectClient})
	segmentBindings.Handler.BindCoreOperations(coreOperations)
	segmentBindings.Handler.BindAudienceRadarReferences(audienceRadarReferenceAdapter{radars: radarManager})
	segmentWebhookService, err := segmentapp.NewWebhookService(uow, segmentRepository, oneID, segmentSnapshots)
	if err != nil {
		return fail(err)
	}
	segmentWebhookHandler, err := segmenthttp.NewWebhookHandler(segmentWebhookService, cfg.AutomationOperations.WebhookSecret)
	if err != nil {
		return fail(err)
	}
	mediaPreparationWriter := mediaapp.NewGroupOpsMaterialPreparationWriter(uow, mediaRepository)
	mediaPreparationBindings, err := mediaModule.BindMaterialPreparation(mediaRepository, mediaPreparationWriter)
	if err != nil {
		return fail(err)
	}
	var groupOpsProvider *outbound.GroupMessageProvider
	materialFreezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{sources: mediaRepository, preparer: materialPreparation, scopeDigest: materialScopeDigest})
	if err != nil {
		return fail(err)
	}
	webhookLessonCards := mediaapp.NewWebhookLessonCardResolver(mediaRepository, nil)
	groupOpsMaterials, err := newUnifiedGroupOpsMaterialAdapter(mediaContentBindings.SourceCapturer, materialFreezer, mediaRepository, materialPreparation, materialScopeDigest, webhookLessonCards)
	if err != nil {
		return fail(err)
	}
	tagModule := tag.NewModuleRegistration()
	var callbackStateDigester wecom.StateDigester
	if cfg.WeCom.CallbackEnabled {
		callbackStateDigester, err = wecom.NewHMACStateDigester([]byte(cfg.WeCom.ChannelStateHMACKey))
		if err != nil {
			return fail(err)
		}
	}
	channelAssetStore := channelstore.NewPostgreSQLAssetStore(pool.Native())
	channelAcquisition := channelstore.NewPostgreSQLStore()
	legacyAudienceSource.Channels = channelAcquisition
	channelEntrantActions := channelstore.NewEntrantActionStore(effectRepository, channelWelcomeMaterialAdapter{resolver: groupOpsMaterials})
	channelLinkStore := channelstore.NewAcquisitionLinkStore()
	channelAssetCompletionSink, err := outbound.NewChannelAssetCompletionSink(channelAssetCompletionAdapter{assets: channelAssetStore, bindings: channelAcquisition, digester: callbackStateDigester, corpID: cfg.WeCom.CorpID})
	if err != nil {
		return fail(err)
	}
	channelEntrantCompletionSink, err := outbound.NewChannelEntrantCompletionSink(channelEntrantActions)
	if err != nil {
		return fail(err)
	}
	channelLinkCompletionSink, err := outbound.NewChannelLinkCompletionSink(channelLinkStore)
	if err != nil {
		return fail(err)
	}
	tagRepository, err := tagstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	tagCompletionSink, err := outbound.NewTagCatalogCompletionSink(tagRepository)
	if err != nil {
		return fail(err)
	}
	tagMutationCompletionSink, err := outbound.NewTagCatalogMutationCompletionSink(tagRepository)
	if err != nil {
		return fail(err)
	}
	groupOpsRepository, err := groupopsstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	catalogService := &groupopsapp.CatalogService{Store: groupOpsRepository, Enabled: cfg.GroupOps.ProviderReadEnabled, Enqueue: catalogEnqueuer.Enqueue, RetryDetail: catalogEnqueuer.EnqueueDetail}
	catalogWorker.Service = catalogService
	catalogDetailWorker.Service = catalogService
	catalogScheduleWorker.Service = catalogService
	invitationService := &mediaapp.InvitationService{Store: mediaRepository, Catalog: catalogService, Effects: effectRepository, Origin: h5PublicOrigin(cfg), WriteEnabled: cfg.Effects.ProviderEnabled && cfg.WeCom.ChannelQRProviderEnabled}
	invitationWorker.Service = invitationService
	invitationHandler := mediahttp.InvitationHandler{Service: invitationService, Security: requestSecurity}
	groupOpsStaff := groupOpsStaffAdapter{access: accessRepository, owners: groupOpsRepository}
	audienceDirectory := audienceOperationMemberDirectory{uow: uow, directory: groupOpsStaff}
	// Directory reads have their own explicitly published capability gate. A
	// dispatch grant cannot silently authorize a new provider-read path.
	groupOpsDirectory := &wecomGroupOpsDirectory{uow: uow, enabled: cfg.GroupOps.ProviderReadEnabled, staff: groupOpsStaff}
	groupOpsEvidence := groupopsport.ReconciliationEvidenceVerifier(providerDisabledGroupOpsEvidence{})
	groupOpsService := groupopsapp.NewService(uow, groupOpsRepository, groupOpsStaff, groupOpsRepository)
	groupOpsHistory := groupopsapp.NewHistoryService(uow, groupOpsRepository)
	groupOpsRuntime := groupopsapp.NewRuntimeService(uow, groupOpsRepository, groupOpsRepository, effectRepository, groupOpsStaff, groupOpsDirectory, groupOpsStaff, groupOpsEvidence, groupOpsExternalReconciler{repository: effectRepository}, groupOpsMaterials)
	groupOpsRuntime.SetDispatchEnabled(cfg.GroupOps.ProviderEnabled)
	groupOpsRuntime.SetCatalog(catalogService)
	if err = groupOpsContinuationWorker.Bind(groupOpsRuntime); err != nil {
		return fail(err)
	}
	groupOpsProtocols := &groupOpsProtocolAuthenticator{key: []byte(cfg.GroupOps.WebhookSecret), replay: groupOpsRepository, now: time.Now}
	groupOpsModule := groupops.NewModuleRegistration()
	groupOpsBindings, err := groupOpsModule.BindWithHistory(groupOpsService, groupOpsRuntime, groupOpsHistory, requestSecurity, groupOpsProtocols, mediaContentBindings.ContentDelivery)
	if err != nil {
		return fail(err)
	}
	groupOpsCompletionSink, err := outbound.NewGroupMessageCompletionSink(groupOpsRepository, groupOpsRepository)
	if err != nil {
		return fail(err)
	}
	groupOpsCompletionSink.WithContinuation(groupOpsContinuationEnqueuer)
	customerTagCompletionSink, err := outbound.NewCustomerTagCompletionSink(customerstore.TagCommandPostgreSQL{}, customerTagCommandReaderAdapter{uow: uow, source: customerstore.TagCommandPostgreSQL{}}, channelEntrantActions)
	if err != nil {
		return fail(err)
	}
	privateCompletionSink, err := outbound.NewPrivateMessageCompletionSink(privateWriter, aiRepository)
	if err != nil {
		return fail(err)
	}
	outboundCompletionSink, err := outbound.NewCompletionRouterWithAllChannels(tagCompletionSink, groupOpsCompletionSink, channelAssetCompletionSink, channelEntrantCompletionSink, channelLinkCompletionSink)
	if err != nil {
		return fail(err)
	}
	outboundCompletionSink.WithCustomerTag(customerTagCompletionSink)
	outboundCompletionSink.WithTagCatalogMutation(tagMutationCompletionSink)
	outboundCompletionSink.WithPrivateMessage(privateCompletionSink)
	outboundCompletionSink.WithAutomationMessage(outboundMessages)
	outboundCompletionSink.WithInvitationCode(outbound.InvitationCodeCompletionSink{Store: mediaRepository})
	sidebarExpiry := outbound.SidebarJSSDKExpiry{}
	outboundCompletionSink.WithSidebarJSSDK(sidebarExpiry)
	sidebarMediaPreparation, err := outbound.NewSidebarMediaPreparationService(uow, effectRepository, pool.Native())
	if err != nil {
		return fail(err)
	}
	materialCompletionMux := outbound.MaterialEffectMux{GenericCompletion: materialPreparation, LegacyCompletion: sidebarMediaPreparation}
	outboundCompletionSink.WithSidebarMedia(materialCompletionMux)
	coreRecommendationCompletion, err := automationprovider.NewAudienceRecommendationCompletionSink(coreOperations)
	if err != nil {
		return fail(err)
	}
	generationCompletionSink, err := automationprovider.NewGenerationCompletionSink(automationRuntime)
	if err != nil {
		return fail(err)
	}
	if cfg.Survey.DataKey == "" {
		return fail(errors.New("survey data encryption key is not configured"))
	}
	surveyCipher, err := secure.NewCipher(cfg.Survey.DataKey)
	if err != nil {
		return fail(err)
	}
	surveyRepository, err := surveystore.NewPostgreSQL(pool.Native(), uow, surveyCipher)
	if err != nil {
		return fail(err)
	}
	legacyAudienceSource.Survey = surveyRepository
	legacyAudienceSource.Submissions = surveyRepository
	surveyDefinitions := surveyapp.NewService(uow, surveyRepository)
	segmentBindings.Handler.BindAudienceSurveyReferences(audienceSurveyReferenceAdapter{surveys: surveyDefinitions})
	surveySubmissions := surveyapp.NewSubmissionService(uow, surveyRepository, surveyCipher)
	surveySubmissions.BindSubmissionObserver(segment.CoreSubmissionEnqueuer{Client: effectClient})
	surveyCompletionTargets, err := surveyCompletionTargets(cfg.Survey.CompletionTargetsJSON)
	if err != nil {
		return fail(err)
	}
	surveyCompletionRuntime, err := outbound.NewStaticSurveyCompletionTargets(surveyCompletionTargets)
	if err != nil {
		return fail(err)
	}
	surveyCompletionRefs := make([]string, 0, len(surveyCompletionTargets))
	for _, target := range surveyCompletionTargets {
		surveyCompletionRefs = append(surveyCompletionRefs, target.Reference)
	}
	surveyCompletionEndpoints := outbound.NewSurveyCompletionEndpoints(pool.Native(), surveyCompletionRuntime, surveyCompletionRefs)
	surveyCompletionProvider, err := outbound.NewSurveyCompletionProvider(outbound.SurveyCompletionProviderConfig{Enabled: cfg.Survey.CompletionProviderEnabled, Resolver: surveyCompletionEndpoints, Reader: surveyRepository, Client: surveyCompletionHTTPClient, Network: surveyCompletionNetwork, Identities: queries})
	if err != nil {
		return fail(err)
	}
	surveyCompletionSink, err := outbound.NewSurveyCompletionSink(surveyRepository)
	if err != nil {
		return fail(err)
	}
	if err = surveySubmissions.BindCompletionIntent(surveyCompletionEffectAccepter{effects: effectRepository}); err != nil {
		return fail(err)
	}
	if err = surveySubmissions.BindCompletionPolicy(surveyCompletionProvider); err != nil {
		return fail(err)
	}
	if err = surveySubmissions.BindCompletionIdentity(surveyCompletionProvider); err != nil {
		return fail(err)
	}
	if err = surveySubmissions.BindCompletionEndpoints(surveyCompletionEndpoints); err != nil {
		return fail(err)
	}
	if err = surveySubmissions.BindCustomerTimeline(customerStore); err != nil {
		return fail(err)
	}
	if err = surveySubmissions.BindDeclaredPhone(oneID, customerStore); err != nil {
		return fail(err)
	}
	outboundCompletionSink.WithSurveyCompletion(surveyCompletionSink)
	surveyOAuthProvider, err := surveyprovider.NewWeChatOAuth(cfg.Survey.OAuthEnabled, cfg.Survey.OAuthAppID, cfg.Survey.OAuthSecret, cfg.Survey.OAuthOpenPlatformID, h5PublicOrigin(cfg)+"/api/h5/surveys/oauth/callback", cfg.Survey.OAuthScope)
	if err != nil {
		return fail(err)
	}
	surveyOAuth := surveyapp.NewOAuthService(uow, surveyRepository, surveyOAuthProvider, oneID)
	surveyModule := surveymodule.NewModuleRegistration().SetCompletionProviderEnabled(cfg.Survey.CompletionProviderEnabled).SetCompletionTargetCatalog(surveyCompletionProvider)
	tagCatalog := tagapp.NewCatalogService(uow, tagRepository, tagRepository, tagRepository, tagRepository)
	if err = tagCatalog.RequireProviderMutations(); err != nil {
		return fail(err)
	}
	tagOutbound, err := outbound.NewTagCatalogSyncAccepter(effectRepository)
	if err != nil {
		return fail(err)
	}
	tagMutationOutbound, err := outbound.NewTagCatalogMutationAccepter(effectRepository)
	if err != nil {
		return fail(err)
	}
	if cfg.TagCatalog.MutationEnabled {
		if err = tagCatalog.BindProviderMutations(tagMutationOutbound); err != nil {
			return fail(err)
		}
	}
	if err := tagCatalog.BindMutationRecovery(effectRepository); err != nil {
		return fail(err)
	}
	tagSync := tagapp.NewSyncService(uow, tagRepository, tagRepository, tagOutbound)
	tagGate := tagapp.NewExecutionStatusService(uow, tagRepository)
	tagBindings, err := tagModule.Bind(tagCatalog, tagSync, tagGate, requestSecurity)
	if err != nil {
		return fail(err)
	}
	productModule := productmodule.NewModuleRegistration()
	productRepository, err := productstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	productEvents, err := productstore.NewTransactionalEventAppender(auditService, platformoutbox.NewPostgreSQL())
	if err != nil {
		return fail(err)
	}
	productCatalog := productapp.NewService(uow, productRepository, productEvents)
	productLifecycle := productapp.NewLocalProductLifecycleService(uow, productRepository, productEvents)
	productServicePeriod := productapp.NewServicePeriodService(uow, productRepository, productEvents)
	if err = productCatalog.SetDistributionPolicyWriter(distributionPolicyService); err != nil {
		return fail(err)
	}
	if err = productServicePeriod.SetDistributionPolicyWriter(distributionPolicyService); err != nil {
		return fail(err)
	}
	if err = productLifecycle.SetDistributionPolicyWriter(distributionPolicyService); err != nil {
		return fail(err)
	}
	commercePushRuntimeTargets, err := commercePushTargetsFromRuntime(cfg.CommercePush)
	if err != nil {
		return fail(err)
	}
	commercePushTemplateRefs := make([]string, 0, len(commercePushRuntimeTargets.values))
	for reference := range commercePushRuntimeTargets.values {
		commercePushTemplateRefs = append(commercePushTemplateRefs, reference)
	}
	commercePushTargetResolver := outbound.NewCommercePushEndpoints(pool.Native(), commercePushRuntimeTargets, commercePushTemplateRefs)
	var commercePushCipher outbound.CommercePayloadCipher
	if cfg.CommercePush.PayloadDataKey != "" {
		commercePushCipher, err = outbound.NewCommercePayloadAESGCM(cfg.CommercePush.PayloadDataKey)
		if err != nil {
			return fail(err)
		}
	}
	commercePushService, err := outbound.NewCommercePushService(pool.Native(), uow, effectRepository, commerceProductConfigurationReader{reader: productRepository}, queries, commercePushTargetResolver, commercePushCipher)
	if err != nil {
		return fail(err)
	}
	commercePushCompletionSink, err := outbound.NewCommercePushCompletionSink(commercePushService)
	if err != nil {
		return fail(err)
	}
	outboundCompletionSink.WithCommercePush(commercePushCompletionSink)
	// 0079 is Product-owned workspace metadata.  The HTTP host still reads
	// members through the Order port and display names through the Customer
	// port; it does not receive either store here.
	productMemberGridStaff := productMemberGridStaffDirectory{users: accessRepository}
	productMemberGrid := productapp.NewMemberGridWorkspaceService(uow, productRepository, productMemberGridStaff, productEvents)
	productExternalPush, err := productapp.NewCommerceExternalPushService(uow, productRepository, commercePushService, commercePushService, productEvents)
	if err != nil {
		return fail(err)
	}
	productExternalPush.SetCommercePushEndpointManager(commercePushTargetResolver)
	productBindings, err := productModule.Bind(productCatalog, productLifecycle, productServicePeriod, productExternalPush, requestSecurity)
	if err != nil {
		return fail(err)
	}
	if err = productBindings.ProductHandler.SetDistributionPolicyReader(distributionPolicyService); err != nil {
		return fail(err)
	}
	if err = productBindings.ProductHandler.SetServicePeriodMemberWorkspace(productMemberGrid); err != nil {
		return fail(err)
	}
	if err = productBindings.ProductHandler.SetServicePeriodMemberStaffDirectory(productMemberGridStaff); err != nil {
		return fail(err)
	}
	publicProductHandler, err := producthttp.NewPublicHandler(productCatalog)
	if err != nil {
		return fail(err)
	}
	publicProductHandler.SetPaymentMethods(cfg.WeChatPay.Enabled, cfg.Alipay.Enabled)
	// Public commerce presentation is a release-only browser closure. Resolve
	// its manifest lazily on the public route, consistent with the other UI
	// bindings below: workers and non-UI composition fixtures need no cwd
	// artifact, while an actual public request still fails closed if web/dist is
	// missing, altered, or incomplete.
	publicCommerceAssets, err := producthttp.NewDeferredPublicPresentationAssets("web/dist")
	if err != nil {
		return fail(err)
	}
	if err = publicProductHandler.SetPublicPresentationAssets(publicCommerceAssets); err != nil {
		return fail(err)
	}
	if err = publicProductHandler.SetPublicMediaReader(mediaService); err != nil {
		return fail(err)
	}
	publicServicePeriodHandler, err := producthttp.NewServicePeriodPublicHandler(productServicePeriod)
	if err != nil {
		return fail(err)
	}
	publicServicePeriodHandler.SetPaymentMethods(cfg.WeChatPay.Enabled, cfg.Alipay.Enabled)
	if err = publicServicePeriodHandler.SetPublicPresentationAssets(publicCommerceAssets); err != nil {
		return fail(err)
	}
	productTargets, err := productapp.NewTargetReader(productCatalog, productServicePeriod)
	if err != nil {
		return fail(err)
	}
	couponTargetNames, err := productapp.NewTargetBatchReader(uow, productRepository)
	if err != nil {
		return fail(err)
	}
	couponProducts, err := newCouponProductReads(productCatalog, couponTargetNames)
	if err != nil {
		return fail(err)
	}
	couponModule := coupon.NewModuleRegistration()
	couponRepository, err := couponstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	couponService := couponapp.NewService(uow, couponRepository, productTargets, couponRepository)
	sidebarCouponCatalog, err := couponapp.NewSidebarClaimableCatalog(uow, couponRepository, productTargets)
	if err != nil {
		return fail(err)
	}
	couponCheckout, err := couponapp.NewCheckoutService(uow, couponRepository)
	if err != nil {
		return fail(err)
	}
	couponPublic, err := couponapp.NewPublicCouponService(uow, couponRepository)
	if err != nil {
		return fail(err)
	}
	couponClaimAdmin, err := composeCouponClaimAdmin(uow, couponRepository)
	if err != nil {
		return fail(err)
	}
	couponBindings, err := couponModule.BindWithClaimsAndPublic(couponService, couponProducts, couponClaimAdmin, couponPublic, requestSecurity)
	if err != nil {
		return fail(err)
	}
	// PR09 config has no OneID, Provider-write, or worker dependency. Its
	// local settings, audit rows, and idempotency receipts share this UOW.
	configModule := configmodule.NewRegistration()
	configManager := configapp.NewManager(uow, configRepository, configRepository)
	if err = automationRuntime.SetRuntimeConfig(runtimeReleaseService, runtimeReleaseService); err != nil {
		return fail(err)
	}
	settingsService := configapp.NewSettingsCompatibilityService(uow, configRepository, configManager, configapp.SecretConfiguredSnapshot{
		DatabaseURL: cfg.DatabaseURL != "", WeComSecret: cfg.WeCom.Secret != "",
		WeComCallbackToken: cfg.WeCom.CallbackToken != "", WeComCallbackAESKey: cfg.WeCom.CallbackAESKey != "",
	})
	setupWizard, err := configapp.NewSetupWizardService(configManager, configapp.SetupWizardSecretConfigured{
		WeComSecret: cfg.WeCom.Secret != "", WeComCallbackToken: cfg.WeCom.CallbackToken != "", WeComCallbackAESKey: cfg.WeCom.CallbackAESKey != "",
	})
	if err != nil {
		return fail(err)
	}
	adminOpsProjection, err := adminopsapp.NewProjectionService(uow, adminOpsProjectionStore)
	if err != nil {
		return fail(err)
	}
	diagnostics, err := adminopsapp.NewDiagnosticsService(adminOpsProjection)
	if err != nil {
		return fail(err)
	}
	releaseObservation, err := releaseapp.NewObservationService(adminOpsReleaseObservationWriter{projections: adminOpsProjection})
	if err != nil {
		return fail(err)
	}
	configBindings, err := configModule.Bind(settingsService, setupWizard, configManager, adminOpsProjection, requestSecurity, runtimeReleaseService)
	configBindings = configBindings.WithAIModels(aiModelSettings)
	if err != nil {
		return fail(err)
	}
	channelCursorKey := make([]byte, 32)
	if _, err = rand.Read(channelCursorKey); err != nil {
		return fail(err)
	}
	channelCatalogStore := channelstore.NewPostgreSQLCatalogStore()
	channelModule := channelstore.NewModuleRegistration()
	channelEvents, err := channelstore.NewChannelCatalogEventAppender(auditService, platformoutbox.NewPostgreSQL())
	if err != nil {
		return fail(err)
	}
	channelCatalogService := channelstore.NewCatalogService(uow, channelCatalogStore, channelCatalogStore, channelEvents,
		channelMaterialReferenceAdapter{media: mediaRepository}, channelTagReferenceAdapter{tags: tagRepository}, channelStaffReferenceAdapter{users: accessRepository, profiles: groupOpsRepository})
	segmentBindings.Handler.BindAudienceChannelReferences(audienceChannelReferenceAdapter{channels: channelCatalogService})
	publicCompletionTargets, err := platformconfig.ParseSurveyCompletionNavigationTargets(cfg.Survey.CompletionNavigationTargetsJSON)
	if err != nil {
		return fail(err)
	}
	publicCompletionResolver, err := newSurveyCompletionNavigationResolver(publicCompletionTargets)
	if err != nil {
		return fail(err)
	}
	if err = surveySubmissions.BindPublicCompletionTarget(publicCompletionResolver); err != nil {
		return fail(err)
	}
	publicLeadQRCodes := channelstore.NewPostgreSQLPublicLeadQRCodeReader(uow)
	if err = surveySubmissions.BindPublicLeadQRCode(publicLeadQRCodes); err != nil {
		return fail(err)
	}
	// Bind Survey HTTP only after its two public completion read boundaries are
	// available. Survey still receives neither Channel tables nor an outbound
	// Provider endpoint.
	surveyBindings, err := surveyModule.Bind(surveyDefinitions, surveySubmissions, requestSecurity, surveyOAuth)
	if err != nil {
		return fail(err)
	}
	channelCatalog, err := channelstore.NewCatalogHTTPHandler(channelstore.CatalogHTTPConfig{Application: channelCatalogService, Summaries: channelstore.NewPostgreSQLCatalogSummaryReader(uow), Security: requestSecurity, CursorSigningKey: channelCursorKey})
	if err != nil {
		return fail(err)
	}
	channelAssetService := channelstore.NewAssetService(uow, channelCatalogStore, channelAssetStore, effectRepository, channelEvents)
	if err = channelAssetService.SetReconciler(channelAssetReconciler{uow: uow, assets: channelAssetStore, effects: effectRepository, bindings: channelAcquisition, digester: callbackStateDigester, corpID: cfg.WeCom.CorpID}); err != nil {
		return fail(err)
	}
	channelAssetHandler, err := channelstore.NewAssetHTTPHandler(channelAssetService, requestSecurity)
	if err != nil {
		return fail(err)
	}
	operationModule := operationcycle.NewModuleRegistration()
	operationRepository := operationstore.NewRepository()
	operationJournal := operationstore.NewEventJournal()
	operationService := operationapp.NewService(uow, operationRepository, operationJournal, operationJournal)
	if err = aiService.BindExcelBatchStrategyReader(operationCycleExcelStrategyAdapter{read: operationRepository}); err != nil {
		return fail(err)
	}
	operationBindings, err := operationModule.Bind(operationService, requestSecurity, cfg.OperationCycleServiceToken)
	if err != nil {
		return fail(err)
	}
	oneIDHandler, err := identityhttp.NewHandler(identityhttp.Config{
		UnitOfWork: uow, Authenticator: requestSecurity, CSRF: requestSecurity,
		OneID: oneID, SourceConflicts: hxcIdentity, Queries: queries, Audit: auditService,
	})
	if err != nil {
		return fail(err)
	}
	cursorSigningKey := make([]byte, 32)
	if _, err = rand.Read(cursorSigningKey); err != nil {
		return fail(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	segmentBindings.Handler.BindCoreProductOptions(productCatalog)
	segmentBindings.Handler.BindAudienceProductReferences(audienceProductReferenceAdapter{products: productCatalog, historical: orderRepository, uow: uow})
	legacyAudienceSource.Orders = orderRepository
	orderService := orderapp.NewService(uow, orderRepository)
	commercePushService.SetFieldMappingReaders(orderService, customerStore)
	if err = orderService.SetCheckoutCouponCoordinator(couponCheckout); err != nil {
		return fail(err)
	}
	if cfg.WeChatPay.H5OAuthEnabled {
		contactCipher, cipherErr := ordersecure.NewContactCipher(cfg.WeChatPay.OrderContactDataKey)
		if cipherErr != nil {
			return fail(cipherErr)
		}
		if err = orderService.SetContactCipher(contactCipher); err != nil {
			return fail(err)
		}
	}
	if err = productCatalog.SetSalesSummaryReader(productSalesAdapter{orders: orderRepository, refunds: paymentRepository}); err != nil {
		return fail(err)
	}
	entitlements, err := orderapp.NewEntitlementApplication(uow, orderRepository)
	if err != nil {
		return fail(err)
	}
	archiveReader, err := wecomadapter.NewMessageArchiveReader(wecomadapter.MessageArchiveConfig{Enabled: cfg.WeCom.MessageArchiveEnabled, CorpID: cfg.WeCom.CorpID, Secret: cfg.WeCom.MessageArchiveSecret, RunnerPath: cfg.WeCom.MessageArchiveRunnerPath, LibraryPath: cfg.WeCom.MessageArchiveLibraryPath, PrivateKeyPaths: cfg.WeCom.MessageArchivePrivateKeyPaths, Timeout: 15 * time.Second})
	if err != nil {
		return fail(err)
	}
	archiveService := archiveapp.Service{Enabled: cfg.WeCom.MessageArchiveEnabled, ReadEnabled: true, CorpScope: "wecom-corp:" + cfg.WeCom.CorpID, Reader: archiveReader, Identity: oneID, Lineage: queries, Staff: accessRepository, StaffDirectory: accessRepository, Store: archivestore.NewPostgreSQL(), UOW: uow, PageLimit: cfg.WeCom.MessageArchivePageLimit, PageBudget: cfg.WeCom.MessageArchivePageBudget}
	archiveHandler, err := archivehttp.NewHandler(requestSecurity, archiveService, auditService, uow)
	if err != nil {
		return fail(err)
	}

	customerProfileStore := wecom.NewPostgreSQLCustomerSyncStore()
	legacyAudienceSource.PrimaryOwners = customerProfileStore
	sidebarProfiles.Numbers = queries
	openPlatformTimeline := customerTimelineAdapter{uow: uow, reader: customerStore}
	openPlatformScopes := configuredOpenPlatformScopes(cfg.WeCom.CorpID, []string{cfg.HXCDashboard.UnionIDScope, "wechat-open-platform:" + cfg.Survey.OAuthOpenPlatformID}, []string{cfg.Survey.OAuthAppID, cfg.WeChatPay.AppID, cfg.WeChatPay.H5AppID, cfg.WeChatShop.AppID})
	// Questionnaire history has one frozen donor Open Platform scope. Do not
	// infer it from the broader set of configured UnionID integrations.
	openPlatformScopes.SurveyUnionScopes = distinctScopes([]string{"wechat-open-platform:" + cfg.Survey.OAuthOpenPlatformID}, "wechat-open-platform:")
	openPlatformIdentities := openPlatformIdentityAdapter{resolver: oneID, values: queries, directory: queries, machineFacts: queries, machineAudit: accessRepository, uow: uow}
	openPlatformExecutor, err := newOpenPlatformExecutor(openPlatformIdentities, orderService, sidebarProfiles, archiveService, openPlatformTimeline, openPlatformOwnerAdapter{uow: uow, reader: customerProfileStore}, openPlatformScopes)
	if err != nil {
		return fail(err)
	}
	if err = openPlatformExecutor.BindV1Radar(radarQuery, radarManager); err != nil {
		return fail(err)
	}
	if err = openPlatformExecutor.BindV1CustomerDetails(openPlatformCustomerBusinessDetailAdapter{uow: uow, reader: customerProfileStore}); err != nil {
		return fail(err)
	}
	if err = openPlatformExecutor.BindExternalSurveySubmissions(surveySubmissions, openPlatformIdentities); err != nil {
		return fail(err)
	}
	if err = openPlatformExecutor.BindV1CustomerActivities(surveySubmissions, radarQuery, cursorSigningKey); err != nil {
		return fail(err)
	}
	if err = openPlatformExecutor.BindV1Orders(orderRepository, paymentRepository, uow, cursorSigningKey); err != nil {
		return fail(err)
	}
	if err = openPlatformExecutor.BindV1ExternalCursorKey(cursorSigningKey); err != nil {
		return fail(err)
	}
	if err = openPlatformExecutor.BindV1OperationAudit(accessRepository, uow); err != nil {
		return fail(err)
	}
	if err = openPlatformExecutor.BindV1AI(aiService, aiService, uow); err != nil {
		return fail(err)
	}
	openPlatformExecutor.coreAudience = coreOperations
	openPlatformHandler, err := openplatformhttp.NewHandler(openplatformhttp.Config{
		MachineAuthentication: machineService,
		RateLimiter:           machineRateLimiter,
		AdminAuthentication:   authentication,
		Management:            machineService,
		Operations:            openPlatformExecutor,
		Executor:              openPlatformExecutor,
		SessionCookieName:     accesshttp.SessionCookieName,
		CSRFCookieName:        accesshttp.CSRFCookieName,
		TrustedProxyCIDRs:     cfg.OpenPlatform.TrustedProxyCIDRs,
		PublicOrigin:          cfg.PublicOrigin,
		RequestTimeout:        10 * time.Second,
	})
	if err != nil {
		return fail(err)
	}
	if err = productBindings.ProductHandler.SetServicePeriodMemberReaders(entitlements, orderCustomerDisplayNameAdapter{uow: uow, reader: customerStore}); err != nil {
		return fail(err)
	}
	entitlementFulfillment, err := orderapp.NewEntitlementFulfillmentApplication(orderRepository)
	if err != nil {
		return fail(err)
	}
	if err = orderService.SetServicePeriodEntitlementCoordinator(entitlementFulfillment); err != nil {
		return fail(err)
	}
	relationships := wecom.NewPostgreSQLFollowRelationshipStore()
	customerTagCommands, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effectRepository, customerTagCommandGate{uow: uow, corpID: cfg.WeCom.CorpID, owners: customerProfileStore, staff: accessRepository, relationships: relationships, tags: tagRepository, identities: queries}, auditService, platformoutbox.NewPostgreSQL())
	if err != nil {
		return fail(err)
	}
	if err = channelEntrantActions.SetTagCommandSubmitter(customerTagCommands); err != nil {
		return fail(err)
	}
	paidPurchaseActions, err := productapp.NewPaidPurchaseActionService(uow, productRepository, customerTagCommands)
	if err != nil {
		return fail(err)
	}
	paidPurchaseActions.SetPaidGuidanceOrderReader(orderService)
	if err = paidPurchaseActions.SetCheckoutSnapshotReader(orderService); err != nil {
		return fail(err)
	}
	if err = paidPurchaseActions.SetCompletionURLLinkResolver(productapp.NewCompletionURLLinkResolver()); err != nil {
		return fail(err)
	}
	legacyAudienceSource.PrimaryOwners = customerProfileStore
	ownerHandoffCipher, cipherErr := customer.NewOwnerHandoffCipher(cfg.Survey.DataKey)
	if cipherErr != nil {
		return fail(cipherErr)
	}
	ownerHandoffStore := customer.NewPostgreSQLOwnerHandoffStoreWithCipher(ownerHandoffCipher)
	ownerHandoffService, ownerServiceErr := customerapp.NewOwnerHandoffService(uow, ownerHandoffStore, accessRepository, customerOwnerHandoffCandidates{staff: accessRepository, relationships: relationships, relationshipLister: relationships, primaries: customerProfileStore, primaryLister: customerProfileStore, identities: queries, owners: ownerHandoffStore}, auditService, platformoutbox.NewPostgreSQL())
	if ownerServiceErr != nil {
		return fail(ownerServiceErr)
	}
	if ownerServiceErr = ownerHandoffService.SetExternalEffectAccepter(effectRepository); ownerServiceErr != nil {
		return fail(ownerServiceErr)
	}
	if ownerServiceErr = ownerHandoffService.SetBatchEnqueuer(ownerHandoffBatchEnqueuer); ownerServiceErr != nil {
		return fail(ownerServiceErr)
	}
	if ownerServiceErr = ownerHandoffBatchWorker.Bind(ownerHandoffService); ownerServiceErr != nil {
		return fail(ownerServiceErr)
	}
	ownerHandoffService.SetWeComProviderEnabled(cfg.Effects.ProviderEnabled && cfg.WeCom.Enabled && cfg.WeCom.ContactSecret != "")
	ownerHandoffCompletion, ownerCompletionErr := outbound.NewCustomerOwnerHandoffCompletionSink(ownerHandoffStore)
	if ownerCompletionErr != nil {
		return fail(ownerCompletionErr)
	}
	outboundCompletionSink.WithCustomerOwnerHandoff(ownerHandoffCompletion)
	customerHandler, err := customerhttp.NewHandler(customerhttp.Config{UnitOfWork: uow, Auth: requestSecurity, CSRF: requestSecurity,
		Directory: customerapp.Directory{Numbers: queries, Store: customerStore, SigningKey: cursorSigningKey, Tags: customerDirectoryTagFilter{bindings: tagRepository, members: customerProfileStore}}, Store: customerStore, Identities: queries, Audit: auditService,
		Canonical:   canonicalCustomerAdapter{reader: queries},
		Owners:      customerOwnerAdapter{uow: uow, observations: customerProfileStore, users: accessRepository, owners: ownerHandoffStore},
		Tags:        customerTagAdapter{uow: uow, observations: customerProfileStore, names: tagRepository},
		TagCommands: customerTagCommands,
		TagHistory:  customerstore.TagCommandPostgreSQL{},
		Surveys:     customerSurveyAdapter{reader: surveySubmissions},
		Timeline:    sidebarBusinessTimeline{surveys: surveySubmissions, orders: orderService, radar: radarQuery, channels: channelAcquisition, uow: uow}, Chat: disabledCustomerChatActivity{}, Orders: orderService, ProfileSigningKey: cursorSigningKey,
		OwnerHandoff: ownerHandoffService, OwnerHandoffReader: ownerHandoffStore, OwnerHandoffTransfers: ownerHandoffService,
		OwnerHandoffStaff: customerOwnerHandoffStaffDirectory{uow: uow, staff: accessRepository, profiles: groupOpsRepository}, OwnerHandoffCorpScope: "wecom-corp:" + cfg.WeCom.CorpID, OwnerHandoffIdentity: oneID, OwnerHandoffPresentation: customerOwnerHandoffPreviewPresenter{uow: uow, display: customerStore, identities: queries, staff: accessRepository}})
	if err != nil {
		return fail(err)
	}
	orderHandler, err := orderhttp.NewHandler(orderService, requestSecurity, orderCustomerContactDisplayAdapter{numbers: queries, uow: uow, reader: customerStore})
	if err != nil {
		return fail(err)
	}
	if err = orderHandler.SetCustomerFilterResolver(orderCustomerFilterAdapter{uow: uow, oneID: oneID, corpID: cfg.WeCom.CorpID}); err != nil {
		return fail(err)
	}
	if err = orderHandler.SetCheckoutContactReader(orderService); err != nil {
		return fail(err)
	}
	orderRuns := ordermigration.PostgreSQLRuns{Pool: pool.Native()}
	orderImportHandler, err := orderhttp.NewImportHandler(ordermigration.OrderOnlyRunner{Orders: orderService, Runs: orderRuns}, orderRuns, requestSecurity)
	if err != nil {
		return fail(err)
	}
	paymentSession, err := paymentsession.NewService(uow, oneID, customerStore, paymentsession.NewPostgreSQL(), 10*time.Minute)
	if err != nil {
		return fail(err)
	}
	paymentService := paymentapp.NewService(uow, paymentRepository, orderService, paymentSession, effectRepository, effectRepository)
	paymentService.SetCanonicalLineageReader(queries)
	if err = paymentService.SetProfitSharingIdentityReader(queries); err != nil {
		return fail(err)
	}
	if err = paymentService.SetProfitSharingEnabled(cfg.WeChatPay.ProfitSharingEnabled); err != nil {
		return fail(err)
	}
	if err = paymentService.SetCheckoutProductReader(productTargets); err != nil {
		return fail(err)
	}
	if err = paymentService.SetReconciliationEnqueuer(paymentReconciliationEnqueuer); err != nil {
		return fail(err)
	}
	paymentCompletionSink, err := payment.NewCompletionSink(paymentRepository, paymentReconciliationEnqueuer, orderService)
	if err != nil {
		return fail(err)
	}
	if err = effectRepository.SetCompletionSink(composedCompletionRouter{adminops: opsCompletion{observer: opsInspections}, outbound: outboundCompletionSink, payment: paymentCompletionSink, paymentDistribution: paymentService, automation: generationCompletionSink, segment: coreRecommendationCompletion}); err != nil {
		return fail(err)
	}
	if err = paymentReconciliationWorker.BindService(paymentService); err != nil {
		return fail(err)
	}
	var wechatPayAdapter *paymentprovider.WeChatPay
	var alipayAdapter *paymentprovider.Alipay
	var paymentCallbackVerifier *paymentprovider.CallbackVerifier
	if cfg.WeChatPay.Enabled {
		if err = paymentService.SetPaymentChannelAppIDs(cfg.WeChatPay.AppID, cfg.WeChatPay.H5AppID); err != nil {
			return fail(err)
		}
		privateKey, readErr := os.ReadFile(cfg.WeChatPay.PrivateKeyPath)
		if readErr != nil {
			return fail(readErr)
		}
		platformCertificate, readErr := os.ReadFile(cfg.WeChatPay.PlatformCertPath)
		if readErr != nil {
			return fail(readErr)
		}
		signer, parseErr := paymentprovider.ParseMerchantPrivateKey(privateKey)
		if parseErr != nil {
			return fail(parseErr)
		}
		platformSerial, platformKey, parseErr := paymentprovider.ParsePlatformCertificate(platformCertificate)
		if parseErr != nil {
			return fail(parseErr)
		}
		credential := paymentprovider.Credential{MerchantID: cfg.WeChatPay.MerchantID, Serial: cfg.WeChatPay.MerchantSerial, Signer: signer, PlatformKeys: map[string]*rsa.PublicKey{platformSerial: platformKey}}
		loader := paymentprovider.DBMaterialLoader{UOW: uow, Intents: paymentRepository, Identities: queries, AppScope: cfg.WeChatPay.AppScope, H5AppScope: cfg.WeChatPay.H5AppScope, ProfitSharing: paymentService}
		wechatPayAdapter, err = paymentprovider.NewWeChatPay(paymentprovider.Config{Enabled: true, AppID: cfg.WeChatPay.AppID, AppScope: cfg.WeChatPay.AppScope, H5AppID: cfg.WeChatPay.H5AppID, H5AppScope: cfg.WeChatPay.H5AppScope, APIBaseURL: "https://api.mch.weixin.qq.com", PaymentNotifyURL: cfg.PublicOrigin + "/api/public/wechat-pay/callbacks/payment", RefundNotifyURL: cfg.PublicOrigin + "/api/public/wechat-pay/callbacks/refund", Credential: credential}, loader, &http.Client{Timeout: 10 * time.Second})
		if err != nil {
			return fail(err)
		}
		if cfg.WeChatPay.ProfitSharingEnabled {
			merchantRSAKey, rsaOK := signer.(*rsa.PrivateKey)
			if !rsaOK {
				return fail(errors.New("wechat pay merchant signer is not RSA"))
			}
			// Profit-sharing trust material is intentionally selected by the
			// explicit runtime mode. A platform-certificate serial is not a
			// public-key ID, and Composition never falls back to another SDK mode.
			var authentication paymentprovider.ProfitSharingAuthentication
			switch cfg.WeChatPay.ProfitSharingAuthMode {
			case "certificate":
				certificate, certificateErr := paymentprovider.ParsePlatformX509Certificate(platformCertificate)
				if certificateErr != nil {
					return fail(certificateErr)
				}
				authentication = paymentprovider.ProfitSharingAuthentication{Mode: paymentprovider.ProfitSharingAuthenticationCertificate, PlatformCertificate: certificate}
			case "public_key":
				authentication = paymentprovider.ProfitSharingAuthentication{Mode: paymentprovider.ProfitSharingAuthenticationPublicKey, PlatformPublicKeyID: cfg.WeChatPay.ProfitSharingPublicKeyID, PlatformPublicKey: platformKey}
			default:
				return fail(errors.New("invalid enabled WeChat Pay profit-sharing authentication mode"))
			}
			profitSharingSDK, sdkErr := paymentprovider.NewOfficialProfitSharingSDKWithAuthentication(ctx, cfg.WeChatPay.MerchantID, cfg.WeChatPay.MerchantSerial, merchantRSAKey, authentication)
			if sdkErr != nil {
				return fail(sdkErr)
			}
			if err = wechatPayAdapter.SetProfitSharingSDK(profitSharingSDK); err != nil {
				return fail(err)
			}
			if err = paymentService.SetProfitSharingReconciler(wechatPayAdapter); err != nil {
				return fail(err)
			}
		}
		paymentCallbackVerifier, err = paymentprovider.NewCallbackVerifier(credential.PlatformKeys, []byte(cfg.WeChatPay.APIV3Key), cfg.WeChatPay.AppID, cfg.WeChatPay.MerchantID, cfg.WeChatPay.H5AppID)
		if err != nil {
			return fail(err)
		}
	} else {
		wechatPayAdapter, err = paymentprovider.NewWeChatPay(paymentprovider.Config{}, nil, nil)
		if err != nil {
			return fail(err)
		}
	}
	if cfg.Alipay.Enabled {
		if err = paymentService.SetAlipayAppID(cfg.Alipay.AppID); err != nil {
			return fail(err)
		}
		privateKey, readErr := os.ReadFile(cfg.Alipay.PrivateKeyPath)
		if readErr != nil {
			return fail(readErr)
		}
		contentEncryptionKey, readErr := paymentprovider.LoadContentEncryptionKey(cfg.Alipay.ContentEncryptionKeyPath)
		if readErr != nil {
			return fail(readErr)
		}
		alipayAdapter, err = paymentprovider.NewAlipay(paymentprovider.AlipayConfig{
			Enabled: true, Production: cfg.Alipay.Production, AppID: cfg.Alipay.AppID,
			PrivateKey: string(privateKey), Gateway: cfg.Alipay.Gateway, ContentEncryptionKey: contentEncryptionKey, AlipayPublicKey: cfg.Alipay.AlipayPublicKey,
			AppCertPath: cfg.Alipay.AppCertPath, AlipayCertPath: cfg.Alipay.AlipayCertPath, AlipayRootPath: cfg.Alipay.AlipayRootPath,
			NotifyURL: cfg.Alipay.NotifyURL, ReturnURL: cfg.Alipay.ReturnURL,
		})
		if err != nil {
			return fail(err)
		}
		if err = alipayAdapter.SetMaterialLoader(paymentprovider.DBMaterialLoader{UOW: uow, Intents: paymentRepository, Checkouts: orderService}); err != nil {
			return fail(err)
		}
	} else {
		alipayAdapter, err = paymentprovider.NewAlipay(paymentprovider.AlipayConfig{})
		if err != nil {
			return fail(err)
		}
	}
	shopLoader := paymentprovider.DBMaterialLoader{UOW: uow, Intents: paymentRepository}
	wechatShopAdapter, err := paymentprovider.NewWeChatShop(paymentprovider.ShopConfig{Enabled: cfg.WeChatShop.Enabled, AppID: cfg.WeChatShop.AppID, AppSecret: cfg.WeChatShop.AppSecret, APIBaseURL: "https://api.weixin.qq.com"}, shopLoader, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return fail(err)
	}
	if err = paymentService.SetShopReconciler(wechatShopAdapter); err != nil {
		return fail(err)
	}
	if err = paymentService.SetWeChatPayReconciler(wechatPayAdapter); err != nil {
		return fail(err)
	}
	if err = paymentService.SetAlipayReconciler(alipayAdapter); err != nil {
		return fail(err)
	}
	paymentAdapter := paymentProviderRouter{wechatPay: wechatPayAdapter, wechatShop: wechatShopAdapter, alipay: alipayAdapter}
	paymentHandler, err := paymenthttp.NewHandler(paymentService, paymentCallbackVerifier, requestSecurity, cfg.WeChatPay.Enabled || cfg.Alipay.Enabled, cfg.WeChatShop.Enabled, cfg.Alipay.Enabled)
	if err != nil {
		return fail(err)
	}
	if cfg.Alipay.Enabled {
		if err = paymentHandler.SetAlipayCallbackVerifier(alipayAdapter); err != nil {
			return fail(err)
		}
	}
	if err = paymentHandler.SetCommercePushDeliveryReaders(orderService, commercePushService); err != nil {
		return fail(err)
	}
	if err = paymentHandler.SetPaidPurchaseActionReader(paidPurchaseActions, publicLeadQRCodes); err != nil {
		return fail(err)
	}
	if cfg.WeChatPay.Enabled {
		miniProgramVerifier, verifyErr := identityprovider.NewWeChatMiniProgram(identityprovider.WeChatMiniProgramConfig{AppID: cfg.WeChatPay.AppID, AppSecret: cfg.WeChatPay.AppSecret, APIBaseURL: "https://api.weixin.qq.com"}, &http.Client{Timeout: 10 * time.Second})
		if verifyErr != nil {
			return fail(verifyErr)
		}
		if err = paymentHandler.SetTrustedSessionIssuer(miniProgramVerifier, paymentSession); err != nil {
			return fail(err)
		}
	}
	if cfg.WeChatPay.H5OAuthEnabled && cfg.WeChatPay.H5AppID != cfg.Survey.OAuthAppID {
		return fail(errors.New("payment H5 OAuth open-platform scope requires matching configured Official Account"))
	}
	h5OAuthProvider, err := paymentprovider.NewH5OAuthIdentity(cfg.WeChatPay.H5OAuthEnabled, cfg.WeChatPay.H5AppID, cfg.WeChatPay.H5AppSecret, cfg.WeChatPay.H5AppScope, h5PublicOrigin(cfg)+"/api/h5/wechat-pay/oauth/callback", cfg.Survey.OAuthOpenPlatformID)
	if err != nil {
		return fail(err)
	}
	if err = publicServicePeriodHandler.SetTrustedPublicState(uow, paymentSession, entitlements); err != nil {
		return fail(err)
	}
	if err = publicServicePeriodHandler.SetPublicMediaReader(mediaService); err != nil {
		return fail(err)
	}
	if err = publicServicePeriodHandler.SetPublicLeadQRCodeReader(publicLeadQRCodes); err != nil {
		return fail(err)
	}
	h5OAuthService, err := paymenth5oauth.NewService(uow, paymenth5oauth.PostgreSQL{}, h5OAuthProvider, paymentSession)
	if err != nil {
		return fail(err)
	}
	if err = paymentHandler.SetH5OAuth(h5OAuthService); err != nil {
		return fail(err)
	}
	// Referral has its own campaign state and durable close work, but its member
	// actor is the existing trusted WeChat browser session. A missing dedicated
	// invite-token key leaves only Referral unavailable; it must not weaken the
	// existing host's startup configuration or expose an unauthenticated route.
	var referralPublic http.Handler = referralUnavailableHandler{}
	var referralAdmin http.Handler = referralUnavailableHandler{}
	var referralService *referralapp.Service
	var referralAdminService *referralapp.AdminService
	var referralHandler *referralhttp.Handler
	var referralErr error
	var referralPaidConsumer orderport.PaidEventConsumer
	var referralRefundConsumer orderport.RefundSettlementConsumer
	if cfg.Referral.TokenDataKey != "" {
		referralService, err = referralapp.NewService(uow, referralRepository, cfg.PublicOrigin, cfg.Referral.TokenDataKey, referralCampaignCloseEnqueuer, auditService, platformoutbox.NewPostgreSQL())
		if err != nil {
			return fail(err)
		}
		referralPaidConsumer = referralService
		referralRefundConsumer = referralService
		// Product activity checkout facts are owned by Referral and must be
		// frozen in the same Order/Payment transaction as the checkout
		// snapshot.  Bind the coordinator whenever Referral is enabled; this
		// remains independent of Distribution so an organic activity purchase
		// can still create its participant and sale fact.
		if err = orderService.SetProductSaleCheckoutCoordinator(referralService); err != nil {
			return fail(err)
		}
		referralAdminService, err = referralapp.NewAdminService(uow, referralRepository, referralCanonicalCustomerVerifier{resolver: canonicalCustomerAdapter{reader: queries}, identities: queries}, referralCampaignCloseEnqueuer, auditService, platformoutbox.NewPostgreSQL())
		if err != nil {
			return fail(err)
		}
		if err = referralCampaignCloseWorker.BindService(referralAdminService); err != nil {
			return fail(err)
		}
	}
	// Both Referral and Distribution may use this browser-session bridge, but
	// only its scoped, verified Payment identity is required for Referral.
	// Keep the bridge independent of Distribution's payment-enabled commercial
	// capability so activities never require a distributor registration,
	// receiver preparation, or a purchase. An incomplete H5 scope pair leaves
	// Referral fail-closed rather than weakening identity validation.
	var trustedBrowserSessions *distributionapp.BrowserSessionService
	var trustedPaymentSessionBridge *distributionapp.PaymentSessionBridge
	if cfg.WeChatPay.AppID != "" && cfg.WeChatPay.AppScope != "" && (cfg.WeChatPay.H5AppID == "") == (cfg.WeChatPay.H5AppScope == "") {
		trustedBrowserSessions, err = distributionapp.NewBrowserSessionService(uow, distributionRepository)
		if err != nil {
			return fail(err)
		}
		trustedPaymentSessionBridge, err = distributionapp.NewPaymentSessionBridge(uow, paymentSession, trustedBrowserSessions, cfg.WeChatPay.AppID, cfg.WeChatPay.AppScope, cfg.WeChatPay.H5AppID, cfg.WeChatPay.H5AppScope)
		if err != nil {
			return fail(err)
		}
		if referralService != nil && referralAdminService != nil {
			referralHandler, referralErr = referralhttp.NewHandler(referralhttp.Config{
				ProductOptions:    productCatalog,
				ProductTargets:    productTargets,
				Public:            referralService,
				Admin:             referralAdminService,
				Sessions:          trustedBrowserSessions,
				Bridge:            trustedPaymentSessionBridge,
				Names:             orderCustomerDisplayNameAdapter{uow: uow, reader: customerStore},
				Profiles:          referralCustomerProfileAdapter{uow: uow, reader: customerStore},
				Security:          requestSecurity,
				CookieSecure:      true,
				AllowedOrigins:    []string{cfg.PublicOrigin, h5PublicOrigin(cfg)},
				SessionCookieName: distributionhttp.DistributionSessionCookieName,
				CSRFCookieName:    distributionhttp.DistributionCSRFCookieName,
				CSRFHeader:        distributionhttp.DistributionCSRFHeader,
			})
			if referralErr != nil {
				return fail(referralErr)
			}
			referralPublic = http.HandlerFunc(referralHandler.ServePublicHTTP)
			referralAdmin = http.HandlerFunc(referralHandler.ServeAdminHTTP)
		}
	}
	// Distribution is a separate external-customer capability. It is composed
	// only when the configured Payment channel can provide the exact scoped
	// WeChat identity and provider boundary it needs; otherwise every public
	// Distribution route fails closed and no paid-event commission consumer is
	// installed.
	var distributionPublic http.Handler = distributionUnavailableHandler{}
	var distributionAdmin http.Handler = distributionUnavailableHandler{}
	var distributionCommissionConsumer orderport.PaidEventConsumer
	var distributionRefundConsumer orderport.RefundSettlementConsumer
	if cfg.WeChatPay.Enabled && cfg.WeChatPay.AppID != "" && cfg.WeChatPay.AppScope != "" {
		if trustedBrowserSessions == nil || trustedPaymentSessionBridge == nil {
			return fail(errors.New("distribution trusted Payment session bridge is unavailable"))
		}
		qualificationService, distributionErr := distributionapp.NewQualificationService(queries, orderService, paymentService)
		if distributionErr != nil {
			return fail(distributionErr)
		}
		if referralService != nil {
			if distributionErr = referralService.SetPurchaseQualificationReader(qualificationService); distributionErr != nil {
				return fail(distributionErr)
			}
		}
		registration, distributionErr := distributionapp.NewRegistrationService(uow, distributionRepository, paymentService)
		if distributionErr != nil {
			return fail(distributionErr)
		}
		promotion, distributionErr := distributionapp.NewPromotionService(uow, distributionRepository, qualificationService, productCatalog, productTargets, queries, cfg.PublicOrigin, cfg.Survey.DataKey)
		if distributionErr != nil {
			return fail(distributionErr)
		}
		promotion.SetSettlementEnabled(cfg.WeChatPay.ProfitSharingEnabled)
		if referralHandler != nil {
			if distributionErr = referralHandler.SetPromotionApplication(promotion); distributionErr != nil {
				return fail(distributionErr)
			}
		}
		commissionService, distributionErr := distributionapp.NewCommissionService(distributionRepository, distributionDueEnqueuer, qualificationService)
		if distributionErr != nil {
			return fail(distributionErr)
		}
		if referralService != nil {
			if distributionErr = referralService.SetSaleEvidenceReaders(commissionService, platformaudit.NewPostgreSQLStore()); distributionErr != nil {
				return fail(distributionErr)
			}
		}
		refundService, distributionErr := distributionapp.NewRefundService(uow, distributionRepository, distributionDueEnqueuer, distributionRefundEnqueuer, qualificationService, orderService)
		if distributionErr != nil {
			return fail(distributionErr)
		}
		settlementService, distributionErr := distributionapp.NewSettlementService(uow, distributionRepository, qualificationService, paymentService)
		if distributionErr != nil {
			return fail(distributionErr)
		}
		if distributionErr = distributionDueWorker.BindService(settlementService); distributionErr != nil {
			return fail(distributionErr)
		}
		if distributionErr = distributionRefundWorker.BindService(refundService); distributionErr != nil {
			return fail(distributionErr)
		}
		if distributionErr = orderService.SetCheckoutAttributionCoordinator(promotion); distributionErr != nil {
			return fail(distributionErr)
		}
		if distributionErr = orderService.SetRefundSettlementConsumer(refundService); distributionErr != nil {
			return fail(distributionErr)
		}
		if distributionErr = paymentService.SetRefundExposureConsumer(refundService); distributionErr != nil {
			return fail(distributionErr)
		}
		if distributionErr = paymentService.SetProfitSharingReceiverStatusObserver(registration); distributionErr != nil {
			return fail(distributionErr)
		}
		readModelService, distributionErr := distributionapp.NewReadModelService(uow, distributionRepository)
		if distributionErr != nil {
			return fail(distributionErr)
		}
		if distributionErr = readModelService.SetDirectoryDisplayNameReader(orderCustomerDisplayNameAdapter{uow: uow, reader: customerStore}); distributionErr != nil {
			return fail(distributionErr)
		}
		if distributionErr = orderHandler.SetDistributionReader(readModelService); distributionErr != nil {
			return fail(distributionErr)
		}
		distributionPublic, distributionErr = distributionhttp.NewHandler(distributionhttp.Config{Registration: registration, Promotion: promotion, Earnings: readModelService, Sessions: trustedBrowserSessions, Bridge: trustedPaymentSessionBridge, CookieSecure: true, AllowedOrigins: []string{cfg.PublicOrigin, h5PublicOrigin(cfg)}})
		if distributionErr != nil {
			return fail(distributionErr)
		}
		adminService, distributionErr := distributionapp.NewAdminService(uow, distributionRepository, paymentService)
		if distributionErr != nil {
			return fail(distributionErr)
		}
		distributionAdmin, distributionErr = distributionhttp.NewAdminHandler(distributionhttp.AdminConfig{Reader: readModelService, Commands: adminService, Security: requestSecurity})
		if distributionErr != nil {
			return fail(distributionErr)
		}
		distributionCommissionConsumer = commissionService
		distributionRefundConsumer = refundService
	}
	if err = orderService.SetPaidEventConsumer(orderPaidEventFanout{commerce: commercePushService, purchase: paidPurchaseActions, distribution: distributionCommissionConsumer, referral: referralPaidConsumer}); err != nil {
		return fail(err)
	}
	if distributionRefundConsumer != nil || referralRefundConsumer != nil {
		if err = orderService.SetRefundSettlementConsumer(orderRefundSettlementFanout{distribution: distributionRefundConsumer, referral: referralRefundConsumer}); err != nil {
			return fail(err)
		}
	}
	couponPublicHandler, err := couponhttp.NewPublicHandler(couponPublic, couponCheckout, paymentSession, productTargets, uow)
	if err != nil {
		return fail(err)
	}
	if cfg.WeChatShop.Enabled {
		shopCredential, credentialErr := paymentprovider.NewShopCallbackCredential(cfg.WeChatShop.AppID, cfg.WeChatShop.CallbackToken, cfg.WeChatShop.CallbackEncodingAESKey)
		if credentialErr != nil {
			return fail(credentialErr)
		}
		shopVerifier, verifierErr := paymentprovider.NewShopCallbackVerifier(shopCredential)
		if verifierErr != nil {
			return fail(verifierErr)
		}
		if err = paymentHandler.SetShopCallbackVerifier(shopVerifier); err != nil {
			return fail(err)
		}
	}

	renderer, err := webshell.NewRenderer("web/dist")
	if err != nil {
		return fail(err)
	}
	accessHandler, err := accesshttp.NewHandler(accesshttp.Config{
		Renderer: renderer, Auth: authentication, Management: management, CookieSecure: true, CookiePath: "/",
	})
	if err != nil {
		return fail(err)
	}
	shellHandler, err := webshell.NewHandler(webshell.HandlerOptions{Renderer: renderer, DistDir: "web/dist"})
	if err != nil {
		return fail(err)
	}

	providerClient, err := providerFactory(weComProviderConfig(cfg))
	if err != nil {
		return fail(err)
	}
	if err = audienceDirectPush.BindObservation(
		audienceDirectPushDeliveryAdapter{receipts: outboundMessages, provider: providerClient},
		audienceDirectPushOpenAdapter{uow: uow, identities: queries, excel: excelClient, unionScope: audienceUnionScope},
		audienceDirectPushScheduler,
	); err != nil {
		return fail(err)
	}
	catalogService.Provider = providerClient
	// Enterprise employee selection is a separate, read-only application
	// directory capability. It never reuses the external-contact follow-user
	// subset and remains unavailable when the scoped provider/key is absent.
	if providerClient.EnterpriseDirectoryReady() && len(cfg.WeCom.ContextSigningKey) >= 32 {
		if err = management.SetEnterpriseEmployeeDirectory(providerClient, []byte(cfg.WeCom.ContextSigningKey), cfg.WeCom.CorpID); err != nil {
			return fail(err)
		}
	}
	// The transfer-result endpoint is a read-only WeCom protocol leaf. Keep the
	// Customer UoW separate from this Provider call; its service persists the
	// returned status projection only after the read finishes.
	if err = ownerHandoffService.SetTransferResultReader(providerClient); err != nil {
		return fail(err)
	}
	groupOpsDirectory.groups = providerClient
	groupOpsDirectory.staffs = providerClient
	groupOpsDirectory.profiles = providerClient
	if cfg.Effects.ProviderEnabled && cfg.WeCom.Enabled && cfg.GroupOps.ProviderEnabled {
		groupOpsEvidence = wecomGroupOpsEvidence{uow: uow, receipts: groupOpsRepository, reader: providerClient}
		groupOpsRuntime.SetEvidenceVerifier(groupOpsEvidence)
	}
	groupOpsProvider, err = outbound.NewGroupMessageProvider(outbound.GroupMessageProviderConfig{
		Enabled:           cfg.Effects.ProviderEnabled && cfg.WeCom.Enabled && cfg.GroupOps.ProviderEnabled,
		PreparationWriter: mediaPreparationBindings.Writer,
		Executions:        groupOpsDispatchReader{uow: uow, execution: groupOpsRepository, senders: groupOpsStaff},
		Materials:         groupOpsMaterialReadinessAdapter{uow: uow, capturer: mediaContentBindings.SourceCapturer, freezer: materialFreezer},
		FrozenSources:     groupOpsMaterialReadinessAdapter{uow: uow, capturer: mediaContentBindings.SourceCapturer},
		Writer:            providerClient,
		Sources:           mediaRepository,
		Preparer:          materialPreparation,
		ScopeDigest:       materialScopeDigest,
	})
	if err != nil {
		return fail(err)
	}
	staffDirectoryRefresh := wecom.StaffDirectoryRefreshService{
		Enabled: cfg.WeCom.ChannelProviderReadEnabled, Provider: providerClient, Projector: staffProjector,
		Store: wecom.NewPostgreSQLStaffDirectoryRefreshStore(), Audit: auditService, UOW: uow,
	}
	if err = staffDirectoryWorker.BindService(staffDirectoryRefresh); err != nil {
		return fail(err)
	}
	if err = channelAssetService.SetProvider(channelFollowUserGate{enabled: cfg.WeCom.ChannelProviderReadEnabled, source: providerClient}); err != nil {
		return fail(err)
	}
	channelAcquisitionService := channelstore.NewAcquisitionService(uow, channelCatalogService, channelStaffReferenceAdapter{users: accessRepository, profiles: groupOpsRepository}, channelFollowUserGate{enabled: cfg.WeCom.ChannelProviderReadEnabled, source: providerClient})
	if err = channelAssetService.SetPublishValidator(channelAcquisitionService); err != nil {
		return fail(err)
	}
	channelAcquisitionHandler, err := channelstore.NewAcquisitionHTTPHandler(channelAcquisitionService, requestSecurity, channelstore.AcquisitionHTTPOptions{
		ProviderReadEnabled:  cfg.WeCom.ChannelProviderReadEnabled,
		ProviderWriteEnabled: cfg.Effects.ProviderEnabled && cfg.WeCom.ChannelQRProviderEnabled,
	})
	if err != nil {
		return fail(err)
	}
	channelHistoryService := channelstore.NewHistoryService(uow, channelCatalogService, channelstore.NewPostgreSQLStore())
	channelHistoryHandler, err := channelstore.NewHistoryHTTPHandler(channelHistoryService, requestSecurity, channelCursorKey)
	if err != nil {
		return fail(err)
	}
	channelLinkService := channelstore.NewAcquisitionLinkService(uow, channelLinkStore, effectRepository)
	channelLinkGate := channelLinkProviderGate{read: cfg.WeCom.ChannelProviderReadEnabled, write: cfg.WeCom.ChannelQRProviderEnabled, source: providerClient}
	if err = channelLinkService.SetProvider(channelLinkGate); err != nil {
		return fail(err)
	}
	if err = channelLinkService.SetReconciler(channelLinkReconciler{uow: uow, store: channelLinkStore, effects: effectRepository, provider: channelLinkGate}); err != nil {
		return fail(err)
	}
	channelLinkHandler, err := channelstore.NewAcquisitionLinkHTTPHandler(channelLinkService, requestSecurity)
	if err != nil {
		return fail(err)
	}
	channelCenter := channelstore.CenterHTTPHandler{Catalog: channelCatalog, Acquisition: channelAcquisitionHandler, History: channelHistoryHandler, Assets: channelAssetHandler}
	var callbackCrypto *wecom.CallbackCrypto
	var welcomeGrantStore *wecom.PostgreSQLWelcomeGrantStore
	if cfg.WeCom.CallbackEnabled {
		callbackCrypto, err = wecom.NewCallbackCrypto(cfg.WeCom.CallbackToken, cfg.WeCom.CallbackAESKey, cfg.WeCom.CorpID)
		if err != nil {
			return fail(err)
		}
		welcomeGrantCipher, cipherErr := wecom.NewWelcomeGrantCipher(cfg.WeCom.CallbackAESKey)
		if cipherErr != nil {
			return fail(cipherErr)
		}
		welcomeGrantStore = wecom.NewPostgreSQLWelcomeGrantStore(welcomeGrantCipher)
		welcomeMessageCipher, cipherErr := wecom.NewChannelWelcomeMessageCipher(cfg.WeCom.CallbackAESKey)
		if cipherErr != nil {
			return fail(cipherErr)
		}
		if err = channelEntrantActions.SetWelcomeMessageDependencies(customerStore, welcomeMessageCipher); err != nil {
			return fail(err)
		}
	}
	legacyAudienceSource.RegistrationFacts = customerStore
	legacyAudienceSource.Contacts = relationships
	legacyAudienceSource.RecognizedContacts = relationships
	var channelAssetProvider effectport.ProviderAdapter
	var channelEntrantProvider effectport.ProviderAdapter
	var channelLinkProvider effectport.ProviderAdapter
	if cfg.WeCom.ChannelQRProviderEnabled && cfg.WeCom.CallbackEnabled {
		channelAssetProvider = outbound.NewChannelAssetProvider(channelPublishedConfigAdapter{uow: uow, assets: channelAssetStore, users: accessRepository}, providerClient)
	}
	if (cfg.WeCom.ChannelWelcomeProviderEnabled || cfg.WeCom.ChannelTagProviderEnabled) && cfg.WeCom.CallbackEnabled {
		entrantProvider := outbound.NewChannelEntrantProvider(
			channelEntrantActionReaderAdapter{uow: uow, source: channelEntrantActions}, channelEntrantActionReaderAdapter{uow: uow, source: channelEntrantActions}, uow, welcomeGrantStore,
			channelCurrentContactAdapter{uow: uow, corpID: cfg.WeCom.CorpID, staff: accessRepository, relationships: relationships, identities: queries},
			channelProviderTagAdapter{uow: uow, tags: tagRepository}, providerClient,
		)
		channelEntrantProvider = channelEntrantProviderGate{welcome: cfg.WeCom.ChannelWelcomeProviderEnabled, tag: cfg.WeCom.ChannelTagProviderEnabled, source: entrantProvider}
	}
	if cfg.WeCom.ChannelQRProviderEnabled {
		channelLinkProvider = outbound.NewChannelLinkProvider(channelLinkMutationReaderAdapter{uow: uow, source: channelLinkStore}, providerClient)
	}
	genericCustomerTagEnabled := cfg.Effects.ProviderEnabled && cfg.WeCom.Enabled && cfg.WeCom.CustomerTagProviderEnabled
	channelEntryTagEnabled := cfg.Effects.ProviderEnabled && cfg.WeCom.Enabled && cfg.WeCom.CallbackEnabled && cfg.WeCom.ChannelTagProviderEnabled
	// Channel entry tags now share Customer's command ownership, but keep the
	// legacy Channel Tag/callback capability boundary by persisted source.
	// Readback is enabled only after one of those write paths was authorized;
	// CustomerTagProvider calls it only after a confirmed mark_tag success.
	customerTagObservationRefresh := wecom.CustomerTagObservationService{Enabled: genericCustomerTagEnabled || channelEntryTagEnabled, CorpID: cfg.WeCom.CorpID, Provider: providerClient, Store: customerProfileStore, UOW: uow}
	customerTagProvider, err := outbound.NewCustomerTagProvider(outbound.CustomerTagProviderConfig{GenericEnabled: genericCustomerTagEnabled, ChannelEntryTagEnabled: channelEntryTagEnabled}, customerTagCommandReaderAdapter{uow: uow, source: customerstore.TagCommandPostgreSQL{}}, channelCurrentContactAdapter{uow: uow, corpID: cfg.WeCom.CorpID, staff: accessRepository, relationships: relationships, identities: queries}, channelProviderTagAdapter{uow: uow, tags: tagRepository}, providerClient, customerTagObservationRefresh)
	if err != nil {
		return fail(err)
	}
	contactDescriptionIntents, err := outbound.NewContactDescriptionIntentStore(pool.Native(), effectRepository)
	if err != nil {
		return fail(err)
	}
	contactDescriptionProvider, err := outbound.NewContactDescriptionProvider(cfg.WeCom.ContactDescriptionProviderEnabled, contactDescriptionIntents, contactDescriptionTargetAdapter{uow: uow, corpID: cfg.WeCom.CorpID, identities: queries}, providerClient, providerClient)
	if err != nil {
		return fail(err)
	}
	contactDescriptionCompletionSink, err := outbound.NewContactDescriptionCompletionSink(contactDescriptionIntents)
	if err != nil {
		return fail(err)
	}
	outboundCompletionSink.WithContactDescription(contactDescriptionCompletionSink)
	if excelClient != nil {
		excelClient.MediaCovers = mediaRepository
	}
	excelBridge := &aiexcel.Bridge{Client: excelClient, App: aiService, Repo: aiRepository, Receipts: privateWriter, Provider: providerClient, Security: requestSecurity, Authorizer: accessapp.AIAssistantAuthorizer{}, Scope: "wechat-open-platform:" + cfg.Survey.OAuthOpenPlatformID, Covers: mediaRepository, Strategies: operationCycleExcelStrategyPageAdapter{read: operationService}}
	excelWorker.Bridge = excelBridge
	aiService.ExcelSnapshot = excelBridge.PrepareSnapshot
	privateProvider, err := outbound.NewPrivateMessageProvider(cfg.AIAssistant.DispatchEnabled, privateWriter, aiPrivateTargetResolver{deferred: aiRepository, resolver: oneID, trusted: queries, uow: uow, identities: queries, access: accessRepository, relationships: relationships, corpID: cfg.WeCom.CorpID}, aiPrivatePayloadReader{excel: excelClient, content: aiRepository, images: mediaService, materials: mediaRepository, attachments: mediaService, uow: uow, capturer: mediaRepository, sources: materialSources, preparer: materialPreparation, scopeDigest: materialScopeDigest}, providerClient)
	if err != nil {
		return fail(err)
	}
	var tagCatalogProvider externaleffects.ProviderAdapter
	var tagCatalogMutationProvider externaleffects.ProviderAdapter
	if cfg.TagCatalog.Enabled {
		catalogReader, readerErr := outbound.NewWeComTagCatalogReader(providerClient)
		if readerErr != nil {
			return fail(readerErr)
		}
		catalogProvider, providerErr := outbound.NewTagCatalogProvider(catalogReader)
		if providerErr != nil {
			return fail(providerErr)
		}
		tagCatalogProvider = catalogProvider
		if cfg.TagCatalog.MutationEnabled {
			mutationProvider, mutationErr := outbound.NewTagCatalogMutationProvider(tagCatalogDispatchReader{uow: uow, reader: tagRepository}, providerClient, catalogReader)
			if mutationErr != nil {
				return fail(mutationErr)
			}
			tagCatalogMutationProvider = mutationProvider
		}
	}
	messageProvider, providerErr := outbound.NewMessageProvider(outbound.MessageProviderConfig{Enabled: cfg.Effects.ProviderEnabled && cfg.WeCom.Enabled && cfg.AutomationOperations.ProviderEnabled(), CorpScope: "wecom-corp:" + cfg.WeCom.CorpID, Executions: outboundMessages, Identities: outboundIdentityAdapter{uow: uow, reader: queries}, Staff: segmentStaff, Content: automationService, Payloads: automationFrozenPayloadReader{preparer: aiPrivatePayloadReader{images: mediaService, materials: mediaRepository, attachments: mediaService, uow: uow, capturer: mediaRepository, sources: materialSources, preparer: materialPreparation, scopeDigest: materialScopeDigest}}, Writer: providerClient})
	if providerErr != nil {
		return fail(providerErr)
	}
	ownerHandoffProvider, ownerProviderErr := outbound.NewCustomerOwnerHandoffProvider(customerOwnerHandoffExecutionAdapter{uow: uow, executions: ownerHandoffStore, staff: accessRepository}, providerClient)
	if ownerProviderErr != nil {
		return fail(ownerProviderErr)
	}
	commercePushProvider, err := outbound.NewCommercePushProvider(cfg.CommercePush.ProviderEnabled, commercePushService, commercePushTargetResolver, commercePushCipher)
	if err != nil {
		return fail(err)
	}
	generationProvider, err := automationprovider.NewGenerationProvider(automationprovider.GenerationConfig{Enabled: cfg.AIGeneration.Enabled, BaseURL: cfg.AIGeneration.BaseURL, APIKey: cfg.AIGeneration.APIKey, Model: cfg.AIGeneration.Model, Timeout: cfg.AIGeneration.Timeout}, automationRuntime)
	if err != nil {
		return fail(err)
	}
	generationProvider.ConfigReader = func(ctx context.Context) (automationprovider.GenerationConfig, error) {
		stored, found, err := aiModelSettings.ReadAIModelRuntime(ctx)
		current := automationprovider.GenerationConfig{Enabled: cfg.AIGeneration.Enabled, BaseURL: cfg.AIGeneration.BaseURL, APIKey: cfg.AIGeneration.APIKey, Model: cfg.AIGeneration.Model, Timeout: cfg.AIGeneration.Timeout}
		if found {
			current.BaseURL = stored.BaseURL
			current.APIKey = stored.APIKey
			current.Model = stored.Model
		}
		return current, err
	}
	generationContext := dynamicGenerationContextAdapter{
		questionnaires: surveySubmissions,
		messages:       archiveService,
		tags:           customerTagAdapter{uow: uow, observations: customerProfileStore, names: tagRepository},
		profiles:       sidebarProfiles,
	}
	coreRecommendationProvider, err := automationprovider.NewAudienceRecommendationProvider(automationprovider.GenerationConfig{Enabled: cfg.AIGeneration.Enabled, BaseURL: cfg.AIGeneration.BaseURL, APIKey: cfg.AIGeneration.APIKey, Model: cfg.AIGeneration.Model, Timeout: cfg.AIGeneration.Timeout}, coreOperations)
	if err != nil {
		return fail(err)
	}
	coreRecommendationProvider.ConfigReader = generationProvider.ConfigReader
	coreOperations.BindRecommendationRuntime(effectRepository, generationContext, coreRecommendationProvider)
	if err = automationRuntime.SetDynamicGenerationDependencies(effectRepository, generationContext, automationService, generationProvider); err != nil {
		return fail(err)
	}
	var sidebarMediaProvider externaleffects.ProviderAdapter
	var materialProvider *outbound.MaterialPreparationProvider
	if cfg.WeCom.Enabled && cfg.Effects.ProviderEnabled {
		sidebarMediaProvider, err = outbound.NewSidebarMediaPreparationProvider(sidebarMediaPreparation, providerClient)
		if err != nil {
			return fail(err)
		}
		materialUploader, uploaderErr := wecomadapter.NewMaterialUploader(providerClient, materialScopeDigest)
		if uploaderErr != nil {
			return fail(uploaderErr)
		}
		materialProvider, err = outbound.NewMaterialPreparationProvider(materialPreparation, materialUploader)
		if err != nil {
			return fail(err)
		}
	}
	materialProviderMux := outbound.MaterialEffectMux{GenericProvider: materialProvider, LegacyProvider: sidebarMediaProvider}
	providerRouter := outbound.NewProviderRouterWithGroupMessageAndChannels(tagCatalogProvider, groupOpsProvider, channelAssetProvider, channelEntrantProvider, channelLinkProvider).WithTagCatalogMutation(tagCatalogMutationProvider).WithContactDescription(contactDescriptionProvider).WithCustomerTag(customerTagProvider).WithPrivateMessage(privateProvider).WithAutomationMessage(messageProvider).WithSidebarJSSDK(sidebarExpiry).WithSidebarMedia(materialProviderMux).WithSurveyCompletion(surveyCompletionProvider).WithCommercePush(commercePushProvider).WithCustomerOwnerHandoff(ownerHandoffProvider).WithInvitationCode(&outbound.InvitationCodeProvider{Store: mediaRepository, Provider: providerClient, Enabled: invitationService.WriteEnabled})
	if err = effectsModule.SetProviderAdapter(composedProviderRouter{adminops: opsProvider, outbound: providerRouter, payment: paymentAdapter, automation: generationProvider, segment: coreRecommendationProvider}); err != nil {
		return fail(err)
	}
	callbackReceipts := wecom.NewPostgreSQLCallbackReceiptStore()
	oauthStates := wecom.NewPostgreSQLOAuthStateStore()
	callbackDescriptionService := wecom.ContactDescriptionCallbackService{Enabled: cfg.WeCom.ContactDescriptionProviderEnabled && cfg.WeCom.CallbackEnabled,
		CorpID: cfg.WeCom.CorpID, Inbox: inboxService, Provider: providerClient, Identity: oneID, Relationships: relationships, Intents: contactDescriptionIntents, UOW: uow}
	if cfg.WeCom.ContactDescriptionProviderEnabled && cfg.WeCom.CallbackEnabled {
		if err = contactDescriptionCallbackWorker.BindService(callbackDescriptionService); err != nil {
			return fail(err)
		}
	}
	weComProcessor := wecom.InboxProcessor{
		Enabled: cfg.WeCom.CallbackEnabled, CorpID: cfg.WeCom.CorpID, Inbox: inboxService, UOW: uow,
		Lifecycle: wecom.ExternalContactLifecycle{
			Identity: oneID, Relationships: relationships, States: channelAcquisition, Entrants: channelAcquisition, Actions: channelEntrantActions,
			Directory: customerStore, Outbox: platformoutbox.NewPostgreSQL(),
		},
		Receipts: callbackReceipts, Audit: auditService,
	}
	if cfg.WeCom.ContactDescriptionProviderEnabled && cfg.WeCom.CallbackEnabled {
		weComProcessor.DescriptionJobs = contactDescriptionCallbackEnqueuer
	}
	weComArchiveProcessor := wecom.ArchiveInboxProcessor{Enabled: cfg.WeCom.MessageArchiveEnabled, Inbox: inboxService, UOW: uow, Archive: archiveService}
	customerSync := wecom.CustomerSyncService{Enabled: cfg.WeCom.CustomerSyncEnabled, CorpID: cfg.WeCom.CorpID, Provider: providerClient,
		Identity: oneID, Projection: customerStore, Timeline: customerStore, Store: customerProfileStore, Outbox: platformoutbox.NewPostgreSQL(),
		Enqueuer: customerSyncEnqueuer, DescriptionSourceCoverage: customerProfileStore, Audit: auditService, UOW: uow}
	if cfg.WeCom.ContactDescriptionProviderEnabled {
		customerSync.DescriptionIntents = contactDescriptionIntents
	}
	if err = customerSyncWorker.BindService(customerSync); err != nil && cfg.WeCom.CustomerSyncEnabled {
		return fail(err)
	}
	if cfg.HXCDashboard.Enabled {
		hxcSource, err = hxcprovider.Open(cfg.HXCDashboard.SourceDSN)
		if err != nil {
			return fail(err)
		}
	}
	hxcModule := hxc.NewModuleRegistration()
	hxcRepository := hxcstore.NewPostgreSQL(pool.Native())
	// Product receives only HXC's versioned shared-facts Port. It never opens
	// an HXC store or reads dashboard tables while composing the member grid.
	if err = productBindings.ProductHandler.SetServicePeriodMemberSharedFacts(hxcRepository); err != nil {
		return fail(err)
	}
	legacyAudienceSource.MemberFacts = hxcRepository
	legacyAudienceSource.HXCRegistration = hxcRepository
	hxcDashboard := hxcapp.Service{Enabled: cfg.HXCDashboard.Enabled, Scope: cfg.HXCDashboard.UnionIDScope, SubjectKey: []byte(cfg.HXCDashboard.SubjectHMACKey), Source: hxcSource, Identity: hxcIdentity, RegistrationCoverage: queries, IdentityWriteEnabled: cfg.HXCDashboard.IdentityWriteEnabled, UnionIDVerified: cfg.HXCDashboard.UnionIDVerified, Store: hxcRepository, Enqueuer: hxcEnqueuer, Audit: auditService, UOW: uow}
	hxcDashboardWorker.Service = &hxcDashboard
	hxcHandler := hxchttp.Handler{Service: hxcDashboard, Store: hxcRepository, Auth: requestSecurity, Key: []byte(cfg.HXCDashboard.SubjectHMACKey)}
	syncHandler := wecom.CustomerSyncHTTPHandler{Service: customerSync, Auth: requestSecurity, CSRF: requestSecurity,
		DescriptionEnabled: cfg.WeCom.ContactDescriptionProviderEnabled, DescriptionStatus: contactDescriptionIntents, DescriptionReadbacks: contactDescriptionIntents, UOW: uow}
	sidebarContextTokens := wecom.ContextTokenService{CorpID: cfg.WeCom.CorpID, SigningKey: []byte(cfg.WeCom.ContextSigningKey), TTL: cfg.WeCom.ContextTokenTTL}
	callbackDispatcher := wecom.CallbackEventDispatcher{ExternalContact: wecom.ExternalContactCallbackDispatcher{StateDigester: callbackStateDigester, Inbox: inboxService, UOW: uow, WelcomeGrants: welcomeGrantStore, WelcomeActions: channelEntrantActions, States: channelAcquisition}}
	if cfg.WeCom.MessageArchiveEnabled {
		callbackDispatcher.Archive = wecom.ArchiveCallbackDispatcher{Inbox: inboxService, UOW: uow}
	}
	weComHandler, err := wecom.NewHTTPHandler(wecom.HTTPHandlerOptions{
		Callback: wecom.CallbackHandler{Enabled: cfg.WeCom.CallbackEnabled, Crypto: callbackCrypto, StateDigester: callbackStateDigester, Inbox: inboxService, UOW: uow, WelcomeGrants: welcomeGrantStore, WelcomeActions: channelEntrantActions, States: channelAcquisition, Dispatcher: callbackDispatcher},
		OAuth: wecom.OAuthService{Enabled: cfg.WeCom.Enabled, CorpID: cfg.WeCom.CorpID, StateStore: oauthStates, UOW: uow,
			Client: providerClient, AllowedPaths: allowedOAuthRedirects(), StateTTL: 10 * time.Minute},
		ContextTokens: sidebarContextTokens,
		JSSDKSigner:   providerClient, JSSDKOrigin: cfg.PublicOrigin,
		PrincipalResolver: sidebarPrincipalResolver{authentication: authentication, users: accessRepository, uow: uow, corpID: cfg.WeCom.CorpID},
		SessionIssuer:     weComSessionIssuer{authentication: authentication},
		ExistingIdentity:  existingWeComIdentityResolver{service: oneID, uow: uow, corpID: cfg.WeCom.CorpID}, CookieSecure: true,
	})
	if err != nil {
		return fail(err)
	}
	sidebarSends, err := outbound.NewSidebarSendService(uow, effectRepository)
	if err != nil {
		return fail(err)
	}
	sidebarHandler, err := sidebar.NewHandler(sidebar.Config{
		Contexts: sidebarContextAdapter{tokens: sidebarContextTokens}, Profiles: sidebarProfiles,
		Viewer: sidebarViewerBootstrapper{
			principals: sidebarPrincipalResolver{authentication: authentication, users: accessRepository, uow: uow, corpID: cfg.WeCom.CorpID},
			identity:   existingWeComIdentityResolver{service: oneID, uow: uow, corpID: cfg.WeCom.CorpID},
			tokens:     sidebarContextTokens,
		},
		Surveys: customerSurveyAdapter{reader: surveySubmissions}, Timeline: sidebarBusinessTimeline{surveys: surveySubmissions, orders: orderService, radar: radarQuery, channels: channelAcquisition, uow: uow},
		Products: productCatalog, ProductByID: productTargets, Orders: orderService, Entitlements: entitlements,
		Coupons: sidebarCouponCatalog, Materials: mediaLibrary, MaterialSend: sidebarImagePreparation{sources: mediaRepository, preparer: materialPreparation, scopeDigest: materialScopeDigest, enabled: cfg.WeCom.Enabled && cfg.Effects.ProviderEnabled}, ImageVariants: mediaService, Radar: radarManager, Sends: sidebarSends, PublicOrigin: cfg.PublicOrigin, CursorSigningKey: cursorSigningKey,
	})
	if err != nil {
		return fail(err)
	}
	var groupMembershipRefresh wecom.GroupMembershipRefresher
	if cfg.WeCom.ChannelProviderReadEnabled {
		groupMembershipRefresh = wecom.GroupMembershipRefresh{Provider: providerClient, Resolver: oneID, Store: wecom.PostgreSQLGroupMembershipFacts{}, Audit: auditService, UOW: uow, CorpScope: "wecom-corp:" + cfg.WeCom.CorpID}
	}
	preparedAudienceSchedule.refresh = groupMembershipRefresh
	callbackAdminHandler, err := wecom.NewCallbackAdminHandler(wecom.CallbackAdminConfig{
		UnitOfWork: uow, Authenticator: requestSecurity, CSRF: requestSecurity,
		Receipts: callbackReceipts, Retrier: inboxService, GroupMembership: groupMembershipRefresh,
	})
	if err != nil {
		return fail(err)
	}
	entrantAdminHandler, err := channelstore.NewEntrantAdminHandler(channelstore.EntrantAdminConfig{
		UnitOfWork: uow, Authenticator: requestSecurity, CSRF: requestSecurity,
		Receipts: channelAcquisition, Audit: auditService,
	})
	if err != nil {
		return fail(err)
	}
	adminAPIs := http.NewServeMux()
	adminAPIs.Handle("/api/admin/oneid/", oneIDHandler.Routes())
	adminAPIs.Handle("/api/admin/wecom/", callbackAdminHandler.Routes())
	adminAPIs.Handle("/api/admin/wecom/contact-description-backfills", syncHandler.Routes())
	adminAPIs.Handle("/api/admin/wecom/contact-description-backfills/", syncHandler.Routes())
	adminAPIs.Handle("/api/admin/channel-acquisition-entrant-receipts/", entrantAdminHandler.Routes())
	adminAPIs.Handle("/api/admin/customers", customerHandler.Routes())
	adminAPIs.Handle("/api/admin/customers/", customerHandler.Routes())
	adminAPIs.Handle("/api/v1/customer-tag-commands", customerHandler.TagCommandRoutes())
	adminAPIs.Handle("/api/v1/customer-tag-commands/", customerHandler.TagCommandRoutes())
	adminAPIs.Handle("/api/admin/customer-sync-runs", syncHandler.Routes())
	adminAPIs.Handle("/api/admin/customer-sync-runs/", syncHandler.Routes())
	adminAPIs.Handle("/api/admin/overview", adminOverviewHandler)
	adminAPIs.Handle("/api/admin/overview/paid-records", adminOverviewHandler)
	adminAPIs.Handle("/api/admin/hxc-dashboard/", hxcHandler.Routes())
	adminAPIs.Handle("/api/admin/orders", orderHandler)
	adminAPIs.Handle("/api/admin/orders/", orderHandler)
	adminAPIs.Handle("/api/admin/order-imports/", orderImportHandler)
	mountPaymentAdminAPIs(adminAPIs, orderHandler, paymentHandler)
	adminAPIs.Handle("/api/admin/exports", orderHandler)
	adminAPIs.Handle("/api/admin/exports/", orderHandler)
	adminAPIs.Handle("/api/admin/alipay/transactions", orderHandler)
	adminAPIs.Handle("/api/v1/wechat-pay/", paymentHandler)
	adminAPIs.Handle("/api/v1/alipay/", paymentHandler)
	adminAPIs.Handle("/api/h5/wechat-pay/oauth/", paymentHandler)
	adminAPIs.Handle("/api/public/wechat-pay/", paymentHandler)
	adminAPIs.Handle("/api/public/alipay/", paymentHandler)
	adminAPIs.Handle("/api/public/wechat-shop/", paymentHandler)
	adminAPIs.Handle("/api/public/service-period-member-grid/bootstrap", productBindings.Products)
	adminAPIs.Handle("/api/public/service-period-member-grid/query", productBindings.Products)
	adminAPIs.Handle("/api/public/service-period-member-grid/scoped-query", productBindings.Products)
	adminAPIs.Handle("/api/public/hxc-dashboard/query", hxcHandler.Routes())
	adminAPIs.Handle("/api/v1/products", productBindings.Products)
	adminAPIs.Handle("/api/v1/products/", productBindings.Products)
	adminAPIs.Handle("/api/admin/wechat-pay/products", productBindings.Products)
	adminAPIs.Handle("/api/admin/wechat-pay/products/", productBindings.Products)
	adminAPIs.Handle("/api/admin/service-period-products", productBindings.Products)
	adminAPIs.Handle("/api/admin/service-period-products/", productBindings.Products)
	adminAPIs.Handle("/api/admin/coupons", couponBindings.Coupons)
	adminAPIs.Handle("/api/admin/coupons/", couponBindings.Coupons)
	adminAPIs.Handle("/api/admin/config/", configBindings.Config)
	adminAPIs.Handle("/api/admin/setup-wizard", configBindings.Config)
	adminAPIs.Handle("/api/admin/automation-agents", automationBindings.Agents)
	adminAPIs.Handle("/api/admin/automation-agents/", automationBindings.Agents)
	adminAPIs.Handle("/api/admin/automations", automationBindings.Runtime)
	adminAPIs.Handle("/api/admin/automations/", automationBindings.Runtime)
	adminAPIs.Handle("/api/admin/automation-runs", automationBindings.Runtime)
	adminAPIs.Handle("/api/admin/automation-runs/", automationBindings.Runtime)
	adminAPIs.Handle("/api/admin/channels", channelCenter)
	adminAPIs.Handle("/api/admin/channels/", channelCenter)
	adminAPIs.Handle("/api/admin/wecom-customer-acquisition-links", channelLinkHandler)
	adminAPIs.Handle("/api/admin/wecom-customer-acquisition-links/", channelLinkHandler)
	adminAPIs.Handle("/api/admin/operation-batches", excelBridge)
	adminAPIs.Handle("/api/admin/operation-batches/", excelBridge)
	adminAPIs.Handle("/api/admin/ai-assistant/", aiHandler.Routes())
	adminAPIs.Handle("/api/admin/ai-assist/review-plans", aiHandler.Routes())
	adminAPIs.Handle("/api/sidebar/v2/", sidebarHandler.Routes())
	adminAPIs.Handle("/api/admin/common/operation-members", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("scope") {
		case "owner_migration":
			customerHandler.OwnerHandoffOperationMembersHandler().ServeHTTP(w, r)
		case "channel_code":
			channelOperationMemberPicker{directory: channelAcquisitionService, security: requestSecurity}.ServeHTTP(w, r)
		case "audience_senders":
			audienceOperationMemberPicker{directory: audienceDirectory, security: requestSecurity}.ServeHTTP(w, r)
		default:
			groupOpsBindings.GroupOps.ServeHTTP(w, r)
		}
	}))
	mountSurveyAPIs(adminAPIs, surveyBindings.Survey, customerHandler.TagCommandRoutes())
	adminAPIs.Handle("/api/admin/operation-cycles/", operationBindings.API)
	adminAPIs.Handle("/api/operation-cycles/", operationBindings.API)
	readiness := platformruntime.ReadinessFunc(func(readinessContext context.Context) error {
		if checkErr := pool.Check(readinessContext); checkErr != nil {
			return checkErr
		}
		checkErr := checkCurrentReleaseSchema(readinessContext, pool.Native(), cfg)
		if checkErr != nil {
			return checkErr
		}
		if checkErr = effectsModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = outbound.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = mediaModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = radarModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = aiModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if cfg.WeCom.ChannelProviderReadEnabled {
			if checkErr = wecom.CheckGroupMembershipReadiness(readinessContext, pool.Native()); checkErr != nil {
				return checkErr
			}
		}
		if checkErr = tagModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = channelModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = productModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = couponModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = configModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = automationModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = segmentModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = groupOpsModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if cfg.HXCDashboard.Enabled {
			if checkErr = hxcModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
				return checkErr
			}
		}
		if checkErr = surveyModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = operationModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if cfg.Ops.Enabled {
			if checkErr = opsInspections.Readiness(readinessContext); checkErr != nil {
				return checkErr
			}
			if checkErr = opsRetention.Readiness(readinessContext); checkErr != nil {
				return checkErr
			}
		}
		if checkErr = adminOpsProjectionStore.Readiness(readinessContext); checkErr != nil {
			return checkErr
		}
		return nil
	})
	healthHandler, err := platformruntime.NewHandler(platformruntime.HandlerOptions{ReleaseSHA: cfg.ReleaseSHA, Readiness: readiness})
	if err != nil {
		return fail(err)
	}

	effectsUI := effectsModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, tokens, labs, admin string) error {
		return renderer.RenderExternalEffects(writer, webshell.AdminPageForRequest(request, "外部效果与 Push Center", "仅展示本地外部效果状态与对账事实。", "api.admin_cloud_orchestrator_workspace"), webshell.ExternalEffectsAssets{TokensCSS: tokens, LabsCSS: labs, AdminJS: admin})
	})
	hxcUI := hxchttp.NewUIHandler("web/dist", func(writer http.ResponseWriter, request *http.Request, assets hxchttp.PageAssets) error {
		return renderer.RenderHXC(writer, webshell.AdminPageForRequest(request, "漏斗 / 数据看板", "HXC 当前全量投影；OneID 仅作为次级质量指标。", "api.admin_hxc_dashboard_workspace"), webshell.HXCAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	mediaUI := mediaModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets media.MediaAssets) error {
		endpoint := map[string]string{"images": "api.admin_image_library_workspace", "mpLib": "api.admin_miniprogram_library_workspace", "attach": "api.admin_attachment_library_workspace"}[page]
		title := map[string]string{"images": "图片素材库", "mpLib": "小程序素材库", "attach": "附件素材库"}[page]
		if request.URL.Path == "/admin/materials" {
			title = "素材库"
			endpoint = "api.admin_materials_workspace"
		}
		return renderer.RenderMedia(writer, webshell.AdminPageForRequest(request, title, "仅管理本地素材、私有 blob 与审计事实。", endpoint), page, donorTemplate, webshell.MediaAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, MaterialSaveHostJS: assets.MaterialSaveHostJS, ImageLibraryFilterHostJS: assets.ImageLibraryFilterHostJS, MaterialLibraryHostJS: assets.MaterialLibraryHostJS})
	})
	tagUI := tagModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, donorTemplate string, assets tag.TagsAssets) error {
		return renderer.RenderTags(writer, webshell.AdminPageForRequest(request, "企微标签管理", "管理标签目录与本地同步意图。", "api.admin_wecom_tags_page"), donorTemplate, webshell.TagsAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, PageHeaderActionHostJS: assets.PageHeaderActionHostJS})
	})
	memberGridUI := producthttp.NewMemberGridUI()
	productUI := productModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets productmodule.ProductAssets) error {
		titles := map[string]string{"products": "商品管理", "productForm": "创建普通商品", "spProducts": "周期商品管理", "spProductForm": "创建周期商品", "spProductData": "周期商品 · 会员数据"}
		if page == "productForm" && (request.URL.Query().Get("id") != "" || strings.HasSuffix(request.URL.Path, "/edit")) {
			titles[page] = "编辑普通商品"
		}
		if page == "spProductForm" && (request.URL.Query().Get("id") != "" || strings.HasSuffix(request.URL.Path, "/edit")) {
			titles[page] = "编辑周期商品"
		}
		// These are presentation-only active navigation identifiers. They use
		// the canonical V3 admin routes shared by the server shell and the
		// release-document navigation Host; Product remains the owner of its
		// page data and commands.
		endpoints := map[string]string{"products": "api.admin_wechat_pay_products_page", "productForm": "api.admin_wechat_pay_products_page", "spProducts": "api.admin_service_period_products_page", "spProductForm": "api.admin_service_period_products_page", "spProductData": "api.admin_service_period_products_page"}
		return renderer.RenderProducts(writer, webshell.AdminPageForRequest(request, titles[page], "管理本地商品、周期会员数据与受控配置。", endpoints[page]), page, donorTemplate, webshell.ProductAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, ProductCSS: assets.ProductCSS, HostJS: assets.HostJS, StandardHostJS: assets.StandardHostJS, StandardCSS: assets.StandardCSS})
	})
	orderUI := orderui.NewUIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets orderui.PageAssets) error {
		title := map[string]string{"orders": "交易管理", "orderDetail": "订单详情"}[page]
		return renderer.RenderOrders(writer, webshell.AdminPageForRequest(request, title, "历史订单默认只读；未验证身份不归属 OneID。", "api.admin_orders_page"), page, donorTemplate, webshell.OrderAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, HostJS: assets.HostJS})
	})
	couponUI := couponModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets coupon.Assets) error {
		titles := map[string]string{"coupons": "优惠券", "couponForm": "优惠券", "couponData": "优惠券 · 领取数据"}
		endpoints := map[string]string{"coupons": "api.admin_coupons_page", "couponForm": "api.admin_coupon_form_page", "couponData": "api.admin_coupon_claims"}
		return renderer.RenderCoupons(writer, webshell.AdminPageForRequest(request, titles[page], "管理本地优惠券规则、领取事实与核销快照。", endpoints[page]), page, donorTemplate, webshell.CouponAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, HostJS: assets.HostJS})
	})
	radarUI := radarModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page string, assets radarmodule.UIAssets) error {
		titles := map[string]string{"radar": "内容雷达", "radarDetail": "雷达详情", "radarForm": "雷达配置"}
		return renderer.RenderRadar(writer, webshell.AdminPageForRequest(request, titles[page], "UnionID 经 OneID 解析后形成可审计访问归因。", "api.admin_radar_links"), page, webshell.RadarAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, HostJS: assets.HostJS, StandardHostJS: assets.StandardHostJS, SelectionDialogCSS: assets.SelectionDialogCSS})
	})
	groupOpsUI := groupOpsModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets groupops.GroupOpsAssets) error {
		endpoint := "api.admin_group_ops_ui"
		if page == "groupopsDetail" {
			endpoint = "api.admin_group_ops_plan_detail"
		}
		return renderer.RenderGroupOps(writer, webshell.AdminPageForRequest(request, "群运营计划", "管理本地群计划、节点、素材快照与执行回执。", endpoint), page, donorTemplate, webshell.GroupOpsAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, ReadonlyCSS: assets.ReadonlyCSS, ReadonlyJS: assets.ReadonlyJS, StandardCSS: assets.StandardCSS, HostJS: assets.HostJS, SelectionDialogCSS: assets.SelectionDialogCSS, OperationPickerJS: assets.OperationPickerJS, GroupPickerCSS: assets.GroupPickerCSS, GroupPickerJS: assets.GroupPickerJS, MaterialPickerCSS: assets.MaterialPickerCSS, MaterialPickerJS: assets.MaterialPickerJS, ComposerCSS: assets.ComposerCSS, ComposerJS: assets.ComposerJS})
	})
	automationUI := automationModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets automation.AgentAssets, bootstrap automation.AgentPageBootstrap) error {
		return renderer.RenderAutomation(writer, webshell.AdminPageForRequest(request, "自动化话术", "管理本地 Agent 与固定话术配置。", "api.admin_automation_agents"), page, donorTemplate, webshell.AutomationAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, PresentationCSS: assets.PresentationCSS, ContentCSS: assets.ContentCSS, SelectionDialogCSS: assets.SelectionDialogCSS, MaterialPickerCSS: assets.MaterialPickerCSS, MaterialPickerJS: assets.MaterialPickerJS, ContentHostJS: assets.ContentHostJS}, bootstrap.CreateCode)
	})
	surveyUI := surveyModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets surveymodule.UIAssets) error {
		titles := map[string]string{"questionnaires": "问卷管理", "questionnaireDetail": "问卷编辑", "questionnaireOps": "问卷运营"}
		return renderer.RenderSurvey(writer, webshell.AdminPageForRequest(request, titles[page], "管理问卷定义、版本、答卷及只读外部效果回执。", "api.admin_questionnaires"), page, donorTemplate, webshell.SurveyAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, EditorJS: assets.EditorJS, EditorCSS: assets.EditorCSS, StandardHostJS: assets.StandardHostJS, SurveyHostJS: assets.SurveyHostJS, OperationsHostJS: assets.OperationsHostJS, OperationsCSS: assets.OperationsCSS, StandardCSS: assets.StandardCSS})
	})
	surveyPublicUI := surveyModule.PublicUIBinding("web/dist")
	operationUI := operationModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets operationcycle.UIAssets) error {
		return renderer.RenderOperationCycles(writer, webshell.AdminPageForRequest(request, "运营闭环", "运营周期、执行事实与复盘记录。", "api.admin_operation_cycles_page"), page, donorTemplate, webshell.OperationCycleAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, HostJS: assets.HostJS})
	})
	ownerHandoffUI := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !isOwnerHandoffUIRequest(request) {
			http.NotFound(writer, request)
			return
		}
		if renderErr := renderer.RenderOwnerHandoff(writer, webshell.AdminPageForRequest(request, "负责人迁移", "冻结预览后按本地或企微受理模式执行；企微最终接替单独回查。", "api.admin_owner_migration_page")); renderErr != nil {
			http.Error(writer, "owner handoff page unavailable", http.StatusInternalServerError)
		}
	})
	configUI := configModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets configmodule.UIAssets) error {
		if page == "runtimeConfigCenter" || page == "runtimeConfigCategory" || page == "runtimeReleaseList" || page == "runtimeReleaseNew" || page == "runtimeReleaseDetail" {
			title := "配置发布"
			if page == "runtimeConfigCenter" || page == "runtimeConfigCategory" {
				title = "配置中心"
			}
			// Runtime releases are a V3-owned Host rather than a frozen AdminOps
			// document. The Config module sends its small host template through
			// this renderer callback, so preserve that page class here.
			return renderer.RenderRuntimeConfig(writer, webshell.AdminPageForRequest(request, title, "保存草稿、校验、发布并按进程 revision 确认受控应用。", "api.admin_runtime_config_releases"), page, donorTemplate)
		}
		title := map[string]string{"config": "配置", "configDetail": "配置", "apidocs": "API 文档"}[page]
		endpoint := map[string]string{"config": "api.admin_config", "configDetail": "api.admin_config", "apidocs": "api.admin_api_docs"}[page]
		return renderer.RenderConfig(writer, webshell.AdminPageForRequest(request, title, "", endpoint), page, donorTemplate, webshell.ConfigAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	channelUI := channelModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, resourceID, donorTemplate string, assets channelstore.UIAssets) error {
		title := map[string]string{"channels": "渠道码中心", "channelForm": "渠道配置"}[page]
		endpoint := map[string]string{"channels": "api.admin_channels_page", "channelForm": "api.admin_channel_new_page"}[page]
		return renderer.RenderChannels(writer, webshell.AdminPageForRequest(request, title, "管理渠道定义、客服分配、资产状态与安全历史归因。", endpoint), page, resourceID, donorTemplate, webshell.ChannelAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, StandardHostJS: assets.StandardHostJS, StandardCSS: assets.StandardCSS})
	})
	aiUI := aiModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets aiassistant.Assets) error {
		return renderer.RenderAIAssistant(writer, webshell.AdminPageForRequest(request, "AI 助手", "AI 计划审阅与可对账执行结果。", "api.admin_ai_assistant"), page, donorTemplate, webshell.AIAssistantAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, GroupCSS: assets.GroupCSS, MaterialCSS: assets.MaterialCSS, ComposerCSS: assets.ComposerCSS, ReadonlyCSS: assets.ReadonlyCSS, HostJS: assets.HostJS, PageHeaderActionHostJS: assets.PageHeaderActionHostJS})
	})
	groupOpsRoute := audienceOperationMemberSubtree{
		audience: audienceOperationMemberPicker{directory: audienceDirectory, security: requestSecurity},
		groupOps: groupOpsBindings.GroupOps,
	}
	handler, err := routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(healthHandler, accessHandler.Routes(), adminAPIs, effectsBindings.Effects, effectsBindings.PushCenter, effectsUI, mediaBindings.Media, mediaUI, tagBindings.Tags, tagUI, productBindings.Products, productUI, couponBindings.Coupons, couponUI, channelCenter, groupOpsRoute, groupOpsUI, automationBindings.Agents, automationUI, operationUI, configBindings.Config, configUI, weComHandler, shellHandler, authentication, cfg.PublicOrigin, h5PublicOrigin(cfg))
	if err != nil {
		return fail(err)
	}
	handler = mountOpsGovernance(handler, opsInspectionHTTP, opsRetentionHTTP, opsCPUProfileHTTP, opsGovernanceOutcomesHTTP)
	handler = mountInvitations(handler, invitationHandler)
	handler = openplatformhttp.Mount(handler, openPlatformHandler.Routes())
	handler = mountOpenPlatformUI(handler, shellHandler, authentication)
	handler = mountMemberGridUI(handler, memberGridUI)
	handler = mountOwnerHandoffUI(handler, requireAdminSession(authentication, ownerHandoffUI))
	handler, err = mountSegmentAPI(handler, segmentBindings.Audience)
	if err != nil {
		return fail(err)
	}
	handler, err = mountAutomationRuntimeAPI(handler, automationBindings.Runtime)
	if err != nil {
		return fail(err)
	}
	handler, err = mountSegmentWebhook(handler, segmentWebhookHandler)
	if err != nil {
		return fail(err)
	}
	handler, err = mountAudienceDirectPush(handler, audienceDirectPushHandler)
	if err != nil {
		return fail(err)
	}
	handler, err = mountAudienceDirectPushAdmin(handler, audienceDirectPushAdmin)
	if err != nil {
		return fail(err)
	}
	aiReviewAPIs := http.NewServeMux()
	aiReviewAPIs.Handle("/api/admin/operation-batches", excelBridge)
	aiReviewAPIs.Handle("/api/admin/operation-batches/", excelBridge)
	aiReviewAPIs.Handle("/", aiHandler.Routes())
	handler = mountAIAssistant(handler, aiReviewAPIs, aiUI, authentication, cfg.AIAssistant.UIEnabled, cfg.PublicOrigin)
	handler = securityHeaders(mountPublicCoupon(mountPublicServicePeriod(mountPublicProduct(mountRadar(mountChannelUI(mountHXCUI(mountOrderUI(mountSurveyUI(handler, surveyUI, surveyPublicUI, authentication), orderUI, authentication), hxcUI, authentication), channelUI, authentication), radarBindings.Radar, radarUI, authentication), publicProductHandler), publicServicePeriodHandler), couponPublicHandler))
	handler = mountDistribution(handler, distributionPublic, distributionAdmin)
	// Referral is independently authenticated by a trusted WeChat browser
	// session, never a distributor registration or employee Access cookie. The
	// concrete handlers replace these fail-closed defaults when all Referral
	// dependencies have been composed.
	handler = mountSecuredReferral(handler, referralPublic, referralAdmin, cfg.PublicOrigin, h5PublicOrigin(cfg))
	handler = redirectH5EntryOrigin(handler, cfg.PublicOrigin, h5PublicOrigin(cfg))
	handler, err = mountMessageArchive(handler, archiveHandler.Routes())
	if err != nil {
		return fail(err)
	}
	// These are local observations only: they make the release and diagnostics
	// projections truthful and readable after startup, without claiming deploy,
	// cutover, provider execution, or runtime-secret application.
	if err = releaseObservation.Record(ctx, releaseport.ReleaseObservation{ReleaseSHA: cfg.ReleaseSHA, Status: "observed"}); err != nil {
		return fail(err)
	}
	if _, err = diagnostics.Record(ctx, adminopsport.DiagnosticSnapshot{Key: "aicrm.composition", Status: "ok"}); err != nil {
		return fail(err)
	}
	for key, enabled := range map[string]bool{
		"channel.provider.read":       cfg.WeCom.ChannelProviderReadEnabled,
		"channel.provider.qr":         cfg.WeCom.ChannelQRProviderEnabled,
		"channel.provider.media_prep": cfg.WeCom.ChannelMediaPrepProviderEnabled,
		"channel.provider.welcome":    cfg.WeCom.ChannelWelcomeProviderEnabled,
		"channel.provider.tag":        cfg.WeCom.ChannelTagProviderEnabled,
		"channel.staff_refresh":       cfg.WeCom.ChannelProviderReadEnabled && staffDirectoryRefresh.Ready(),
	} {
		status := "warning"
		if enabled {
			status = "ok"
		}
		if _, err = diagnostics.Record(ctx, adminopsport.DiagnosticSnapshot{Key: key, Status: status}); err != nil {
			return fail(err)
		}
	}
	if cfg.Role == platformconfig.RoleEffectsWorker {
		if _, err = diagnostics.Record(ctx, adminopsport.DiagnosticSnapshot{Key: "channel.effects_worker", Status: "ok"}); err != nil {
			return fail(err)
		}
	}
	// A one-shot payment repair is not a long-lived runtime application and is
	// intentionally outside the immutable runtime-application role catalog.
	// It still performs the full Composition below and its service persists the
	// normal Payment/Order/audit/outbox facts; it simply must not claim a daemon
	// was started or widen the catalog/migration for an emergency operation.
	if cfg.Role != platformconfig.RolePaymentReconcile {
		// Record only after every configuration-dependent Adapter and route has
		// been constructed successfully. A failed startup therefore leaves no
		// application fact that could be mistaken for a running process.
		if err = runtimeReleaseService.RecordRuntimeApplication(ctx, configport.RuntimeApplication{Revision: runtimeSnapshot.Revision, Source: runtimeSnapshot.Source, Role: string(cfg.Role), ReleaseSHA: cfg.ReleaseSHA, SnapshotChecksum: runtimeSnapshot.Checksum, AppliedAt: time.Now().UTC()}); err != nil {
			return fail(err)
		}
	}
	if cfg.Ops.Enabled {
		handler = platformdiagnostics.Middleware(handler, cfg.ReleaseSHA, opsRecord)
	}
	return &composedApplication{invitationService: invitationService, catalogService: catalogService, pool: pool, handler: handler, authentication: authentication, management: management, weComProcessor: weComProcessor, weComArchiveProcessor: weComArchiveProcessor, effectsRuntime: effectsRuntime, paymentDistribution: paymentService, paymentReconciliation: paymentService, paymentSession: paymentSession, channelEntrantActions: channelEntrantActions, customerSync: customerSync, hxcDashboard: hxcDashboard, hxcSource: hxcSource, adminOps: adminOpsProjection, release: releaseObservation, diagnostics: diagnostics}, nil
}

func mountMessageArchive(next, archive http.Handler) (http.Handler, error) {
	if next == nil || archive == nil {
		return nil, errors.New("message archive HTTP routes are required")
	}
	mux := http.NewServeMux()
	mux.Handle("/api/admin/message-archive/", archive)
	mux.Handle("/", next)
	return mux, nil
}

func mountSurveyAPIs(mux *http.ServeMux, survey http.Handler, tagHandlers ...http.Handler) {
	var customerTags http.Handler
	if len(tagHandlers) > 0 {
		customerTags = tagHandlers[0]
	}
	mux.Handle("/api/admin/questionnaires", survey)
	mux.Handle("/api/admin/questionnaires/", survey)
	mux.Handle("/api/admin/survey-history/", survey)
	mux.Handle("/api/public/questionnaires/", survey)
	mux.Handle("/api/public/survey-submission-results/query", survey)
	mux.Handle("/api/h5/surveys/oauth/", survey)
	mux.Handle("/api/h5/surveys/session", survey)
	mux.Handle("/q/", survey)
	mux.Handle("/api/v1/customers/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if customerTags != nil && ((r.Method == http.MethodPut || r.Method == http.MethodDelete) && strings.Contains(r.URL.Path, "/tags/") || r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tag-commands")) {
			customerTags.ServeHTTP(w, r)
			return
		}
		if customerTags != nil && r.Method == http.MethodPost && (r.URL.Path == "/api/v1/customer-tag-commands" || r.URL.Path == "/api/v1/customer-tag-commands/preview") {
			customerTags.ServeHTTP(w, r)
			return
		}
		survey.ServeHTTP(w, r)
	}))
	// The frozen operations workspace reads its history projection from this
	// legacy page-shaped path. Keep it inside the authenticated admin mux so the
	// response is JSON from Survey instead of the outer mux's plain-text 404.
	mux.Handle("/admin/questionnaires/", survey)
}

// mountOpenPlatformUI replaces only the retired API-docs presentation with the
// V3-owned caller-management Host. The Access-owned Open Platform APIs stay
// mounted by their own module; this shell adapter neither grants permissions
// nor stores credentials. Keeping the outer route here prevents Config's
// frozen document binding from claiming the page before the Host is loaded.
func mountOpenPlatformUI(next, ui http.Handler, authentication accessAuthentication) http.Handler {
	if next == nil || ui == nil || authentication == nil {
		return http.NotFoundHandler()
	}
	protected := requireAdminSession(authentication, ui)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/admin/api-docs", "/admin/apidocs.html":
			protected.ServeHTTP(writer, request)
			return
		default:
			next.ServeHTTP(writer, request)
		}
	})
}

func mountHXCUI(next, dashboardUI http.Handler, authentication accessAuthentication) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/shared/data-dashboard" || strings.HasPrefix(request.URL.Path, "/dashboard-public-assets/") {
			dashboardUI.ServeHTTP(writer, request)
			return
		}
		if request.URL.Path == "/admin/hxc-dashboard" || strings.HasPrefix(request.URL.Path, "/hxc-dashboard-assets/") {
			requireAdminSession(authentication, dashboardUI).ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func mountAIAssistant(next, api, ui http.Handler, authentication accessAuthentication, uiEnabled bool, publicOrigin string) http.Handler {
	api = rejectCrossSiteUnsafeRequests(api, canonicalOrigin(publicOrigin))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/operation-batches" || strings.HasPrefix(r.URL.Path, "/api/admin/operation-batches/") || strings.HasPrefix(r.URL.Path, "/api/admin/ai-assistant/") || r.URL.Path == "/api/admin/ai-assist/review-plans" || r.URL.Path == "/api/integrations/ai-assistant/review-plans" {
			api.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/ai-assistant-assets/") || r.URL.Path == "/admin/ai.html" || r.URL.Path == "/admin/aiDetail.html" || r.URL.Path == "/admin/cloud-orchestrator/plans" || strings.HasPrefix(r.URL.Path, "/admin/cloud-orchestrator/plans/") {
			if !uiEnabled {
				http.NotFound(w, r)
				return
			}
			requireAdminSession(authentication, ui).ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mountOrderUI(next, adminUI http.Handler, authentication accessAuthentication) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/orders", "/admin/orders.html", "/admin/orderDetail.html":
			requireAdminSession(authentication, adminUI).ServeHTTP(w, r)
		default:
			if strings.HasPrefix(r.URL.Path, "/order-assets/") {
				requireAdminSession(authentication, adminUI).ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		}
	})
}

func mountPublicProduct(next, products http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/product-public-assets/") || strings.HasPrefix(r.URL.Path, "/api/public/products/") || strings.HasPrefix(r.URL.Path, "/api/h5/product-images/") || strings.HasPrefix(r.URL.Path, "/p/") || strings.HasPrefix(r.URL.Path, "/pay/") {
			products.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mountPublicServicePeriod(next, products http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/s/") || strings.HasPrefix(r.URL.Path, "/api/h5/service-period-products/") {
			products.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mountPublicCoupon(next, coupons http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/c/") || r.URL.Path == "/api/h5/coupons/available" || strings.HasPrefix(r.URL.Path, "/api/h5/coupons/") {
			coupons.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mountSurveyUI(next, adminUI, publicUI http.Handler, authentication accessAuthentication) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/admin/questionnaires" || r.URL.Path == "/admin/questionnaires.html" || r.URL.Path == "/admin/questionnaireDetail.html" || r.URL.Path == "/admin/questionnaireOps.html":
			requireAdminSession(authentication, adminUI).ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/h5/") || strings.HasPrefix(r.URL.Path, "/survey-assets/"):
			publicUI.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func mountChannelUI(next, adminUI http.Handler, authentication accessAuthentication) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		isCanonicalEdit := strings.HasPrefix(path, "/admin/channels/") && strings.HasSuffix(path, "/edit")
		switch {
		case path == "/admin/channels", path == "/admin/channels.html", path == "/admin/channels/new", path == "/admin/channelForm.html", isCanonicalEdit:
			requireAdminSession(authentication, adminUI).ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func (application *composedApplication) Close() {
	if application != nil && application.hxcSource != nil {
		_ = application.hxcSource.Close()
	}
	if application != nil && application.pool != nil {
		application.pool.Close()
	}
}

func (application *composedApplication) bootstrap(ctx context.Context, config platformconfig.Bootstrap) error {
	if !config.Enabled {
		return nil
	}
	_, _, err := application.management.Bootstrap(ctx, accessapp.BootstrapInput{
		Username: config.Username, Password: config.Password, DisplayName: config.DisplayName,
	})
	return err
}

func allowedOAuthRedirects() map[string]struct{} {
	paths := map[string]struct{}{webshell.SidebarPagePath: {}}
	for _, route := range webshell.ADMIN_ROUTE_REGISTRY {
		if strings.HasPrefix(route.Path, webshell.AdminRootPath) {
			paths[route.Path] = struct{}{}
		}
	}
	return paths
}

func routeApplication(health, access, identity, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithEffects(health, access, identity, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithEffects(health, access, identity, effects, pushCenter, effectsUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithMedia(health, access, identity, effects, pushCenter, effectsUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithMedia(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithMediaTags(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func mountOwnerHandoffUI(next, ui http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isOwnerHandoffUIRequest(request) {
			ui.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// mountPaymentAdminAPIs keeps every Payment-owned admin prefix on the composed
// admin mux. The outer application router intentionally mounts the same exact
// prefixes to this mux; neither layer has a generic Payment fallback.
func mountPaymentAdminAPIs(mux *http.ServeMux, orderHandler, paymentHandler http.Handler) {
	mux.Handle("/api/admin/refunds", paymentHandler)
	mux.Handle("/api/admin/refunds/recovery", paymentHandler)
	mux.Handle("/api/admin/wechat-pay/orders", orderHandler)
	mux.Handle("/api/admin/wechat-pay/orders/", paymentHandler)
	mux.Handle("/api/admin/wechat-pay/payments/", paymentHandler)
	mux.Handle("/api/admin/wechat-pay/profit-sharing/receivers/", paymentHandler)
	mux.Handle("/api/admin/wechat-pay/refunds/", paymentHandler)
	mux.Handle("/api/admin/wechat-shop/refunds/", paymentHandler)
	mux.Handle("/api/admin/wechat-pay/order-exports", orderHandler)
	mux.Handle("/api/admin/payments/", paymentHandler)
}

// isOwnerHandoffUIPath identifies the canonical owner-handoff Host route and
// the frozen new-shell navigation's ownerMig.html alias.
func isOwnerHandoffUIPath(path string) bool {
	return path == "/admin/owner-migration" || path == "/admin/ownerMig.html"
}

// isOwnerHandoffUIRequest reserves the menu alias for the V3 Host while
// retaining the existing V1 contact-history read-only entry on ownerMig.html.
// That history entry never mounts the mutation-capable Host.
func isOwnerHandoffUIRequest(request *http.Request) bool {
	return isOwnerHandoffUIPath(request.URL.Path) && request.URL.Query().Get("contact_history") != "1"
}

func mountMemberGridUI(next, ui http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if path == "/shared/service-period-member-grid" || strings.HasPrefix(path, "/service-period-member-grid-assets/") || strings.HasPrefix(path, "/static/service-period/icons/") {
			ui.ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func routeApplicationWithMediaTags(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProducts(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProducts(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, channelHandler, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCoupons(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, http.NotFoundHandler(), http.NotFoundHandler(), channelHandler, weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCoupons(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOps(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

// routeApplicationWithGroupOps keeps the pre-Product/Coupon composition helper
// available to tests while the real Composition Root mounts every owned module
// through the combined route below.
func routeApplicationWithGroupOps(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, groupOpsHandler, groupOpsUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOps(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), groupOpsHandler, groupOpsUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCouponsGroupOps(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, automationHandler, automationUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, automationHandler, automationUI, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithAll(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, operationUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), operationUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCouponsAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, operationUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), operationUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCouponsGroupOpsAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, operationUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, http.NotFoundHandler(), http.NotFoundHandler(), operationUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, automationHandler, automationUI, operationUI, configHandler, configUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string, h5Origins ...string) (http.Handler, error) {
	if health == nil || access == nil || identity == nil || effects == nil || pushCenter == nil || effectsUI == nil || mediaHandler == nil || mediaUI == nil || tagHandler == nil || tagUI == nil || productHandler == nil || productUI == nil || couponHandler == nil || couponUI == nil || channelHandler == nil || groupOpsHandler == nil || groupOpsUI == nil || automationHandler == nil || automationUI == nil || operationUI == nil || configHandler == nil || configUI == nil || weCom == nil || shell == nil || authentication == nil || canonicalOrigin(publicOrigin) == "" {
		return nil, errors.New("application HTTP dependencies are required")
	}
	mux := http.NewServeMux()
	mux.Handle("/healthz", health)
	mux.Handle("/readyz", health)
	mux.Handle("/login", access)
	mux.Handle("/logout", access)
	mux.Handle("/api/admin/access/", access)
	// Frozen PR09 AdminOps requests this exact compatibility URL. Access remains
	// the route owner; Config never receives or reimplements admin credentials.
	mux.Handle("/api/admin/admin-access", access)
	mux.Handle("/api/admin/oneid/", identity)
	mux.Handle("/api/admin/wecom/", identity)
	mux.Handle("/api/admin/channel-acquisition-entrant-receipts/", identity)
	mux.Handle("/api/admin/customers", identity)
	mux.Handle("/api/admin/customers/", identity)
	mux.Handle("/api/admin/customer-sync-runs", identity)
	mux.Handle("/api/admin/customer-sync-runs/", identity)
	mux.Handle("/api/admin/overview", identity)
	mux.Handle("/api/admin/overview/paid-records", identity)
	mux.Handle("/api/admin/hxc-dashboard/", identity)
	mux.Handle("/api/admin/questionnaires", identity)
	mux.Handle("/api/admin/questionnaires/", identity)
	mux.Handle("/api/admin/survey-history/", identity)
	mux.Handle("/api/public/questionnaires/", identity)
	mux.Handle("/api/public/survey-submission-results/query", identity)
	mux.Handle("/api/h5/surveys/oauth/", identity)
	mux.Handle("/api/h5/surveys/session", identity)
	mux.Handle("/q/", identity)
	// The active v3 Sidebar API has one owner. Mount the complete versioned
	// subtree before the broader WeCom OAuth/JSSDK prefix below; the identity
	// mux delegates it to internal/sidebar while WeCom retains only session,
	// context-token and JSSDK protocol routes.
	mux.Handle("/api/sidebar/v2/", identity)
	mux.Handle("/api/v1/customers/", identity)
	// Customer owns the batch tag command routes. Keep the exact batch prefix
	// alongside the historical per-customer subtree so the rendered Host and
	// its durable refresh readback reach the same Customer handler.
	mux.Handle("/api/v1/customer-tag-commands", identity)
	mux.Handle("/api/v1/customer-tag-commands/", identity)
	mux.Handle("/admin/questionnaires/", identity)
	mux.Handle("/api/admin/orders", identity)
	mux.Handle("/api/admin/orders/", identity)
	mux.Handle("/api/admin/order-imports/", identity)
	mux.Handle("/api/admin/refunds", identity)
	mux.Handle("/api/admin/refunds/recovery", identity)
	mux.Handle("/api/admin/exports", identity)
	mux.Handle("/api/admin/exports/", identity)
	mux.Handle("/api/admin/alipay/transactions", identity)
	mux.Handle("/api/admin/wechat-pay/orders", identity)
	mux.Handle("/api/admin/payments/", identity)
	mux.Handle("/api/v1/wechat-pay/", identity)
	mux.Handle("/api/v1/alipay/", identity)
	mux.Handle("/api/h5/wechat-pay/oauth/", identity)
	mux.Handle("/api/public/wechat-pay/", identity)
	mux.Handle("/api/public/alipay/", identity)
	mux.Handle("/api/public/wechat-shop/", identity)
	mux.Handle("/api/admin/wechat-pay/orders/", identity)
	mux.Handle("/api/admin/wechat-pay/payments/", identity)
	mux.Handle("/api/admin/wechat-pay/profit-sharing/receivers/", identity)
	mux.Handle("/api/admin/wechat-pay/refunds/", identity)
	mux.Handle("/api/admin/wechat-shop/refunds/", identity)
	mux.Handle("/api/admin/wechat-pay/order-exports", identity)
	mux.Handle("/api/admin/operation-cycles/", identity)
	mux.Handle("/api/operation-cycles/", identity)
	mux.Handle("/api/admin/external-effects", effects)
	mux.Handle("/api/admin/external-effects/", effects)
	mux.Handle("/api/admin/push-center/", pushCenter)
	mux.Handle("/api/admin/media-preparations", mediaHandler)
	mux.Handle("/api/admin/media-preparations/", mediaHandler)
	mux.Handle("/api/admin/image-library", mediaHandler)
	mux.Handle("/api/admin/image-library/", mediaHandler)
	mux.Handle("/api/admin/attachment-library", mediaHandler)
	mux.Handle("/api/admin/attachment-library/", mediaHandler)
	mux.Handle("/api/admin/miniprogram-library", mediaHandler)
	mux.Handle("/api/admin/miniprogram-library/", mediaHandler)
	mux.Handle("/api/admin/group-invite-library", mediaHandler)
	mux.Handle("/api/admin/group-invite-library/", mediaHandler)
	mux.Handle("/api/admin/wecom/tags", tagHandler)
	mux.Handle("/api/admin/wecom/tags/", tagHandler)
	mux.Handle("/api/admin/wecom/tag-groups", tagHandler)
	mux.Handle("/api/admin/wecom/tag-groups/", tagHandler)
	// Product API paths are registered before the generic admin compatibility
	// handler. Product owns only local definitions/lifecycle/configuration;
	// member-grid and provider paths remain absent/fail-closed.
	mux.Handle("/api/v1/products", productHandler)
	mux.Handle("/api/v1/products/", productHandler)
	mux.Handle("/api/admin/wechat-pay/products", productHandler)
	mux.Handle("/api/admin/wechat-pay/products/", productHandler)
	mux.Handle("/api/admin/service-period-products", productHandler)
	mux.Handle("/api/admin/service-period-products/", productHandler)
	mux.Handle("/api/public/service-period-member-grid/bootstrap", productHandler)
	mux.Handle("/api/public/service-period-member-grid/query", productHandler)
	mux.Handle("/api/public/service-period-member-grid/scoped-query", productHandler)
	mux.Handle("/api/public/hxc-dashboard/query", identity)
	mux.Handle("/api/admin/coupons", couponHandler)
	mux.Handle("/api/admin/coupons/", couponHandler)
	mux.Handle("/api/admin/config/", configHandler)
	mux.Handle("/api/admin/setup-wizard", configHandler)
	mux.Handle("/api/admin/channels", channelHandler)
	mux.Handle("/api/admin/channels/", channelHandler)
	mux.Handle("/api/admin/wecom-customer-acquisition-links", identity)
	mux.Handle("/api/admin/wecom-customer-acquisition-links/", identity)
	mux.Handle("/api/admin/automation-conversion/group-ops/", groupOpsHandler)
	mux.Handle("/api/admin/automation-agents", automationHandler)
	mux.Handle("/api/admin/automation-agents/", automationHandler)
	// The exact shared-picker URL has one scope decision in adminAPIs: owner
	// migration reaches Customer's Access projection while Group Ops retains its
	// existing scope. Keep the sub-tree on Group Ops for its owned /sync route.
	mux.Handle("/api/admin/common/operation-members", identity)
	mux.Handle("/api/admin/common/operation-members/", groupOpsHandler)
	mux.Handle("/api/automation/group-ops/", groupOpsHandler)
	adminRuntimeAssets := webshell.NewRuntimeAssetsHandler("web/dist", effectsUI)
	mux.Handle("/assets/", distributionRuntimeAssets(requireAdminSession(authentication, adminRuntimeAssets), "web/dist"))
	mux.Handle("/media-assets/", requireAdminSession(authentication, mediaUI))
	mux.Handle("/product-assets/", requireAdminSession(authentication, productUI))
	mux.Handle("/coupon-assets/", requireAdminSession(authentication, couponUI))
	mux.Handle("/groupops-assets/", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/automation-assets/", requireAdminSession(authentication, automationUI))
	mux.Handle("/config-assets/", requireAdminSession(authentication, configUI))
	mux.Handle("/admin/wecom-tags", requireAdminSession(authentication, tagUI))
	mux.Handle("/admin/operation-cycles", requireAdminSession(authentication, operationUI))
	mux.Handle("/admin/config/releases/", requireAdminSession(authentication, configUI))
	mux.Handle("/admin/operation-cycles/", requireAdminSession(authentication, operationUI))
	// The new login page is a V3-owned webshell document. Keep each mounted
	// module Host above: the new shell must not replace approved product, tag,
	// operation-cycle or configuration workflows with a generic document.
	mux.Handle(webshell.LoginAccessPath, requireAdminSession(authentication, shell))
	// The Config catalog used this path before Access gained its V3-owned page.
	// Preserve bookmarked links, but always render through the canonical shell.
	mux.Handle("/admin/admin-access", requireAdminSession(authentication, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		target := webshell.LoginAccessPath
		if request.URL.RawQuery != "" {
			target += "?" + request.URL.RawQuery
		}
		http.Redirect(writer, request, target, http.StatusSeeOther)
	})))
	// OneID remains a backend Port/API foundation; its former admin screen is deliberately unavailable.
	mux.Handle("/admin/oneid", http.NotFoundHandler())
	mux.Handle("/admin/oneid.html", http.NotFoundHandler())
	// The staged Tags donor document is a private template carrier. Only the
	// canonical PR10-mounted route above is public; neither its private staging
	// name nor the donor document name may fall through to a generic 200 shell.
	mux.Handle("/admin/tags.html", http.NotFoundHandler())
	mux.Handle("/admin/wecom-tags.html", http.NotFoundHandler())
	mux.Handle("/admin/cycles.html", http.NotFoundHandler())
	mux.Handle("/admin/cyclesDetail.html", http.NotFoundHandler())
	mux.Handle("/admin/external-effects", requireAdminSession(authentication, effectsUI))
	mux.Handle("/admin/campaigns.html", requireAdminSession(authentication, effectsUI))
	for _, path := range []string{
		"/admin/image-library", "/admin/miniprogram-library", "/admin/attachment-library",
		"/admin/images.html", "/admin/mpLib.html", "/admin/attach.html", "/admin/materials",
	} {
		mux.Handle(path, requireAdminSession(authentication, mediaUI))
	}
	// Product aliases mount the existing V3 Host. This retains the frozen
	// workspace fields and all currently-approved Product actions beneath the
	// new shell rather than letting a generic dist document mask them.
	for _, path := range []string{
		"/admin/wechat-pay/products", "/admin/wechat-pay/products/",
		"/admin/wechat-pay/products.html", "/admin/products.html",
		"/admin/wechat-pay/productForm.html", "/admin/productForm.html",
		"/admin/wechat-pay/spProducts.html", "/admin/spProducts.html",
		"/admin/wechat-pay/spProductForm.html", "/admin/spProductForm.html",
		"/admin/service-period-products", "/admin/service-period-products/",
		"/admin/wechat-pay/products/new", "/admin/service-period-products/new",
	} {
		mux.Handle(path, requireAdminSession(authentication, productUI))
	}
	for _, path := range []string{
		"/admin/spProductData.html", "/admin/wechat-pay/spProductData.html",
		"/admin/wechat-pay/products/spProductData.html", "/admin/service-period-products/spProductData.html",
	} {
		mux.Handle(path, requireAdminSession(authentication, productUI))
	}
	mux.Handle("/admin/automation-conversion/group-ops/ui", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/admin/automation-conversion/group-ops/groups/ui", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/admin/automation-conversion/group-ops/plans/", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/admin/groupops.html", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/admin/groupopsDetail.html", requireAdminSession(authentication, groupOpsUI))
	for _, path := range []string{"/admin/coupons", "/admin/coupons/", "/admin/coupons.html", "/admin/couponForm.html", "/admin/couponData.html"} {
		mux.Handle(path, requireAdminSession(authentication, couponUI))
	}
	for _, path := range []string{"/admin/automation-agents", "/admin/automation-agents/", "/admin/agents.html", "/admin/agentEdit.html"} {
		mux.Handle(path, requireAdminSession(authentication, automationUI))
	}
	for _, path := range []string{"/admin/config", "/admin/config/", "/admin/config.html", "/admin/configDetail.html", "/admin/api-docs", "/admin/apidocs.html", "/admin/config/releases", "/admin/config/releases/new"} {
		mux.Handle(path, requireAdminSession(authentication, configUI))
	}
	mux.Handle("/wecom/external-contact/callback", weCom)
	mux.Handle("/api/wecom/events", weCom)
	mux.Handle("/auth/wecom/start", weCom)
	mux.Handle("/auth/wecom/callback", weCom)
	mux.Handle("/api/sidebar/", weCom)
	mux.Handle("/static/", shell)
	mux.Handle("/sidebar-assets/", shell)
	mux.Handle(webshell.SidebarPagePath, shell)
	// The distributor center has its own Payment-derived browser session. It is
	// a public shell document; its Distribution API independently authorizes
	// every read and mutation and must never require an employee Access cookie.
	mux.Handle("/referral", shell)
	mux.Handle("/referral/", shell)
	mux.Handle("/distribution", shell)
	mux.Handle("/distribution/", shell)
	mux.Handle("/admin", requireAdminSession(authentication, shell))
	mux.Handle("/admin/", requireAdminSession(authentication, shell))
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		http.Redirect(writer, request, "/admin", http.StatusSeeOther)
	})
	return securityHeaders(rejectCrossSiteUnsafeRequests(mux, canonicalOrigin(publicOrigin), h5Origins...)), nil
}

func rejectCrossSiteUnsafeRequests(next http.Handler, publicOrigin string, h5Origins ...string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isUnsafeMethod(request.Method) && !usesIndependentLoginCSRF(request) {
			origin := request.Header.Get("Origin")
			blocked := false
			if origin != "" {
				// Origin is the authoritative browser signal. Fetch Metadata is
				// only a fallback because extensions and restored tabs can report
				// an inconsistent Sec-Fetch-Site for an otherwise same-origin form.
				expectedOrigin := publicOrigin
				if len(h5Origins) == 1 && isH5BrowserMutation(request) {
					expectedOrigin = canonicalOrigin(h5Origins[0])
				}
				actualOrigin := canonicalOrigin(origin)
				blocked = expectedOrigin == "" || actualOrigin != expectedOrigin
				// The customer-facing Referral API is reachable after either the
				// canonical public entry or the configured H5 OAuth return. Its own
				// handler still applies same-origin and CSRF checks; this outer host
				// boundary must not reject either configured origin.
				if blocked && isReferralPublicMutation(request) && len(h5Origins) == 1 && actualOrigin == canonicalOrigin(h5Origins[0]) {
					blocked = false
				}
			} else {
				blocked = strings.EqualFold(request.Header.Get("Sec-Fetch-Site"), "cross-site")
			}
			if blocked {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusForbidden)
				_, _ = writer.Write([]byte(`{"ok":false,"error":"cross_site_request"}`))
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

// Move public entry pages before OAuth starts; callbacks and mutations must
// remain on the origin where they arrived. The target is configuration-owned.
func redirectH5EntryOrigin(next http.Handler, publicOrigin, h5Origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publicURL, publicErr := url.Parse(publicOrigin)
		h5URL, h5Err := url.Parse(h5Origin)
		if publicErr == nil && h5Err == nil && canonicalOrigin(publicOrigin) != "" && canonicalOrigin(h5Origin) != "" && publicOrigin != h5Origin && strings.EqualFold(r.Host, publicURL.Host) && r.Method == http.MethodGet && isH5EntryPage(r.URL.Path) {
			target := *r.URL
			target.Scheme, target.Host, target.User = h5URL.Scheme, h5URL.Host, nil
			http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isH5EntryPage(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if (len(parts) != 2 && len(parts) != 3) || parts[1] == "" || parts[1] == "." || parts[1] == ".." || strings.Contains(parts[1], "\\") {
		return false
	}
	if len(parts) == 3 {
		return parts[0] == "s" && parts[2] == "pay"
	}
	switch parts[0] {
	case "h5":
		return strings.HasSuffix(parts[1], ".html")
	case "q", "p", "pay", "s", "c":
		return true
	default:
		return false
	}
}

// Only these customer-facing mutations use the configured H5 origin. Admin
// routes and Provider callbacks retain the original application boundary.
func isH5BrowserMutation(request *http.Request) bool {
	if request.Method != http.MethodPost {
		return false
	}
	// The independent H5 origin may create an Alipay checkout, but only at
	// this exact mutation endpoint. Do not grant the H5 origin to neighboring
	// Alipay APIs or to a slash-suffixed path.
	if request.URL.Path == "/api/v1/alipay/checkouts" {
		return true
	}
	path := strings.TrimSuffix(request.URL.Path, "/")
	if path == "/api/public/survey-submission-results/query" || path == "/api/v1/wechat-pay/checkouts" {
		return true
	}
	const prefix = "/api/public/questionnaires/"
	if strings.HasPrefix(path, prefix) {
		parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
		return len(parts) == 2 && parts[0] != "" && parts[1] == "submissions"
	}
	return false
}

func isReferralPublicMutation(request *http.Request) bool {
	return request.Method == http.MethodPost && strings.HasPrefix(strings.TrimSuffix(request.URL.Path, "/"), "/api/v1/referral/")
}

func usesIndependentLoginCSRF(request *http.Request) bool {
	return request.Method == http.MethodPost && request.URL.Path == "/login"
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func canonicalOrigin(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	if parsed.Scheme != "https" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		styleSource := "'self'"
		mediaPage := request.URL.Path == "/admin/image-library" || request.URL.Path == "/admin/miniprogram-library" || request.URL.Path == "/admin/attachment-library" || request.URL.Path == "/admin/images.html" || request.URL.Path == "/admin/mpLib.html" || request.URL.Path == "/admin/attach.html" || request.URL.Path == "/admin/materials"
		sidebarPage := request.URL.Path == webshell.SidebarPagePath
		tagsPage := request.URL.Path == "/admin/wecom-tags"
		productPage := isProductShellPath(request.URL.Path)
		orderPage := request.URL.Path == "/admin/orders" || request.URL.Path == "/admin/orders.html" || request.URL.Path == "/admin/orderDetail.html"
		couponPage := request.URL.Path == "/admin/coupons" || strings.HasPrefix(request.URL.Path, "/admin/coupons/") || request.URL.Path == "/admin/coupons.html" || request.URL.Path == "/admin/couponForm.html"
		groupOpsPage := request.URL.Path == "/admin/automation-conversion/group-ops/ui" || request.URL.Path == "/admin/automation-conversion/group-ops/groups/ui" || request.URL.Path == "/admin/groupops.html" || request.URL.Path == "/admin/groupopsDetail.html" || strings.HasPrefix(request.URL.Path, "/admin/automation-conversion/group-ops/plans/")
		automationPage := request.URL.Path == "/admin/automation-agents" || strings.HasPrefix(request.URL.Path, "/admin/automation-agents/") || request.URL.Path == "/admin/agents.html" || request.URL.Path == "/admin/agentEdit.html"
		surveyPage := request.URL.Path == "/admin/questionnaires" || request.URL.Path == "/admin/questionnaires.html" || request.URL.Path == "/admin/questionnaireDetail.html" || request.URL.Path == "/admin/questionnaireOps.html" || strings.HasPrefix(request.URL.Path, "/h5/")
		operationCyclesPage := request.URL.Path == "/admin/operation-cycles" || strings.HasPrefix(request.URL.Path, "/admin/operation-cycles/")
		configPage := request.URL.Path == "/admin/config" || request.URL.Path == "/admin/config.html" || request.URL.Path == "/admin/configDetail.html" || request.URL.Path == "/admin/api-docs" || request.URL.Path == "/admin/apidocs.html"
		hxcPage := request.URL.Path == "/admin/hxc-dashboard"
		aiAssistantPage := request.URL.Path == "/admin/ai.html" || request.URL.Path == "/admin/aiDetail.html" || request.URL.Path == "/admin/cloud-orchestrator/plans" || strings.HasPrefix(request.URL.Path, "/admin/cloud-orchestrator/plans/")
		// Built new-shell documents embed presentational inline style attributes
		// (icon layout) and therefore share the donor pages' style relaxation.
		_, distAdminPage := webshell.DistAdminPageFile("web/dist", request.URL.Path)
		ownerHandoffPage := isOwnerHandoffUIPath(request.URL.Path)
		if (request.URL.Path == "/admin/campaigns.html" && externaleffects.ValidUIQuery(request.URL.Query())) || request.URL.Path == "/shared/data-dashboard" || hxcPage || mediaPage || tagsPage || productPage || orderPage || couponPage || groupOpsPage || automationPage || surveyPage || operationCyclesPage || configPage || aiAssistantPage || ownerHandoffPage || distAdminPage {
			styleSource = "'self' 'unsafe-inline'"
		}
		imageSource := "'self' data:"
		if mediaPage || sidebarPage {
			// Both the Media Host and the V3 sidebar create private thumbnail
			// object URLs from a scoped Media read. Keep blob: limited to these
			// presentation routes; API responses and unrelated admin pages stay
			// under the stricter image policy.
			imageSource += " blob:"
		}
		if strings.HasPrefix(request.URL.Path, "/gi/") {
			// Invitation pages render the provider-issued group QR directly. Keep
			// the exception scoped to this public route instead of weakening the
			// image policy for the admin shell or API responses.
			imageSource += " https://wework.qpic.cn"
		}
		contentPolicy := "default-src 'self'; script-src 'self' https://res.wx.qq.com; style-src " + styleSource + "; img-src " + imageSource + "; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'"
		if request.URL.Path != webshell.SidebarPagePath && !strings.HasPrefix(request.URL.Path, "/api/sidebar/") {
			writer.Header().Set("X-Frame-Options", "SAMEORIGIN")
			contentPolicy += "; frame-ancestors 'self'"
		}
		writer.Header().Set("Content-Security-Policy", contentPolicy)
		next.ServeHTTP(writer, request)
	})
}

func isProductShellPath(path string) bool {
	if strings.HasSuffix(path, "/spProductData.html") {
		return false
	}
	if strings.HasPrefix(path, "/admin/wechat-pay/products") || strings.HasPrefix(path, "/admin/service-period-products") {
		return true
	}
	switch path {
	case "/admin/products.html", "/admin/productForm.html", "/admin/spProducts.html", "/admin/spProductForm.html", "/admin/wechat-pay/spProducts.html", "/admin/wechat-pay/spProductForm.html":
		return true
	default:
		return false
	}
}
