package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	e "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	m "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type InvitationCodeProvider struct {
	Store    m.InvitationCodeStore
	Provider w.InvitationCodeProvider
	Enabled  bool
}

func (p *InvitationCodeProvider) Execute(ctx context.Context, env e.Envelope, a e.Attempt) (e.AdapterResult, error) {
	fail := e.AdapterResult{Completion: e.StateFinalFailed, ReceiptDigest: e.Hash("invitation.code.invalid")}
	if p == nil || !p.Enabled || p.Provider == nil || p.Store == nil || env.Kind != e.KindInvitationCode || !env.Valid() {
		return fail, nil
	}
	if planStore, ok := p.Store.(m.InvitationPlanCodeStore); ok && env.PolicyVersionHash == e.Hash("invitation.code.policy.v2") {
		intent, err := planStore.ReadInvitationPlanCodeIntent(ctx, string(env.SourceRefDigest))
		if err != nil {
			return e.AdapterResult{Completion: e.StateRetryable, ReceiptDigest: e.Hash("invitation.plan-code.read")}, err
		}
		if intent.EffectID != a.EffectID || env.TargetRefDigest != e.Hash("invitation.plan.target.v2", fmt.Sprint(intent.InviteID)) || env.PayloadDigest != e.Hash("invitation.plan-code.v2", string(mustJSON(intent.ChatIDs))) {
			return fail, nil
		}
		planner, ok := p.Provider.(w.InvitationPlanCodeProvider)
		if !ok {
			return fail, errors.New("invitation plan provider unavailable")
		}
		var code w.InvitationCode
		if intent.ConfigID == "" {
			code, err = planner.CreateInvitationCodeForGroups(ctx, intent.ChatIDs)
		} else {
			code, err = planner.UpdateInvitationCodeForGroups(ctx, intent.ConfigID, intent.ChatIDs)
		}
		return p.finishPlan(code, err, env, planStore)
	}
	intent, err := p.Store.ReadInvitationCodeIntent(ctx, string(env.SourceRefDigest))
	if err != nil {
		return e.AdapterResult{Completion: e.StateRetryable, ReceiptDigest: e.Hash("invitation.code.read")}, err
	}
	if intent.EffectID != a.EffectID || env.TargetRefDigest != e.Hash("invitation.target.v1", intent.ChatID) || env.PayloadDigest != e.Hash("invitation.single-code.v1", intent.ChatID, "auto_create_room=0") || env.PolicyVersionHash != e.Hash("invitation.code.policy.v1") {
		return fail, nil
	}
	code, err := p.Provider.CreateInvitationCode(ctx, intent.ChatID)
	raw, _ := json.Marshal(code)
	artifact := e.ResultArtifact{}
	if code.ConfigID != "" {
		artifact = e.ResultArtifact{Kind: "invitation.code.v1", Payload: raw, Digest: e.Hash("external-effect.artifact.v1", "invitation.code.v1", string(raw))}
	}
	if err != nil {
		state := e.StateRetryable
		attempted := w.ProviderCallAttempted(err)
		if attempted || !w.ProviderWriteClassified(err) {
			state = e.StateUnknown
		}
		return e.AdapterResult{Completion: state, ReceiptDigest: e.Hash("invitation.code.provider-error", string(env.Fingerprint())), CallAttempted: attempted, RealExternalCallExecuted: attempted, Artifact: artifact}, err
	}
	return e.AdapterResult{Completion: e.StateExecuted, ReceiptDigest: e.Hash("invitation.code.executed", string(env.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: true, Artifact: e.ResultArtifact{Kind: "invitation.code.v1", Payload: raw, Digest: e.Hash("external-effect.artifact.v1", "invitation.code.v1", string(raw))}}, nil
}

func mustJSON(v []string) []byte { b, _ := json.Marshal(v); return b }
func (p *InvitationCodeProvider) finishPlan(code w.InvitationCode, err error, env e.Envelope, store m.InvitationPlanCodeStore) (e.AdapterResult, error) {
	raw, _ := json.Marshal(code)
	artifact := e.ResultArtifact{}
	if code.ConfigID != "" {
		artifact = e.ResultArtifact{Kind: "invitation.code.v1", Payload: raw, Digest: e.Hash("external-effect.artifact.v1", "invitation.code.v1", string(raw))}
	}
	if err != nil {
		state := e.StateRetryable
		attempted := w.ProviderCallAttempted(err)
		if attempted || !w.ProviderWriteClassified(err) {
			state = e.StateUnknown
		}
		return e.AdapterResult{Completion: state, ReceiptDigest: e.Hash("invitation.plan-code.provider-error", string(env.Fingerprint())), CallAttempted: attempted, RealExternalCallExecuted: attempted, Artifact: artifact}, err
	}
	return e.AdapterResult{Completion: e.StateExecuted, ReceiptDigest: e.Hash("invitation.plan-code.executed", string(env.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

type InvitationCodeCompletionSink struct{ Store m.InvitationCodeStore }

func (s InvitationCodeCompletionSink) CompleteEffect(ctx context.Context, ref string, env e.Envelope, _ e.Attempt, result e.AdapterResult) error {
	if env.Kind != e.KindInvitationCode || s.Store == nil {
		return errors.New("invalid invitation completion")
	}
	v := m.InvitationCodeCompletion{EffectID: ref, State: string(result.Completion)}
	if result.Completion == e.StateExecuted || result.Completion == e.StateReconciled || result.Artifact.Kind == "invitation.code.v1" {
		var code w.InvitationCode
		if !result.Artifact.Valid() || result.Artifact.Kind != "invitation.code.v1" || json.Unmarshal(result.Artifact.Payload, &code) != nil || code.ConfigID == "" || ((result.Completion == e.StateExecuted || result.Completion == e.StateReconciled) && code.QRCode == "") {
			return errors.New("missing invitation artifact")
		}
		v.ConfigID = code.ConfigID
		v.QRCode = code.QRCode
	}
	if env.PolicyVersionHash == e.Hash("invitation.code.policy.v2") {
		if ps, ok := s.Store.(m.InvitationPlanCodeStore); ok {
			return ps.CompleteInvitationPlanCode(ctx, v)
		}
	}
	return s.Store.CompleteInvitationCode(ctx, v)
}
