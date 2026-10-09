package provider

import (
	"context"
	"encoding/json"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
)

func (w *WeChat) configureCardEffect(ctx context.Context, envelope effects.Envelope, attempt effects.Attempt) (effects.AdapterResult, error) {
	if w.Client == nil || w.Verifier == nil {
		return effects.AdapterResult{}, p.ErrDisabled
	}
	source, ok := w.Material.(p.CardEffectMaterial)
	if !ok {
		return effects.AdapterResult{}, p.ErrDisabled
	}
	material, e := source.PrepareCardEffect(ctx, attempt.EffectID)
	if e != nil {
		return effects.AdapterResult{}, e
	}
	raw, _ := json.Marshal(material)
	if !material.Valid() || envelope.PayloadDigest != effects.Hash("invoice.card.payload", string(raw)) {
		return effects.AdapterResult{}, p.ErrInvalid
	}
	card := CardTemplate{SubMerchant: material.MerchantID, AppID: material.AppID}
	card.Information.LogoURL = material.LogoURL
	card.Information.PayeeName = material.PayeeName
	card.Information.CustomCell.Words = "电子发票"
	card.Information.CustomCell.Description = "查看订单"
	card.Information.CustomCell.JumpURL = material.JumpURL
	receipt := (SetupClient{Client: w.Client, Verifier: w.Verifier}).ConfigureCard(ctx, card)
	result := effects.AdapterResult{Completion: effects.StateUnknown, CallAttempted: true, ReceiptDigest: effects.Hash("invoice.card.response", attempt.EffectID, receipt.Outcome, string(receipt.Body)), FailureCode: "card_result_unknown"}
	if receipt.SignatureVerified && receipt.Outcome == "rejected" {
		result.Completion = effects.StateFinalFailed
		result.FailureCode = "card_configuration_rejected"
		return result, nil
	}
	if receipt.SignatureVerified && receipt.Outcome == "confirmed" {
		var response struct {
			CardID string `json:"card_id"`
			AppID  string `json:"card_appid"`
		}
		if json.Unmarshal(receipt.Body, &response) == nil && response.CardID != "" && response.AppID == material.AppID {
			payload, _ := json.Marshal(response)
			result.Completion = effects.StateExecuted
			result.RealExternalCallExecuted = true
			result.FailureCode = ""
			result.Artifact = effects.ResultArtifact{Kind: "invoice_card_template_v1", Payload: payload, Digest: effects.Hash("external-effect.artifact.v1", "invoice_card_template_v1", string(payload))}
		}
	}
	// No official template GET exists. Unknown results remain blocked and never
	// automatically repeat this overwrite call.
	return result, nil
}
