package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

type completionStore struct {
	SubmissionStore
	questionnaire  surveyport.Questionnaire
	configuration  surveyport.OperationConfiguration
	created        bool
	claimed        bool
	createErr      error
	stored         surveyport.Submission
	storedKey      [32]byte
	storedPayload  [32]byte
	publishedReads int
	accepted       int
	bound          int
	auditOutbox    int
	saveCalls      int
	statusLocks    int
	testSnapshot   CompletionTestSnapshot
	testCreated    bool
}

func (s *completionStore) Get(context.Context, surveyport.ID, bool) (surveyport.Questionnaire, error) {
	return s.questionnaire, nil
}

func (s *completionStore) LockQuestionnaireStatus(context.Context, surveyport.ID) (surveyport.QuestionnaireStatus, error) {
	s.statusLocks++
	return s.questionnaire.Status, nil
}

func completionIntPointer(value int) *int { return &value }

func (s *completionStore) GetBySlug(context.Context, string) (surveyport.Questionnaire, error) {
	return s.questionnaire, nil
}
func (s *completionStore) GetPublishedBySlug(context.Context, string) (surveyport.Questionnaire, error) {
	s.publishedReads++
	if s.questionnaire.Status != "" && s.questionnaire.Status != surveyport.StatusPublished {
		return surveyport.Questionnaire{}, surveyport.ErrNotFound
	}
	return s.questionnaire, nil
}
func (s *completionStore) FindSubmissionByKey(_ context.Context, _ surveyport.ID, key, payload [32]byte) (surveyport.Submission, bool, error) {
	if !s.created || key != s.storedKey {
		return surveyport.Submission{}, false, nil
	}
	if payload != s.storedPayload {
		return surveyport.Submission{}, false, surveyport.ErrConflict
	}
	return s.stored, true, nil
}
func (s *completionStore) HasSubmissionClaim(context.Context, surveyport.ID, customerdomain.CustomerID) (bool, error) {
	return s.claimed, nil
}
func (s *completionStore) CreateSubmission(_ context.Context, in PersistSubmission) (surveyport.Submission, bool, error) {
	if s.createErr != nil {
		return surveyport.Submission{}, false, s.createErr
	}
	if s.created {
		if in.SubmissionKeyDigest != s.storedKey || in.PayloadDigest != s.storedPayload {
			return surveyport.Submission{}, false, surveyport.ErrConflict
		}
		return s.stored, false, nil
	}
	s.created, s.claimed = true, true
	s.storedKey, s.storedPayload = in.SubmissionKeyDigest, in.PayloadDigest
	s.stored = surveyport.Submission{ID: 9, QuestionnaireID: in.Questionnaire.ID, QuestionnaireSlug: in.Questionnaire.Slug, DefinitionVersion: in.Questionnaire.DefinitionVersion, Answers: in.Answers}
	return s.stored, true, nil
}
func (s *completionStore) GetOperationConfiguration(context.Context, surveyport.ID) (surveyport.OperationConfiguration, error) {
	return s.configuration, nil
}
func (s *completionStore) SaveOperationConfiguration(_ context.Context, value surveyport.OperationConfiguration, _ int64, now time.Time) (surveyport.OperationConfiguration, error) {
	s.saveCalls++
	value.UpdatedAt = now
	s.configuration = value
	return value, nil
}
func (s *completionStore) RecordCompletionEffect(context.Context, surveyport.ID, surveyport.ID, string, string, string, [32]byte, time.Time) error {
	s.bound++
	return nil
}
func (s *completionStore) RecordCompletionSnapshot(context.Context, surveyport.ID, surveyport.ID, surveyport.CompletionPolicy, string, []byte, time.Time) error {
	return nil
}
func (s *completionStore) GetCompletionTestSnapshot(_ context.Context, qid surveyport.ID, key string) (CompletionTestSnapshot, bool, error) {
	if s.testCreated && s.testSnapshot.QuestionnaireID == qid && s.testSnapshot.IdempotencyKey == key {
		return s.testSnapshot, true, nil
	}
	return CompletionTestSnapshot{}, false, nil
}
func (s *completionStore) RecordCompletionTestSnapshot(_ context.Context, value CompletionTestSnapshot) (CompletionTestSnapshot, bool, error) {
	if s.testCreated {
		if s.testSnapshot.PayloadDigest != value.PayloadDigest {
			return CompletionTestSnapshot{}, false, surveyport.ErrConflict
		}
		return s.testSnapshot, false, nil
	}
	s.testSnapshot, s.testCreated = value, true
	return value, true, nil
}
func (s *completionStore) RecordCompletionTestEffect(context.Context, surveyport.ID, string, string, string, string, [32]byte, time.Time) error {
	s.bound++
	return nil
}
func (s *completionStore) AppendAuditAndOutbox(context.Context, string, surveyport.ID, string, json.RawMessage, string, time.Time) error {
	s.auditOutbox++
	return nil
}

type completionAccepter struct {
	calls     int
	fail      bool
	scheduled []time.Time
}

type completionPolicyStub struct{ policy surveyport.CompletionPolicy }

func (s completionPolicyStub) CompletionPolicy(context.Context, string) (surveyport.CompletionPolicy, bool, error) {
	return s.policy, true, nil
}

func (a *completionAccepter) AcceptCompletionWithin(_ context.Context, in surveyport.CompletionIntent) (surveyport.EffectBinding, error) {
	a.calls++
	a.scheduled = append(a.scheduled, in.ScheduledAt)
	if a.fail {
		return surveyport.EffectBinding{}, errors.New("effect acceptance failed")
	}
	if in.ConfigurationReference != "local-webhook" || (in.IdempotencyKey != "survey.completion:9" && !strings.HasPrefix(in.IdempotencyKey, "survey.completion.test:questionnaire-test-")) || !strings.HasPrefix(in.SourceDigest, "sha256:") {
		return surveyport.EffectBinding{}, errors.New("unexpected completion intent")
	}
	return surveyport.EffectBinding{EffectID: "eer_9", State: "queued"}, nil
}

type completionPhoneAttacher struct{}

func (completionPhoneAttacher) AttachDeclaredPhoneToCustomer(context.Context, identityport.DeclaredPhoneCommand) (identityport.DeclaredAttachResult, error) {
	return identityport.DeclaredAttachResult{}, nil
}

type completionProjection struct{}

func (completionProjection) UpsertDirectoryProjection(context.Context, customerport.DirectoryProjection) error {
	return nil
}
func (completionProjection) MarkDirectoryStale(context.Context, []customerdomain.CustomerID, time.Time) (int64, error) {
	return 0, nil
}
func (completionProjection) UpdateDirectoryPhone(context.Context, customerdomain.CustomerID, string, identitydomain.Assurance, int64, time.Time) error {
	return nil
}
func (completionProjection) ClearDirectoryPhone(context.Context, customerdomain.CustomerID, time.Time) error {
	return nil
}

type completionStatusUOW struct{ inTransaction bool }

func (u *completionStatusUOW) Within(ctx context.Context, run func(context.Context) error) error {
	u.inTransaction = true
	defer func() { u.inTransaction = false }()
	return run(ctx)
}

type publicCompletionResolverStub struct {
	uow   *completionStatusUOW
	url   string
	found bool
	err   error
	calls int
}

func (s *publicCompletionResolverStub) ResolvePublicCompletionTarget(context.Context, string) (string, bool, error) {
	if s.uow != nil && s.uow.inTransaction {
		return "", false, errors.New("resolver invoked inside survey transaction")
	}
	s.calls++
	return s.url, s.found, s.err
}

type publicLeadQRReaderStub struct {
	uow   *completionStatusUOW
	lead  channelport.PublicLeadQRCode
	err   error
	calls int
}

func (s *publicLeadQRReaderStub) ReadPublicLeadQRCode(context.Context, int64) (channelport.PublicLeadQRCode, error) {
	if s.uow != nil && s.uow.inTransaction {
		return channelport.PublicLeadQRCode{}, errors.New("lead QR read invoked inside survey transaction")
	}
	s.calls++
	return s.lead, s.err
}

func TestPublicSubmissionStatusUsesPostCutoverClaimAndResolvesAfterCommit(t *testing.T) {
	questionnaire := surveyport.Questionnaire{ID: 4, Slug: "growth", Mode: surveyport.ModeSurvey, Status: surveyport.StatusPublished}
	store := &completionStore{questionnaire: questionnaire, configuration: surveyport.OperationConfiguration{QuestionnaireID: questionnaire.ID, CompletionNavigationRef: "survey-complete"}}
	uow := &completionStatusUOW{}
	service := NewSubmissionService(uow, store, nil)
	resolver := &publicCompletionResolverStub{uow: uow, url: "https://go.example.test/complete", found: true}
	if err := service.BindPublicCompletionTarget(resolver); err != nil {
		t.Fatal(err)
	}
	customer := customerdomain.CustomerID(7)
	identity := surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customer}

	status, err := service.PublicSubmissionStatus(context.Background(), questionnaire.Slug, identity)
	if err != nil || status.Submitted || status.CompletionAction.Type != surveyport.CompletionActionDefault || resolver.calls != 0 {
		t.Fatalf("unclaimed status=%+v err=%v resolver_calls=%d", status, err, resolver.calls)
	}

	store.claimed = true
	status, err = service.PublicSubmissionStatus(context.Background(), questionnaire.Slug, identity)
	if err != nil || !status.Submitted || status.CompletionAction.Type != surveyport.CompletionActionRedirect || status.CompletionAction.RedirectURL != "https://go.example.test/complete" || resolver.calls != 1 || uow.inTransaction {
		t.Fatalf("redirect status=%+v err=%v resolver_calls=%d in_transaction=%t", status, err, resolver.calls, uow.inTransaction)
	}

	lead := &publicLeadQRReaderStub{uow: uow, lead: channelport.PublicLeadQRCode{URL: "https://cdn.example.test/lead.png"}}
	if err = service.BindPublicLeadQRCode(lead); err != nil {
		t.Fatal(err)
	}
	channelID := int64(8)
	store.configuration.CompletionChannelID = &channelID
	resolver.url = "http://unsafe.example.test"
	status, err = service.PublicSubmissionStatus(context.Background(), questionnaire.Slug, identity)
	if err != nil || status.CompletionAction.Type != surveyport.CompletionActionDefault || lead.calls != 0 {
		t.Fatalf("unsafe redirect status=%+v err=%v lead_calls=%d", status, err, lead.calls)
	}
	resolver.url = "https://go.example.test/complete#fragment"
	status, err = service.PublicSubmissionStatus(context.Background(), questionnaire.Slug, identity)
	if err != nil || status.CompletionAction.Type != surveyport.CompletionActionDefault || lead.calls != 0 {
		t.Fatalf("fragment redirect status=%+v err=%v lead_calls=%d", status, err, lead.calls)
	}

	store.configuration.CompletionNavigationRef = ""
	status, err = service.PublicSubmissionStatus(context.Background(), questionnaire.Slug, identity)
	if err != nil || status.CompletionAction.Type != surveyport.CompletionActionLeadQR || status.CompletionAction.LeadQR == nil || status.CompletionAction.LeadQR.URL != "https://cdn.example.test/lead.png" || lead.calls != 1 || uow.inTransaction {
		t.Fatalf("lead QR status=%+v err=%v lead_calls=%d in_transaction=%t", status, err, lead.calls, uow.inTransaction)
	}
}

func TestSubmissionAlreadySubmittedReturnsSafeCompletionActionAfterRollbackBoundary(t *testing.T) {
	questionnaire := surveyport.Questionnaire{ID: 4, Slug: "growth", DefinitionVersion: 1, Mode: surveyport.ModeSurvey, Questions: []surveyport.Question{{ID: 1, Type: surveyport.QuestionTextarea, Title: "需求", Required: true, SortOrder: 0, Validation: surveyport.Validation{MinimumLength: completionIntPointer(1)}}}}
	store := &completionStore{questionnaire: questionnaire, configuration: surveyport.OperationConfiguration{QuestionnaireID: questionnaire.ID, CompletionNavigationRef: "survey-complete"}, createErr: surveyport.ErrAlreadySubmitted}
	uow := &completionStatusUOW{}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	service := NewSubmissionService(uow, store, cipher)
	service.phoneAttacher, service.phoneProjection = completionPhoneAttacher{}, completionProjection{}
	resolver := &publicCompletionResolverStub{uow: uow, url: "https://go.example.test/complete", found: true}
	if err = service.BindPublicCompletionTarget(resolver); err != nil {
		t.Fatal(err)
	}
	customer := customerdomain.CustomerID(7)
	_, err = service.Submit(context.Background(), surveyport.SubmitCommand{Slug: questionnaire.Slug, DefinitionVersion: 1, SubmissionKey: strings.Repeat("a", 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customer, EvidenceDigest: strings.Repeat("b", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: 1, TextValue: "需要帮助"}}})
	var duplicate *surveyport.AlreadySubmittedError
	if !errors.As(err, &duplicate) || duplicate.CompletionAction.Type != surveyport.CompletionActionRedirect || duplicate.CompletionAction.RedirectURL != "https://go.example.test/complete" || resolver.calls != 1 || uow.inTransaction {
		t.Fatalf("duplicate error=%v action=%+v resolver_calls=%d in_transaction=%t", err, duplicate, resolver.calls, uow.inTransaction)
	}
}

func TestSubmissionAcceptsAndBindsConfiguredCompletionOnce(t *testing.T) {
	q := surveyport.Questionnaire{ID: 4, Slug: "growth", DefinitionVersion: 1, Mode: surveyport.ModeSurvey, Questions: []surveyport.Question{{ID: 1, Type: surveyport.QuestionTextarea, Title: "需求", Required: true, SortOrder: 0, Validation: surveyport.Validation{MinimumLength: completionIntPointer(1)}, Options: []surveyport.Option{}}}}
	store := &completionStore{questionnaire: q, configuration: surveyport.OperationConfiguration{QuestionnaireID: 4, ExternalPushEnabled: true, ExternalPushConfigurationRef: "local-webhook"}}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	service := NewSubmissionService(oauthUOW{}, store, cipher)
	service.phoneAttacher, service.phoneProjection = completionPhoneAttacher{}, completionProjection{}
	accepter := &completionAccepter{}
	if err = service.BindCompletionIntent(accepter); err != nil {
		t.Fatal(err)
	}
	customer := customerdomain.CustomerID(7)
	command := surveyport.SubmitCommand{Slug: "growth", DefinitionVersion: 1, SubmissionKey: strings.Repeat("a", 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customer, EvidenceDigest: strings.Repeat("b", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: 1, TextValue: "需要帮助"}}}
	if _, err = service.Submit(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if accepter.calls != 1 || store.bound != 1 || store.auditOutbox != 1 {
		t.Fatalf("completion calls/bindings/audit-outbox=%d/%d/%d", accepter.calls, store.bound, store.auditOutbox)
	}
}

func TestSubmissionRetainsReceiptAndClaimBeforeMutablePublicValidation(t *testing.T) {
	q := surveyport.Questionnaire{ID: 4, Slug: "growth", Status: surveyport.StatusPublished, DefinitionVersion: 1, Mode: surveyport.ModeSurvey, Questions: []surveyport.Question{{ID: 1, Type: surveyport.QuestionTextarea, Title: "需求", Required: true, SortOrder: 0, Validation: surveyport.Validation{MinimumLength: completionIntPointer(1)}}}}
	store := &completionStore{questionnaire: q, configuration: surveyport.OperationConfiguration{QuestionnaireID: q.ID, CompletionNavigationRef: "survey-complete"}}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	service := NewSubmissionService(oauthUOW{}, store, cipher)
	service.phoneAttacher, service.phoneProjection = completionPhoneAttacher{}, completionProjection{}
	resolver := &publicCompletionResolverStub{url: "https://go.example.test/complete", found: true}
	if err = service.BindPublicCompletionTarget(resolver); err != nil {
		t.Fatal(err)
	}
	customer := customerdomain.CustomerID(7)
	command := surveyport.SubmitCommand{Slug: q.Slug, DefinitionVersion: 1, SubmissionKey: strings.Repeat("a", 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customer, EvidenceDigest: strings.Repeat("b", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: 1, TextValue: "需要帮助"}}}
	first, err := service.Submit(context.Background(), command)
	if err != nil || first.SubmissionID != 9 {
		t.Fatalf("first receipt=%+v err=%v", first, err)
	}
	readsAfterFirst := store.publishedReads

	// A later re-publish or stop may invalidate the mutable definition, but it
	// must not invalidate the exact same idempotency receipt.
	store.questionnaire.Status = surveyport.StatusDisabled
	store.questionnaire.DefinitionVersion = 2
	store.questionnaire.Questions = nil
	replayed, err := service.Submit(context.Background(), command)
	if err != nil || replayed != first || store.publishedReads != readsAfterFirst {
		t.Fatalf("retained replay=%+v err=%v published_reads=%d/%d", replayed, err, store.publishedReads, readsAfterFirst)
	}

	drifted := command
	drifted.Answers = []surveyport.SubmissionAnswer{{QuestionID: 1, TextValue: "不同内容"}}
	if _, err = service.Submit(context.Background(), drifted); !errors.Is(err, surveyport.ErrConflict) || store.publishedReads != readsAfterFirst {
		t.Fatalf("payload drift err=%v published_reads=%d/%d", err, store.publishedReads, readsAfterFirst)
	}

	store.questionnaire.Status = surveyport.StatusArchived
	duplicate := command
	duplicate.DefinitionVersion = 0
	duplicate.SubmissionKey = strings.Repeat("c", 43)
	_, err = service.Submit(context.Background(), duplicate)
	var already *surveyport.AlreadySubmittedError
	if !errors.As(err, &already) || already.CompletionAction.Type != surveyport.CompletionActionRedirect || already.CompletionAction.RedirectURL != "https://go.example.test/complete" || store.publishedReads != readsAfterFirst {
		t.Fatalf("retained claim err=%v action=%+v published_reads=%d/%d", err, already, store.publishedReads, readsAfterFirst)
	}
}

func TestPublicSubmissionStatusRetainsClaimAfterQuestionnaireStops(t *testing.T) {
	customer := customerdomain.CustomerID(7)
	identity := surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customer}
	for _, state := range []surveyport.QuestionnaireStatus{surveyport.StatusDisabled, surveyport.StatusArchived} {
		t.Run(string(state), func(t *testing.T) {
			store := &completionStore{questionnaire: surveyport.Questionnaire{ID: 4, Slug: "growth", Mode: surveyport.ModeSurvey, Status: state}, claimed: true}
			service := NewSubmissionService(oauthUOW{}, store, nil)
			status, err := service.PublicSubmissionStatus(context.Background(), "growth", identity)
			if err != nil || !status.Submitted || status.CompletionAction.Type != surveyport.CompletionActionDefault || store.publishedReads != 0 {
				t.Fatalf("retained status=%+v err=%v published_reads=%d", status, err, store.publishedReads)
			}
			store.claimed = false
			if _, err = service.PublicSubmissionStatus(context.Background(), "growth", identity); !errors.Is(err, surveyport.ErrNotFound) || store.publishedReads != 1 {
				t.Fatalf("unsubmitted stopped questionnaire err=%v published_reads=%d", err, store.publishedReads)
			}
		})
	}
}

func TestOperationConfigurationRejectsNonObjectMetadata(t *testing.T) {
	service := &SubmissionService{}
	_, err := service.SaveOperationConfiguration(context.Background(), surveyport.OperationConfiguration{QuestionnaireID: 1, ExternalPushMetadata: []byte(`[]`)}, 1, "survey-config-invalid-metadata-0001")
	if !errors.Is(err, surveyport.ErrInvalid) {
		t.Fatalf("non-object metadata error=%v", err)
	}
}

func TestOperationConfigurationRejectsArchivedQuestionnaireWithoutChangingRetainedConfiguration(t *testing.T) {
	existing := surveyport.OperationConfiguration{
		QuestionnaireID: 7, CompletionNavigationRef: "history-navigation", ExternalPushEnabled: true,
		ExternalPushConfigurationRef: "history-push", ExternalPushMetadata: json.RawMessage(`{"remark":"retained"}`), Version: 4,
	}
	store := &completionStore{questionnaire: surveyport.Questionnaire{ID: 7, Status: surveyport.StatusArchived}, configuration: existing}
	service := NewSubmissionService(oauthUOW{}, store, nil)
	_, err := service.SaveOperationConfiguration(context.Background(), surveyport.OperationConfiguration{
		QuestionnaireID: 7, CompletionNavigationRef: "new-navigation", ExternalPushEnabled: true,
		ExternalPushConfigurationRef: "new-push", ExternalPushMetadata: json.RawMessage(`{"remark":"must-not-write"}`), Version: 4,
	}, 8, "survey-archived-config-0001")
	if !errors.Is(err, surveyport.ErrNotFound) {
		t.Fatalf("archived configuration write error=%v", err)
	}
	if store.statusLocks != 1 || store.saveCalls != 0 || !reflect.DeepEqual(store.configuration, existing) {
		t.Fatalf("archived configuration mutated retained history locks=%d saves=%d configuration=%+v", store.statusLocks, store.saveCalls, store.configuration)
	}
	readback, err := service.GetOperationConfiguration(context.Background(), 7)
	if err != nil || !reflect.DeepEqual(readback, existing) {
		t.Fatalf("archived historical configuration readback=%+v err=%v", readback, err)
	}
}

func TestQueueCompletionTestFreezesSyntheticRequestAndReplaysSameEffect(t *testing.T) {
	q := surveyport.Questionnaire{ID: 4, Title: "增长调研", Status: surveyport.StatusDraft}
	store := &completionStore{questionnaire: q, configuration: surveyport.OperationConfiguration{QuestionnaireID: q.ID, ExternalPushEnabled: true, ExternalPushConfigurationRef: "local-webhook", ExternalPushMetadata: json.RawMessage(`{"custom_params":{"campaign":"autumn","unionid":"must-not-send"}}`)}}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	service := NewSubmissionService(oauthUOW{}, store, cipher)
	accepter := &completionAccepter{}
	if err = service.BindCompletionIntent(accepter); err != nil {
		t.Fatal(err)
	}
	if err = service.BindCompletionPolicy(completionPolicyStub{policy: surveyport.CompletionPolicy{ConfigurationReference: "local-webhook", ConfigurationVersion: "v1", ConfigurationDigest: "sha256:" + strings.Repeat("a", 64)}}); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 9, 5, 12, 0, 0, 987654321, time.UTC)
	canonical := fixed.Truncate(time.Microsecond)
	service.now = func() time.Time { return fixed }
	key := "survey-completion-test-command-0001"
	first, err := service.QueueCompletionTest(context.Background(), q.ID, 8, key)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return fixed.Add(time.Hour) }
	second, err := service.QueueCompletionTest(context.Background(), q.ID, 8, key)
	if err != nil {
		t.Fatal(err)
	}
	if first.TestRunID == "" || first != second || accepter.calls != 2 || store.testSnapshot.SubmittedAt != canonical || len(accepter.scheduled) != 2 || !accepter.scheduled[0].IsZero() || !accepter.scheduled[1].IsZero() || store.testSnapshot.Policy.CustomParams["campaign"] != "autumn" || store.testSnapshot.Policy.CustomParams["unionid"] != "" {
		t.Fatalf("synthetic replay=%+v/%+v snapshot=%+v calls=%d", first, second, store.testSnapshot, accepter.calls)
	}
	store.configuration.ExternalPushMetadata = json.RawMessage(`{"remark":"changed"}`)
	store.questionnaire.Status = surveyport.StatusArchived
	third, err := service.QueueCompletionTest(context.Background(), q.ID, 8, key)
	if err != nil || third != first || accepter.calls != 3 || store.testSnapshot.Policy.Remark != "" {
		t.Fatalf("changed configuration retargeted test=%+v err=%v calls=%d snapshot=%+v", third, err, accepter.calls, store.testSnapshot)
	}
	if _, err = service.QueueCompletionTest(context.Background(), q.ID, 8, "survey-completion-test-command-0002"); !errors.Is(err, surveyport.ErrNotFound) || accepter.calls != 3 {
		t.Fatalf("archived questionnaire accepted a new test err=%v calls=%d", err, accepter.calls)
	}
}
