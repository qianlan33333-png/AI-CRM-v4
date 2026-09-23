package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"strings"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

// surveyCompletionEffectAccepter is the composition boundary between Survey's
// opaque completion intent and External Effects. It has no Survey table or
// Provider dependency, and joins the active PostgreSQL transaction.
type surveyCompletionEffectAccepter struct {
	effects effectport.TransactionalAccepter
}

func (a surveyCompletionEffectAccepter) AcceptCompletionWithin(ctx context.Context, in surveyport.CompletionIntent) (surveyport.EffectBinding, error) {
	isSubmission := in.SubmissionID > 0 && in.TestRunID == ""
	isSyntheticTest := in.SubmissionID == 0 && strings.HasPrefix(in.TestRunID, "questionnaire-test-") && len(in.TestRunID) == len("questionnaire-test-")+32
	if a.effects == nil || in.QuestionnaireID < 1 || (!isSubmission && !isSyntheticTest) || in.ConfigurationReference == "" || in.IdempotencyKey == "" ||
		!effectport.ValidDigest(effectport.Digest(in.SourceDigest)) || !effectport.ValidDigest(effectport.Digest(in.TargetDigest)) ||
		!effectport.ValidDigest(effectport.Digest(in.PayloadDigest)) || !effectport.ValidDigest(effectport.Digest(in.PolicyDigest)) {
		return surveyport.EffectBinding{}, errors.New("invalid survey completion intent")
	}
	receiptScope := "survey.completion.accept.v1"
	if isSyntheticTest {
		receiptScope = "survey.completion.test.accept.v1"
	}
	projection, receipt, err := a.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{
		ReceiptKey: effectport.Hash(receiptScope, in.IdempotencyKey),
		Envelope: effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindSurveyCompletion,
			SourceRefDigest: effectport.Digest(in.SourceDigest), TargetRefDigest: effectport.Digest(in.TargetDigest), PayloadDigest: effectport.Digest(in.PayloadDigest), PolicyVersionHash: effectport.Digest(in.PolicyDigest)},
		ScheduledAt: in.ScheduledAt,
	})
	if err != nil || projection.ID == "" || receipt.QueueReceiptID == "" || projection.State == "" {
		if err != nil {
			return surveyport.EffectBinding{}, err
		}
		return surveyport.EffectBinding{}, errors.New("incomplete survey completion acceptance")
	}
	return surveyport.EffectBinding{EffectID: projection.ID, State: string(projection.State)}, nil
}

var _ surveyport.CompletionIntentAccepter = surveyCompletionEffectAccepter{}

type surveyCompletionTargetConfig struct {
	Endpoint      string              `json:"endpoint"`
	SigningKey    string              `json:"signing_key"`
	ClientID      string              `json:"client_id"`
	Version       string              `json:"version"`
	IdentityKind  identitydomain.Kind `json:"identity_kind"`
	IdentityScope string              `json:"identity_scope"`
	Day           *int64              `json:"day"`
	Frequency     *int64              `json:"frequency"`
	ExpiresAtTS   *int64              `json:"expires_at_ts"`
	PushType      string              `json:"type"`
	Remark        string              `json:"remark"`
	CustomParams  map[string]string   `json:"custom_params"`
}

// surveyCompletionTargets parses composition-only deployment configuration.
// The keys are the immutable opaque references stored in Survey, forming the
// target whitelist; signing keys remain local to the outbound adapter.
func surveyCompletionTargets(raw string) ([]outbound.SurveyCompletionTarget, error) {
	if raw == "" {
		return nil, nil
	}
	var values map[string]surveyCompletionTargetConfig
	if err := json.Unmarshal([]byte(raw), &values); err != nil || len(values) == 0 || len(values) > 100 {
		return nil, errors.New("invalid survey completion target configuration")
	}
	targets := make([]outbound.SurveyCompletionTarget, 0, len(values))
	for reference, value := range values {
		key, err := base64.RawStdEncoding.DecodeString(value.SigningKey)
		if err != nil {
			return nil, errors.New("invalid survey completion signing key")
		}
		targets = append(targets, outbound.SurveyCompletionTarget{Reference: reference, Endpoint: value.Endpoint, SigningKey: key, ClientID: value.ClientID, Version: value.Version, IdentityKind: value.IdentityKind, IdentityScope: value.IdentityScope, Day: value.Day, Frequency: value.Frequency, ExpiresAtTS: value.ExpiresAtTS, PushType: value.PushType, Remark: value.Remark, CustomParams: value.CustomParams})
	}
	return targets, nil
}
