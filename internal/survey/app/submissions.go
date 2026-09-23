package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	surveydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

type PersistSubmission struct {
	Questionnaire                                   surveyport.Questionnaire
	Command                                         surveyport.SubmitCommand
	SubmissionKeyDigest, PayloadDigest, TokenDigest [32]byte
	Token                                           string
	TotalScore                                      float64
	Result                                          surveyport.AssessmentResult
	Answers                                         []surveyport.AnswerSnapshot
	Now                                             time.Time
}

type SubmissionStore interface {
	Get(context.Context, surveyport.ID, bool) (surveyport.Questionnaire, error)
	LockQuestionnaireStatus(context.Context, surveyport.ID) (surveyport.QuestionnaireStatus, error)
	GetBySlug(context.Context, string) (surveyport.Questionnaire, error)
	GetPublishedBySlug(context.Context, string) (surveyport.Questionnaire, error)
	FindSubmissionByKey(context.Context, surveyport.ID, [32]byte, [32]byte) (surveyport.Submission, bool, error)
	HasSubmissionClaim(context.Context, surveyport.ID, customerdomain.CustomerID) (bool, error)
	CreateSubmission(context.Context, PersistSubmission) (surveyport.Submission, bool, error)
	GetSubmissionByTokenDigest(context.Context, [32]byte) (surveyport.Submission, error)
	ListSubmissions(context.Context, surveyport.ID, int32, int32, surveyport.IdentityState) ([]surveyport.Submission, int64, error)
	GetSubmission(context.Context, surveyport.ID) (surveyport.Submission, error)
	CustomerHistory(context.Context, int64, int32, int32) ([]surveyport.Submission, int64, error)
	SubmissionAnalytics(context.Context, surveyport.ID) (surveyport.Analytics, error)
	ListOperationReceipts(context.Context, surveyport.ID, int32, int32) ([]surveyport.OperationReceipt, int64, error)
	ListLegacyUnresolved(context.Context, surveyport.ID, int32, int32) ([]surveyport.LegacySubmission, int64, error)
	GetLegacyUnresolved(context.Context, surveyport.ID) (surveyport.LegacySubmission, error)
	ListLegacyAnswers(context.Context, surveyport.ID, int32, int32) ([]surveyport.LegacyAnswer, int64, error)
	GetOperationConfiguration(context.Context, surveyport.ID) (surveyport.OperationConfiguration, error)
	SaveOperationConfiguration(context.Context, surveyport.OperationConfiguration, int64, time.Time) (surveyport.OperationConfiguration, error)
	RecordDisabledOperation(context.Context, surveyport.ID, *surveyport.ID, string, [32]byte, time.Time) (surveyport.OperationReceipt, error)
	RecordCompletionEffect(context.Context, surveyport.ID, surveyport.ID, string, string, string, [32]byte, time.Time) error
	RecordCompletionSnapshot(context.Context, surveyport.ID, surveyport.ID, surveyport.CompletionPolicy, string, []byte, time.Time) error
	GetCompletionTestSnapshot(context.Context, surveyport.ID, string) (CompletionTestSnapshot, bool, error)
	RecordCompletionTestSnapshot(context.Context, CompletionTestSnapshot) (CompletionTestSnapshot, bool, error)
	RecordCompletionTestEffect(context.Context, surveyport.ID, string, string, string, string, [32]byte, time.Time) error
	AppendAuditAndOutbox(context.Context, string, surveyport.ID, string, json.RawMessage, string, time.Time) error
	RecordPhoneBinding(context.Context, surveyport.ID, surveyport.ID, int64, int64, identityport.DeclaredAttachStatus, [32]byte, time.Time) error
}

// CompletionTestSnapshot freezes one admin-triggered synthetic test request.
// It never carries a Customer, external identity, phone number, or answers.
type CompletionTestSnapshot struct {
	QuestionnaireID               surveyport.ID
	TestRunID, QuestionnaireTitle string
	SubmittedAt                   time.Time
	Policy                        surveyport.CompletionPolicy
	SourceDigest, TargetDigest    string
	PayloadDigest, PolicyDigest   string
	IdempotencyKey                string
}

type customerHistoryWindowStore interface {
	CustomerHistoryWindow(context.Context, surveyport.CustomerHistoryQuery) ([]surveyport.Submission, error)
}

type externalSubmissionStore interface {
	ExternalSubmissions(context.Context, surveyport.ExternalSubmissionQuery) (surveyport.ExternalSubmissionPage, error)
}

type SubmissionService struct {
	observer            surveyport.SubmissionObserver
	uow                 platformport.UnitOfWork
	store               SubmissionStore
	cipher              *secure.Cipher
	timeline            customerport.TimelineWriter
	phoneAttacher       identityport.DeclaredPhoneAttacher
	phoneProjection     customerport.ProjectionWriter
	completion          surveyport.CompletionIntentAccepter
	completionPolicy    surveyport.CompletionPolicyResolver
	completionIdentity  surveyport.CompletionIdentitySnapshotter
	completionEndpoints surveyport.CompletionEndpointManager
	publicRedirect      surveyport.PublicCompletionTargetResolver
	publicLeadQR        channelport.PublicLeadQRCodeReader
	now                 func() time.Time
}

func (s *SubmissionService) BindCompletionEndpoints(manager surveyport.CompletionEndpointManager) error {
	if s == nil || manager == nil {
		return surveyport.ErrInvalid
	}
	s.completionEndpoints = manager
	return nil
}

func (s *SubmissionService) BindCompletionPolicy(resolver surveyport.CompletionPolicyResolver) error {
	if s == nil || resolver == nil {
		return surveyport.ErrInvalid
	}
	s.completionPolicy = resolver
	return nil
}
func (s *SubmissionService) BindCompletionIdentity(snapshotter surveyport.CompletionIdentitySnapshotter) error {
	if s == nil || snapshotter == nil {
		return surveyport.ErrInvalid
	}
	s.completionIdentity = snapshotter
	return nil
}

// BindPublicCompletionTarget supplies only the public, allowlisted completion
// destination resolver. It is intentionally unrelated to external completion
// Provider targets and remains optional: an absent or failed resolver safely
// yields the default completion page.
func (s *SubmissionService) BindPublicCompletionTarget(resolver surveyport.PublicCompletionTargetResolver) error {
	if s == nil || resolver == nil {
		return surveyport.ErrInvalid
	}
	s.publicRedirect = resolver
	return nil
}

// BindPublicLeadQRCode supplies Channel's active public QR projection through
// its stable read port. Survey neither reads Channel tables nor writes Channel
// state.
func (s *SubmissionService) BindPublicLeadQRCode(reader channelport.PublicLeadQRCodeReader) error {
	if s == nil || reader == nil {
		return surveyport.ErrInvalid
	}
	s.publicLeadQR = reader
	return nil
}

// BindCompletionIntent installs the composition-owned transaction-bound
// external-effect accepter. Survey retains its own binding receipt; the
// adapter owns no Survey tables and Provider execution remains outside UoW.
func (s *SubmissionService) BindCompletionIntent(accepter surveyport.CompletionIntentAccepter) error {
	if s == nil || accepter == nil {
		return surveyport.ErrInvalid
	}
	s.completion = accepter
	return nil
}

func (s *SubmissionService) BindDeclaredPhone(attacher identityport.DeclaredPhoneAttacher, projection customerport.ProjectionWriter) error {
	if s == nil || attacher == nil || projection == nil {
		return surveyport.ErrInvalid
	}
	s.phoneAttacher, s.phoneProjection = attacher, projection
	return nil
}

func NewSubmissionService(uow platformport.UnitOfWork, store SubmissionStore, cipher *secure.Cipher) *SubmissionService {
	return &SubmissionService{uow: uow, store: store, cipher: cipher, now: time.Now}
}

// BindCustomerTimeline installs the stable Customer projection port before the
// service is exposed. It grants no Customer identity or mutation capability.
func (s *SubmissionService) BindCustomerTimeline(writer customerport.TimelineWriter) error {
	if s == nil || writer == nil {
		return surveyport.ErrInvalid
	}
	s.timeline = writer
	return nil
}

func (s *SubmissionService) ReadPublic(ctx context.Context, slug string) (surveyport.Questionnaire, error) {
	if s == nil || s.uow == nil || s.store == nil || !validSlug(slug) {
		return surveyport.Questionnaire{}, surveyport.ErrNotFound
	}
	var result surveyport.Questionnaire
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		result, e = s.store.GetPublishedBySlug(tx, slug)
		if e == nil && result.Mode != surveyport.ModeSurvey {
			return surveyport.ErrNotFound
		}
		return e
	})
	return result, classify(err)
}

// PublicSubmissionStatus only accepts an already trusted, resolved Survey
// session. The claim table is Survey-owned and deliberately starts empty at
// cutover, so historical submissions do not retrospectively consume a user's
// entitlement.
func (s *SubmissionService) PublicSubmissionStatus(ctx context.Context, slug string, identity surveyport.SubmissionIdentity) (surveyport.PublicSubmissionStatus, error) {
	result := surveyport.PublicSubmissionStatus{CompletionAction: surveyport.DefaultCompletionAction()}
	if s == nil || s.uow == nil || s.store == nil || !validSlug(slug) || identity.State != surveyport.IdentityResolved || identity.CustomerID == nil || *identity.CustomerID < 1 {
		return surveyport.PublicSubmissionStatus{}, surveyport.ErrInvalid
	}
	var configuration surveyport.OperationConfiguration
	err := s.uow.Within(ctx, func(tx context.Context) error {
		questionnaire, err := s.store.GetBySlug(tx, slug)
		if err != nil {
			return err
		}
		if questionnaire.Mode != surveyport.ModeSurvey {
			return surveyport.ErrNotFound
		}
		claimed, err := s.store.HasSubmissionClaim(tx, questionnaire.ID, *identity.CustomerID)
		if err != nil {
			return err
		}
		result.Submitted = claimed
		if claimed {
			configuration, err = s.store.GetOperationConfiguration(tx, questionnaire.ID)
			return err
		}
		// Only an already-submitted trusted customer may reach the retained
		// completion action after a questionnaire is disabled or archived.
		// New visitors still use the normal public-published gate.
		_, err = s.store.GetPublishedBySlug(tx, slug)
		return err
	})
	if err != nil {
		return result, classify(err)
	}
	if result.Submitted {
		// Channel and Composition-owned target reads must happen after the
		// Survey UoW commits. Neither port is assumed to participate in this
		// transaction, and a failed public read safely falls back below.
		result.CompletionAction = s.resolvePublicCompletionAction(ctx, configuration)
	}
	return result, nil
}

func (s *SubmissionService) Submit(ctx context.Context, command surveyport.SubmitCommand) (surveyport.SubmissionReceipt, error) {
	if s == nil || s.uow == nil || s.store == nil || s.cipher == nil || s.phoneAttacher == nil || s.phoneProjection == nil || !validSlug(command.Slug) || !validPublicKey(command.SubmissionKey) || command.Identity.State != surveyport.IdentityResolved || command.Identity.CustomerID == nil || !validIdentity(command.Identity) || len(command.SourceChannel) > 100 || len(command.CampaignID) > 200 || len(command.StaffID) > 200 {
		return surveyport.SubmissionReceipt{}, surveyport.ErrInvalid
	}
	canonical, _ := json.Marshal(struct {
		Version       int64                         `json:"version"`
		Answers       []surveyport.SubmissionAnswer `json:"answers"`
		IdentityState surveyport.IdentityState      `json:"identity_state"`
		Evidence      string                        `json:"evidence_digest,omitempty"`
	}{command.DefinitionVersion, command.Answers, command.Identity.State, command.Identity.EvidenceDigest})
	payloadDigest := sha256.Sum256(canonical)
	submissionKeyDigest := sha256.Sum256([]byte(command.SubmissionKey))
	token, err := s.cipher.Token(command.Slug, command.SubmissionKey)
	if err != nil {
		return surveyport.SubmissionReceipt{}, surveyport.ErrUnavailable
	}
	tokenDigest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var stored surveyport.Submission
	completionAction := surveyport.DefaultCompletionAction()
	var completionConfiguration surveyport.OperationConfiguration
	alreadySubmitted := false
	err = s.uow.Within(ctx, func(tx context.Context) error {
		// Idempotency and the post-cutover customer claim intentionally precede
		// the mutable public definition. A retained same-key receipt must be
		// recoverable after re-publish, and a claimed customer must not see a
		// validation error merely because the questionnaire later stopped.
		questionnaire, e := s.store.GetBySlug(tx, command.Slug)
		if e != nil {
			return e
		}
		var found bool
		stored, found, e = s.store.FindSubmissionByKey(tx, questionnaire.ID, submissionKeyDigest, payloadDigest)
		if e != nil {
			return e
		}
		if found {
			completionConfiguration, e = s.store.GetOperationConfiguration(tx, questionnaire.ID)
			return e
		}
		claimed, e := s.store.HasSubmissionClaim(tx, questionnaire.ID, *command.Identity.CustomerID)
		if e != nil {
			return e
		}
		if claimed {
			completionConfiguration, e = s.store.GetOperationConfiguration(tx, questionnaire.ID)
			if e != nil {
				return e
			}
			alreadySubmitted = true
			return nil
		}

		questionnaire, e = s.store.GetPublishedBySlug(tx, command.Slug)
		if e != nil {
			return e
		}
		if command.DefinitionVersion < 1 {
			return surveyport.ErrInvalid
		}
		if questionnaire.DefinitionVersion != command.DefinitionVersion {
			return surveyport.ErrConflict
		}
		if e = surveydomain.ValidateAnswers(questionnaire.Questions, command.Answers); e != nil {
			return surveyport.ErrInvalid
		}
		answers, total, e := snapshotAnswers(questionnaire, command.Answers)
		if e != nil {
			return surveyport.ErrInvalid
		}
		result := surveyport.AssessmentResult{Dimensions: []surveyport.AssessmentDimensionResult{}, StrengthDimensionKeys: []string{}, WeaknessDimensionKeys: []string{}, TagCodes: []string{}}
		if questionnaire.Mode != surveyport.ModeSurvey {
			return surveyport.ErrNotFound
		}
		var created bool
		stored, created, e = s.store.CreateSubmission(tx, PersistSubmission{Questionnaire: questionnaire, Command: command, SubmissionKeyDigest: submissionKeyDigest, PayloadDigest: payloadDigest, TokenDigest: tokenDigest, Token: token, TotalScore: total, Result: result, Answers: answers, Now: now})
		if e != nil {
			if errors.Is(e, surveyport.ErrAlreadySubmitted) {
				completionConfiguration, e = s.store.GetOperationConfiguration(tx, questionnaire.ID)
				if e != nil {
					return e
				}
				alreadySubmitted = true
				return nil
			}
			return e
		}
		if !created {
			completionConfiguration, e = s.store.GetOperationConfiguration(tx, questionnaire.ID)
			return e
		}
		if s.observer != nil && command.Identity.CustomerID != nil {
			if e = s.observer.SubmissionCreatedWithin(tx, int64(stored.ID), int64(*command.Identity.CustomerID)); e != nil {
				return e
			}
		}
		for _, answer := range stored.Answers {
			if answer.QuestionType != surveyport.QuestionMobile {
				continue
			}
			var phone string
			for _, original := range command.Answers {
				if answer.QuestionID != nil && original.QuestionID == *answer.QuestionID {
					phone = original.TextValue
					break
				}
			}
			if phone == "" {
				continue
			}
			bindingKey := fmt.Sprintf("survey-phone:%d:%d", stored.ID, answer.ID)
			binding, attachErr := s.phoneAttacher.AttachDeclaredPhoneToCustomer(tx, identityport.DeclaredPhoneCommand{CustomerID: *command.Identity.CustomerID, Phone: phone, Source: "survey", SourceEventID: fmt.Sprintf("submission:%d:answer:%d", stored.ID, answer.ID), IdempotencyKey: bindingKey})
			if attachErr != nil {
				return attachErr
			}
			evidence := sha256.Sum256([]byte(command.Identity.EvidenceDigest + "\x00" + bindingKey + "\x00" + string(binding.Status)))
			if e = s.store.RecordPhoneBinding(tx, stored.ID, answer.ID, int64(*command.Identity.CustomerID), binding.IdentityID, binding.Status, evidence, now); e != nil {
				return e
			}
			if binding.Status == identityport.DeclaredAttached || binding.Status == identityport.DeclaredAlreadyLinked || binding.Status == identityport.DeclaredReplayed {
				if e = s.phoneProjection.UpdateDirectoryPhone(tx, *command.Identity.CustomerID, answer.TextValueMasked, identitydomain.AssuranceDeclared, 1, now); e != nil {
					return e
				}
			}
		}
		payload, _ := json.Marshal(map[string]any{"questionnaire_id": questionnaire.ID, "submission_id": stored.ID, "identity_state": command.Identity.State})
		outboxKey := "survey.submission:" + hex.EncodeToString(submissionKeyDigest[:])
		if e = s.store.AppendAuditAndOutbox(tx, "submission_created", questionnaire.ID, "public", payload, outboxKey, now); e != nil {
			return e
		}
		configuration, e := s.store.GetOperationConfiguration(tx, questionnaire.ID)
		if e != nil {
			return e
		}
		if configuration.ExternalPushEnabled {
			if s.completion == nil {
				return surveyport.ErrEffectUnavailable
			}
			intent := completionIntent(questionnaire.ID, stored.ID, configuration.ExternalPushConfigurationRef, payloadDigest, now)
			policy := surveyport.CompletionPolicy{ConfigurationReference: configuration.ExternalPushConfigurationRef}
			if s.completionPolicy != nil {
				resolved, found, policyErr := s.completionPolicy.CompletionPolicy(tx, configuration.ExternalPushConfigurationRef)
				if policyErr != nil {
					return policyErr
				}
				if found {
					policy = resolved
				}
			}
			if len(configuration.ExternalPushMetadata) > 0 && json.Unmarshal(configuration.ExternalPushMetadata, &policy) != nil {
				return surveyport.ErrInvalid
			}
			policy.ConfigurationReference = configuration.ExternalPushConfigurationRef
			binding, acceptErr := s.completion.AcceptCompletionWithin(tx, intent)
			if acceptErr != nil || binding.EffectID == "" || binding.State == "" {
				if acceptErr != nil {
					return acceptErr
				}
				return surveyport.ErrEffectUnavailable
			}
			keyDigest := sha256.Sum256([]byte(intent.SourceDigest))
			if e = s.store.RecordCompletionEffect(tx, questionnaire.ID, stored.ID, configuration.ExternalPushConfigurationRef, binding.EffectID, binding.State, keyDigest, now); e != nil {
				return e
			}
			var identityCiphertext []byte
			if s.completionIdentity != nil && command.Identity.CustomerID != nil {
				value, found, snapshotErr := s.completionIdentity.SnapshotCompletionIdentity(tx, int64(*command.Identity.CustomerID), policy)
				if snapshotErr != nil {
					return snapshotErr
				}
				if found && value != "" {
					identityCiphertext, _ = s.cipher.Encrypt(value)
				}
			}
			if e = s.store.RecordCompletionSnapshot(tx, questionnaire.ID, stored.ID, policy, command.Identity.EvidenceDigest, identityCiphertext, now); e != nil {
				return e
			}
		}
		if command.Identity.CustomerID != nil && s.timeline != nil {
			if e = s.timeline.AppendTimeline(tx, customerport.TimelineEvent{CustomerID: *command.Identity.CustomerID,
				SourceDomain: "survey", SourceEventID: "submission:" + fmt.Sprint(stored.ID), EventType: "customer.survey_submitted",
				Title: "问卷已提交", OccurredAt: now}); e != nil {
				return e
			}
		}
		completionConfiguration, e = s.store.GetOperationConfiguration(tx, questionnaire.ID)
		return e
	})
	if err != nil {
		return surveyport.SubmissionReceipt{}, classify(err)
	}
	completionAction = s.resolvePublicCompletionAction(ctx, completionConfiguration)
	if alreadySubmitted {
		return surveyport.SubmissionReceipt{}, &surveyport.AlreadySubmittedError{CompletionAction: completionAction}
	}
	return surveyport.SubmissionReceipt{QuestionnaireID: stored.QuestionnaireID, QuestionnaireSlug: stored.QuestionnaireSlug, DefinitionVersion: stored.DefinitionVersion, SubmissionID: stored.ID, ResultToken: token, CompletionAction: completionAction}, nil
}

// resolvePublicCompletionAction deliberately runs after Survey's transaction
// has ended. Channel and composition resolver ports may own another Unit of
// Work; nesting them under Survey would split or deadlock the write boundary.
// All resolver and QR failures degrade to the non-sensitive default page.
func (s *SubmissionService) resolvePublicCompletionAction(ctx context.Context, configuration surveyport.OperationConfiguration) surveyport.CompletionAction {
	fallback := surveyport.DefaultCompletionAction()
	if len(configuration.CompletionTarget) > 0 {
		if redirectURL, found := s.resolveStoredCompletionTarget(ctx, configuration.CompletionTarget); found && safePublicCompletionURL(redirectURL) {
			return surveyport.CompletionAction{Type: surveyport.CompletionActionRedirect, RedirectURL: redirectURL}
		}
	}
	if configuration.CompletionNavigationRef != "" {
		if s.publicRedirect == nil {
			return fallback
		}
		redirectURL, found, resolveErr := s.publicRedirect.ResolvePublicCompletionTarget(ctx, configuration.CompletionNavigationRef)
		if resolveErr != nil || !found || !safePublicCompletionURL(redirectURL) {
			return fallback
		}
		return surveyport.CompletionAction{Type: surveyport.CompletionActionRedirect, RedirectURL: redirectURL}
	}
	if configuration.CompletionChannelID == nil || s.publicLeadQR == nil {
		return fallback
	}
	lead, readErr := s.publicLeadQR.ReadPublicLeadQRCode(ctx, *configuration.CompletionChannelID)
	if readErr != nil || !safePublicCompletionURL(lead.URL) {
		return fallback
	}
	return surveyport.CompletionAction{Type: surveyport.CompletionActionLeadQR, LeadQR: &surveyport.CompletionLeadQRCode{URL: lead.URL, Title: configuration.LeadQRTitle, Subtitle: configuration.LeadQRSubtitle}}
}

func safePublicCompletionURL(raw string) bool {
	return safeStoredSurveyRedirect(raw)
}

func completionIntent(questionnaireID, submissionID surveyport.ID, configurationRef string, submissionPayloadDigest [32]byte, now time.Time) surveyport.CompletionIntent {
	source := surveyDigest("survey.completion.source.v1", fmt.Sprint(questionnaireID), fmt.Sprint(submissionID))
	return surveyport.CompletionIntent{
		QuestionnaireID: questionnaireID, SubmissionID: submissionID, ConfigurationReference: configurationRef,
		SourceDigest:   source,
		TargetDigest:   surveyDigest("survey.completion.target.v1", configurationRef),
		PayloadDigest:  surveyDigest("survey.completion.payload.v1", hex.EncodeToString(submissionPayloadDigest[:])),
		PolicyDigest:   surveyDigest("survey.completion.policy.v1", "v1"),
		IdempotencyKey: "survey.completion:" + fmt.Sprint(submissionID), ScheduledAt: now,
	}
}

func surveyDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func snapshotAnswers(q surveyport.Questionnaire, values []surveyport.SubmissionAnswer) ([]surveyport.AnswerSnapshot, float64, error) {
	byID := map[surveyport.ID]surveyport.SubmissionAnswer{}
	for _, a := range values {
		byID[a.QuestionID] = a
	}
	out := make([]surveyport.AnswerSnapshot, 0, len(values))
	total := 0.0
	for _, question := range q.Questions {
		answer, ok := byID[question.ID]
		if !ok {
			continue
		}
		qid := question.ID
		snapshot := surveyport.AnswerSnapshot{QuestionID: &qid, QuestionType: question.Type, QuestionTitle: question.Title, SortOrder: question.SortOrder, SelectedOptions: []surveyport.SelectedOptionSnapshot{}, TextValue: answer.TextValue}
		options := map[surveyport.ID]surveyport.Option{}
		for _, o := range question.Options {
			options[o.ID] = o
		}
		for _, id := range answer.OptionIDs {
			o, exists := options[id]
			if !exists {
				return nil, 0, surveyport.ErrInvalid
			}
			snapshot.SelectedOptions = append(snapshot.SelectedOptions, surveyport.SelectedOptionSnapshot{OptionID: id, OptionText: o.Text, Score: o.Score, TagCodes: append([]string(nil), o.TagCodes...)})
			snapshot.Score += o.Score
			total += o.Score
		}
		if question.Type == surveyport.QuestionMobile {
			snapshot.TextValueMasked = maskMobile(answer.TextValue)
		} else if answer.TextValue != "" {
			snapshot.TextValueMasked = "[protected]"
		}
		out = append(out, snapshot)
	}
	return out, total, nil
}

func (s *SubmissionService) QueryResult(ctx context.Context, token string) (surveyport.Submission, error) {
	if s == nil || s.uow == nil || s.store == nil || !validPublicKey(token) {
		return surveyport.Submission{}, surveyport.ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	var result surveyport.Submission
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		result, e = s.store.GetSubmissionByTokenDigest(tx, digest)
		return e
	})
	return result, classify(err)
}
func (s *SubmissionService) ListSubmissions(ctx context.Context, id surveyport.ID, limit, offset int32, state surveyport.IdentityState) (surveyport.SubmissionPage, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if s == nil || s.uow == nil || s.store == nil || id < 1 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset || state != "" && !validIdentityState(state) {
		return surveyport.SubmissionPage{}, surveyport.ErrInvalid
	}
	page := surveyport.SubmissionPage{Limit: limit, Offset: offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		page.Items, page.Total, e = s.store.ListSubmissions(tx, id, limit, offset, state)
		return e
	})
	return page, classify(err)
}
func (s *SubmissionService) GetSubmission(ctx context.Context, id surveyport.ID) (surveyport.Submission, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 {
		return surveyport.Submission{}, surveyport.ErrNotFound
	}
	var result surveyport.Submission
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; result, e = s.store.GetSubmission(tx, id); return e })
	return result, classify(err)
}
func (s *SubmissionService) CustomerHistory(ctx context.Context, customer int64, limit, offset int32) (surveyport.SubmissionPage, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if s == nil || s.uow == nil || s.store == nil || customer < 1 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset {
		return surveyport.SubmissionPage{}, surveyport.ErrInvalid
	}
	page := surveyport.SubmissionPage{Limit: limit, Offset: offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		page.Items, page.Total, e = s.store.CustomerHistory(tx, customer, limit, offset)
		return e
	})
	return page, classify(err)
}

// ExternalSubmissions exposes the exact Survey-owned historic projection for
// the API Host after it has completed machine authorization and OneID
// resolution. Survey treats union values as source facts only.
func (s *SubmissionService) ExternalSubmissions(ctx context.Context, query surveyport.ExternalSubmissionQuery) (surveyport.ExternalSubmissionPage, error) {
	if s == nil || s.uow == nil || s.store == nil || !validExternalSubmissionQuery(query) {
		return surveyport.ExternalSubmissionPage{}, surveyport.ErrInvalid
	}
	store, ok := s.store.(externalSubmissionStore)
	if !ok {
		return surveyport.ExternalSubmissionPage{}, surveyport.ErrUnavailable
	}
	page := surveyport.ExternalSubmissionPage{Items: []surveyport.ExternalSubmission{}, Limit: query.Limit, Offset: query.Offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		page, e = store.ExternalSubmissions(tx, query)
		return e
	})
	return page, classify(err)
}

func validExternalSubmissionQuery(query surveyport.ExternalSubmissionQuery) bool {
	if query.CustomerID < 1 || len(query.HistoricalUnionIDs) > 32 || query.QuestionnaireSourceID < 0 || query.Limit < 1 || query.Limit > 500 || query.Offset < 0 ||
		((query.SourceSystem == "") != (query.SourceRecordID == "") || len(query.SourceSystem) > 200 || len(query.SourceRecordID) > 500) ||
		(query.BeforeSubmittedAt.IsZero() != (query.BeforeSubmissionID == 0)) || query.BeforeSubmissionID < 0 ||
		(!query.SubmittedFrom.IsZero() && !query.SubmittedTo.IsZero() && query.SubmittedFrom.After(query.SubmittedTo)) {
		return false
	}
	seen := make(map[string]struct{}, len(query.HistoricalUnionIDs))
	for _, unionID := range query.HistoricalUnionIDs {
		if unionID == "" || len(unionID) > 1024 || strings.TrimSpace(unionID) != unionID {
			return false
		}
		if _, duplicate := seen[unionID]; duplicate {
			return false
		}
		seen[unionID] = struct{}{}
	}
	return true
}

func (s *SubmissionService) CustomerHistoryWindow(ctx context.Context, query surveyport.CustomerHistoryQuery) (surveyport.CustomerHistoryWindow, error) {
	if s == nil || s.uow == nil || s.store == nil || query.CustomerID < 1 || query.Limit < 1 || query.Limit > 101 || query.Watermark.IsZero() || query.AfterID < 0 {
		return surveyport.CustomerHistoryWindow{}, surveyport.ErrInvalid
	}
	store, ok := s.store.(customerHistoryWindowStore)
	if !ok {
		return surveyport.CustomerHistoryWindow{}, surveyport.ErrUnavailable
	}
	window := surveyport.CustomerHistoryWindow{Items: []surveyport.Submission{}}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		window.Items, e = store.CustomerHistoryWindow(tx, query)
		return e
	})
	return window, classify(err)
}
func (s *SubmissionService) Analytics(ctx context.Context, id surveyport.ID) (surveyport.Analytics, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 {
		return surveyport.Analytics{}, surveyport.ErrInvalid
	}
	var result surveyport.Analytics
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; result, e = s.store.SubmissionAnalytics(tx, id); return e })
	return result, classify(err)
}

// RecordExport persists a non-PII audit/outbox receipt before the HTTP layer
// starts streaming a CSV response. The export itself is a read, so browsers do
// not need to turn a download link into a state-changing CSRF request.
func (s *SubmissionService) RecordExport(ctx context.Context, id surveyport.ID, actor int64, key string) error {
	if s == nil || s.uow == nil || s.store == nil || id < 1 || actor < 1 || len(key) < 16 || len(key) > 200 {
		return surveyport.ErrInvalid
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(map[string]any{"questionnaire_id": id, "format": "csv"})
	err := s.uow.Within(ctx, func(tx context.Context) error {
		return s.store.AppendAuditAndOutbox(tx, "survey_export_requested", id, fmt.Sprint(actor), payload, "survey-export:"+key, now)
	})
	return classify(err)
}

func (s *SubmissionService) ListOperationReceipts(ctx context.Context, id surveyport.ID, limit, offset int32) ([]surveyport.OperationReceipt, int64, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 0 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset {
		return nil, 0, surveyport.ErrInvalid
	}
	var items []surveyport.OperationReceipt
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		items, total, e = s.store.ListOperationReceipts(tx, id, limit, offset)
		return e
	})
	return items, total, classify(err)
}

func (s *SubmissionService) ListLegacyUnresolved(ctx context.Context, questionnaire surveyport.ID, limit, offset int32) ([]surveyport.LegacySubmission, int64, error) {
	if s == nil || s.uow == nil || s.store == nil || questionnaire < 0 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset {
		return nil, 0, surveyport.ErrInvalid
	}
	var items []surveyport.LegacySubmission
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		items, total, e = s.store.ListLegacyUnresolved(tx, questionnaire, limit, offset)
		return e
	})
	return items, total, classify(err)
}
func (s *SubmissionService) GetLegacyUnresolved(ctx context.Context, id surveyport.ID) (surveyport.LegacySubmission, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 {
		return surveyport.LegacySubmission{}, surveyport.ErrNotFound
	}
	var item surveyport.LegacySubmission
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; item, e = s.store.GetLegacyUnresolved(tx, id); return e })
	return item, classify(err)
}
func (s *SubmissionService) ListLegacyAnswers(ctx context.Context, id surveyport.ID, limit, offset int32) ([]surveyport.LegacyAnswer, int64, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset {
		return nil, 0, surveyport.ErrInvalid
	}
	var items []surveyport.LegacyAnswer
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		items, total, e = s.store.ListLegacyAnswers(tx, id, limit, offset)
		return e
	})
	return items, total, classify(err)
}

func (s *SubmissionService) GetOperationConfiguration(ctx context.Context, id surveyport.ID) (surveyport.OperationConfiguration, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 {
		return surveyport.OperationConfiguration{}, surveyport.ErrNotFound
	}
	var value surveyport.OperationConfiguration
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		value, e = s.store.GetOperationConfiguration(tx, id)
		if e == nil && s.completionEndpoints != nil {
			value.ExternalPushURL, e = s.completionEndpoints.ReadSurveyCompletionEndpointWithin(tx, id, value.ExternalPushConfigurationRef)
		}
		return e
	})
	return value, classify(err)
}
func (s *SubmissionService) SaveOperationConfiguration(ctx context.Context, value surveyport.OperationConfiguration, actor int64, key string) (surveyport.OperationConfiguration, error) {
	if len(value.ExternalPushMetadata) == 0 {
		value.ExternalPushMetadata = json.RawMessage(`{}`)
	}
	if len(value.CompletionTarget) == 0 {
		value.CompletionTarget = json.RawMessage(`{}`)
	}
	if !json.Valid(value.CompletionTarget) || len(value.LeadQRTitle) > 40 || len(value.LeadQRSubtitle) > 100 {
		return surveyport.OperationConfiguration{}, surveyport.ErrInvalid
	}
	target, targetErr := parseSurveyCompletionTarget(value.CompletionTarget)
	if targetErr != nil {
		return surveyport.OperationConfiguration{}, surveyport.ErrInvalid
	}
	if target.Enabled {
		value.CompletionNavigationRef = ""
	}
	var metadata map[string]json.RawMessage
	if !json.Valid(value.ExternalPushMetadata) || json.Unmarshal(value.ExternalPushMetadata, &metadata) != nil || metadata == nil {
		return surveyport.OperationConfiguration{}, surveyport.ErrInvalid
	}
	if s == nil || s.uow == nil || s.store == nil || value.QuestionnaireID < 1 || actor < 1 || !validPublicKeyish(key) || !validOpaque(value.CompletionNavigationRef) || !validOpaque(value.ExternalPushConfigurationRef) || value.CompletionChannelID != nil && *value.CompletionChannelID < 1 || value.ExternalPushEnabled && value.ExternalPushURL == "" && value.ExternalPushConfigurationRef == "" {
		return surveyport.OperationConfiguration{}, surveyport.ErrInvalid
	}
	now := s.now().UTC()
	var stored surveyport.OperationConfiguration
	err := s.uow.Within(ctx, func(tx context.Context) error {
		// Lock only the owner lifecycle row. Archive and a new operation-
		// configuration write therefore serialize without requiring a full
		// definition read; retained configuration remains readable afterwards.
		status, e := s.store.LockQuestionnaireStatus(tx, value.QuestionnaireID)
		if e != nil {
			return e
		}
		if status == surveyport.StatusArchived {
			return surveyport.ErrNotFound
		}
		if s.completionEndpoints != nil && (value.ExternalPushURL != "" || strings.HasPrefix(value.ExternalPushConfigurationRef, "survey-endpoint:")) {
			value.ExternalPushConfigurationRef, e = s.completionEndpoints.SaveSurveyCompletionEndpointWithin(tx, value.QuestionnaireID, value.ExternalPushConfigurationRef, value.ExternalPushURL, value.ExternalPushMetadata)
			if e != nil {
				return e
			}
		}
		if value.ExternalPushEnabled && value.ExternalPushConfigurationRef == "" {
			return surveyport.ErrInvalid
		}
		stored, e = s.store.SaveOperationConfiguration(tx, value, actor, now)
		if e != nil {
			return e
		}
		payload, _ := json.Marshal(map[string]any{"questionnaire_id": value.QuestionnaireID, "external_push_enabled": value.ExternalPushEnabled, "configuration_reference": value.ExternalPushConfigurationRef, "completion_configured": value.CompletionNavigationRef != "" || value.CompletionChannelID != nil})
		return s.store.AppendAuditAndOutbox(tx, "survey_operation_configuration_saved", value.QuestionnaireID, fmt.Sprint(actor), payload, "survey-operation-config:"+key, now)
	})
	return stored, classify(err)
}
func (s *SubmissionService) RecordDisabledOperation(ctx context.Context, qid surveyport.ID, sid *surveyport.ID, kind string, actor int64, key string) (surveyport.OperationReceipt, error) {
	if s == nil || s.uow == nil || s.store == nil || qid < 1 || actor < 1 || !validPublicKeyish(key) || kind != "external_push" && kind != "completion" {
		return surveyport.OperationReceipt{}, surveyport.ErrInvalid
	}
	digest := sha256.Sum256([]byte(key))
	now := s.now().UTC()
	var receipt surveyport.OperationReceipt
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		receipt, e = s.store.RecordDisabledOperation(tx, qid, sid, kind, digest, now)
		if e != nil {
			return e
		}
		payload, _ := json.Marshal(map[string]any{"questionnaire_id": qid, "operation_receipt_id": receipt.ID, "status": "disabled"})
		return s.store.AppendAuditAndOutbox(tx, "survey_external_operation_disabled", qid, fmt.Sprint(actor), payload, "survey-disabled:"+key, now)
	})
	return receipt, classify(err)
}

// QueueCompletionTest restores the donor's synthetic questionnaire push. It
// deliberately has no Customer or submission: the immutable snapshot is only
// a safe test body and the effect is accepted in this same transaction.
func (s *SubmissionService) QueueCompletionTest(ctx context.Context, qid surveyport.ID, actor int64, key string) (surveyport.CompletionTestReceipt, error) {
	if s == nil || s.uow == nil || s.store == nil || qid < 1 || actor < 1 || !validPublicKeyish(key) {
		return surveyport.CompletionTestReceipt{}, surveyport.ErrInvalid
	}
	// PostgreSQL persists timestamptz at microsecond precision. Canonicalize
	// before the immutable test snapshot is accepted, because ScheduledAt is
	// part of the External Effects acceptance digest and must survive replay.
	now := s.now().UTC().Truncate(time.Microsecond)
	var receipt surveyport.CompletionTestReceipt
	err := s.uow.Within(ctx, func(tx context.Context) error {
		testRunID := completionTestRunID(key)
		// A prior accepted test is authoritative even if an administrator later
		// changes or disables the current target. Reuse its immutable body and
		// target; never retarget an accepted effect on replay.
		if snapshot, found, err := s.store.GetCompletionTestSnapshot(tx, qid, key); err != nil {
			return err
		} else if found {
			return s.acceptFrozenCompletionTest(tx, qid, actor, snapshot, false, &receipt)
		}
		questionnaire, err := s.store.Get(tx, qid, false)
		if err != nil {
			return err
		}
		// A previously accepted test is replayed from its frozen snapshot above.
		// A new test is a new external-effect intent and must stop once the
		// questionnaire is archived. Draft and disabled configuration tests keep
		// their established pre-publication behavior.
		if questionnaire.Status == surveyport.StatusArchived {
			return surveyport.ErrNotFound
		}
		configuration, err := s.store.GetOperationConfiguration(tx, qid)
		if err != nil {
			return err
		}
		if !configuration.ExternalPushEnabled || configuration.ExternalPushConfigurationRef == "" {
			return surveyport.ErrEffectUnavailable
		}
		if s.completion == nil || s.completionPolicy == nil {
			return surveyport.ErrEffectUnavailable
		}
		policy, found, err := s.completionPolicy.CompletionPolicy(tx, configuration.ExternalPushConfigurationRef)
		if err != nil {
			return err
		}
		if !found {
			return surveyport.ErrEffectUnavailable
		}
		if len(configuration.ExternalPushMetadata) > 0 && json.Unmarshal(configuration.ExternalPushMetadata, &policy) != nil {
			return surveyport.ErrInvalid
		}
		policy.ConfigurationReference = configuration.ExternalPushConfigurationRef
		policy = syntheticTestPolicy(policy)
		intent := completionTestIntent(qid, testRunID, questionnaire.Title, policy)
		snapshot, created, err := s.store.RecordCompletionTestSnapshot(tx, CompletionTestSnapshot{QuestionnaireID: qid, TestRunID: testRunID, QuestionnaireTitle: questionnaire.Title, SubmittedAt: now, Policy: policy, SourceDigest: intent.SourceDigest, TargetDigest: intent.TargetDigest, PayloadDigest: intent.PayloadDigest, PolicyDigest: intent.PolicyDigest, IdempotencyKey: key})
		if err != nil {
			return err
		}
		return s.acceptFrozenCompletionTest(tx, qid, actor, snapshot, created, &receipt)
	})
	return receipt, classify(err)
}

func (s *SubmissionService) acceptFrozenCompletionTest(ctx context.Context, qid surveyport.ID, actor int64, snapshot CompletionTestSnapshot, created bool, receipt *surveyport.CompletionTestReceipt) error {
	if s.completion == nil || receipt == nil {
		return surveyport.ErrEffectUnavailable
	}
	intent := completionTestIntent(snapshot.QuestionnaireID, snapshot.TestRunID, snapshot.QuestionnaireTitle, snapshot.Policy)
	binding, err := s.completion.AcceptCompletionWithin(ctx, intent)
	if err != nil || binding.EffectID == "" || binding.State == "" {
		if err != nil {
			return err
		}
		return surveyport.ErrEffectUnavailable
	}
	keyDigest := sha256.Sum256([]byte(intent.SourceDigest))
	if err = s.store.RecordCompletionTestEffect(ctx, qid, snapshot.TestRunID, snapshot.Policy.ConfigurationReference, binding.EffectID, binding.State, keyDigest, snapshot.SubmittedAt); err != nil {
		return err
	}
	if created {
		payload, _ := json.Marshal(map[string]any{"questionnaire_id": qid, "test_run_id": snapshot.TestRunID, "synthetic_data": true})
		if err = s.store.AppendAuditAndOutbox(ctx, "survey_completion_test_queued", qid, fmt.Sprint(actor), payload, "survey-completion-test:"+snapshot.TestRunID, snapshot.SubmittedAt); err != nil {
			return err
		}
	}
	*receipt = surveyport.CompletionTestReceipt{QuestionnaireID: qid, TestRunID: snapshot.TestRunID, EffectID: binding.EffectID, State: binding.State}
	return nil
}

func completionTestRunID(key string) string {
	digest := sha256.Sum256([]byte("survey.completion.test.run.v1\x00" + key))
	return "questionnaire-test-" + hex.EncodeToString(digest[:16])
}

func completionTestIntent(questionnaireID surveyport.ID, testRunID, title string, policy surveyport.CompletionPolicy) surveyport.CompletionIntent {
	policyBytes, _ := json.Marshal(policy)
	source := surveyDigest("survey.completion.test.source.v1", fmt.Sprint(questionnaireID), testRunID)
	return surveyport.CompletionIntent{QuestionnaireID: questionnaireID, TestRunID: testRunID, ConfigurationReference: policy.ConfigurationReference,
		SourceDigest: source, TargetDigest: surveyDigest("survey.completion.target.v1", policy.ConfigurationReference),
		PayloadDigest: surveyDigest("survey.completion.test.payload.v1", testRunID, title, policy.ConfigurationDigest, string(policyBytes)),
		PolicyDigest:  surveyDigest("survey.completion.test.policy.v1", policy.ConfigurationDigest),
		// SubmittedAt is frozen in the Survey snapshot and request body. An
		// operator test itself is immediately eligible, so it must not turn that
		// timestamp into a River delay.
		IdempotencyKey: "survey.completion.test:" + testRunID}
}

func syntheticTestPolicy(policy surveyport.CompletionPolicy) surveyport.CompletionPolicy {
	filtered := make(map[string]string, len(policy.CustomParams))
	for key, value := range policy.CustomParams {
		if safeSyntheticTestParameter(key) {
			filtered[key] = value
		}
	}
	policy.CustomParams = filtered
	return policy
}

func safeSyntheticTestParameter(key string) bool {
	switch key {
	case "", "user_id", "questionnaire_title", "submitted_at", "answers", "phone_number", "type", "expires_at_ts", "day", "frequency", "remark", "assessment_result_snapshot", "is_test", "test_run_id":
		return false
	}
	if len(key) > 128 {
		return false
	}
	normalized := strings.ToLower(key)
	for _, fragment := range []string{"phone", "mobile", "openid", "unionid", "external_user", "respondent", "identity", "customer"} {
		if strings.Contains(normalized, fragment) {
			return false
		}
	}
	return true
}

func validOpaque(v string) bool {
	if len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r)) {
			return false
		}
	}
	return true
}
func validPublicKeyish(v string) bool { return len(v) >= 16 && len(v) <= 200 }

func validSlug(v string) bool {
	return len(v) > 0 && len(v) <= 128 && strings.Trim(v, "abcdefghijklmnopqrstuvwxyz0123456789-") == "" && v[0] != '-' && v[len(v)-1] != '-'
}
func validPublicKey(v string) bool {
	if len(v) != 43 {
		return false
	}
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func validIdentity(v surveyport.SubmissionIdentity) bool {
	if !validIdentityState(v.State) {
		return false
	}
	if v.State == surveyport.IdentityResolved {
		return v.CustomerID != nil && *v.CustomerID > 0 && len(v.EvidenceDigest) == 64
	}
	return v.CustomerID == nil && len(v.EvidenceDigest) <= 64
}
func validIdentityState(v surveyport.IdentityState) bool {
	return v == surveyport.IdentityAnonymous || v == surveyport.IdentityResolved || v == surveyport.IdentityUnresolved || v == surveyport.IdentityConflict
}
func maskMobile(v string) string {
	r := []rune(v)
	if len(r) < 7 {
		return "***"
	}
	return string(r[:3]) + "****" + string(r[len(r)-4:])
}

var _ surveyport.PublicApplication = (*SubmissionService)(nil)
var _ surveyport.SubmissionApplication = (*SubmissionService)(nil)

var _ surveyport.ExternalSubmissionReader = (*SubmissionService)(nil)

func (s *SubmissionService) BindSubmissionObserver(observer surveyport.SubmissionObserver) {
	s.observer = observer
}
