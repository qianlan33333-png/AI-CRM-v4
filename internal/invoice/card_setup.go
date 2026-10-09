package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

func (s *Service) cardWithin(ctx context.Context) (p.CardSetup, error) {
	tx, e := pg.RequireTransaction(ctx)
	if e != nil {
		return p.CardSetup{}, e
	}
	var out p.CardSetup
	if s.Configuration != nil {
		snapshot, err := s.Configuration.EffectiveSnapshotWithin(ctx)
		if err != nil {
			return out, err
		}
		out.ConfigurationApplied = snapshot.Checksum == s.AppliedConfigurationChecksum
	}
	e = tx.QueryRow(ctx, `SELECT effect_id,card_id,failure_code,material->>'logo_url' FROM invoice_card_setup_intents WHERE merchant_id=$1 ORDER BY id DESC LIMIT 1`, s.Config.MerchantID).Scan(&out.EffectID, &out.CardID, &out.FailureCode, &out.LogoURL)
	if errors.Is(e, pgx.ErrNoRows) {
		out.State = "not_submitted"
		return out, nil
	}
	if e != nil {
		return out, e
	}
	state, e := s.EffectStates.GetWithin(ctx, out.EffectID)
	if e != nil {
		return out, e
	}
	out.State = string(state.State)
	return out, nil
}
func (s *Service) ReadCardSetup(ctx context.Context) (out p.CardSetup, e error) {
	e = s.UoW.Within(ctx, func(tx context.Context) error { var err error; out, err = s.cardWithin(tx); return err })
	return
}
func (s *Service) AcceptCardSetup(ctx context.Context, actor int64) (out p.CardSetup, e error) {
	if actor < 1 || !s.CardConfig.Valid() {
		return out, p.ErrInvalid
	}
	if !s.Config.Enabled || s.Provider == nil {
		return out, p.ErrDisabled
	}
	raw, _ := json.Marshal(s.CardConfig)
	digest := effects.Hash("invoice.card.payload", string(raw))
	e = s.UoW.Within(ctx, func(ctx context.Context) error {
		tx, err := pg.RequireTransaction(ctx)
		if err != nil {
			return err
		}
		// All template versions share the same merchant lock; in-flight/unknown
		// writes must finish before another payload can overwrite the official card.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "invoice.card:"+s.Config.MerchantID); err != nil {
			return err
		}
		current, err := s.cardWithin(ctx)
		if err != nil {
			return err
		}
		if !current.ConfigurationApplied {
			return p.ErrConflict
		}
		if current.EffectID != "" {
			var priorDigest string
			if err = tx.QueryRow(ctx, `SELECT payload_digest FROM invoice_card_setup_intents WHERE effect_id=$1`, current.EffectID).Scan(&priorDigest); err != nil {
				return err
			}
			if current.State == string(effects.StateUnknown) {
				return p.ErrPending
			}
			// A verified input/authentication refusal made no template change. An explicit admin
			// resubmission may try again after fixing credentials/authorization.
			// Accepted, executed and unknown writes always retain their receipt.
			if priorDigest == string(digest) && current.State != "final_failed" {
				out = current
				return nil
			}
			if current.State != "executed" && current.State != "final_failed" && current.State != "cancelled" {
				return p.ErrPending
			}
		}
		envelope := effects.Envelope{Owner: effects.OwnerInvoice, Kind: effects.KindInvoiceCardConfigure, SourceRefDigest: effects.Hash("invoice.card.intent", fmt.Sprint(time.Now().UnixNano()), s.Config.MerchantID), TargetRefDigest: effects.Hash("invoice.card.merchant", s.Config.MerchantID), PayloadDigest: digest, PolicyVersionHash: effects.Hash("invoice.card.policy", "v1")}
		projection, _, err := s.Effects.AcceptAndQueueWithin(ctx, effects.AcceptCommand{ReceiptKey: effects.Hash("invoice.card.accept", string(envelope.SourceRefDigest)), Envelope: envelope})
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO invoice_card_setup_intents(merchant_id,effect_id,payload_digest,material,actor_id) VALUES($1,$2,$3,$4,$5)`, s.Config.MerchantID, projection.ID, string(digest), raw, actor); err != nil {
			return err
		}
		out = p.CardSetup{ConfigurationApplied: true, EffectID: projection.ID, State: string(projection.State), LogoURL: s.CardConfig.LogoURL}
		return nil
	})
	return
}
func (s *Service) PrepareCardEffect(ctx context.Context, id string) (out p.CardConfig, e error) {
	e = s.UoW.Within(ctx, func(ctx context.Context) error {
		tx, err := pg.RequireTransaction(ctx)
		if err != nil {
			return err
		}
		var raw []byte
		var digest string
		if err = tx.QueryRow(ctx, `SELECT material,payload_digest FROM invoice_card_setup_intents WHERE effect_id=$1`, id).Scan(&raw, &digest); err != nil {
			return err
		}
		if json.Unmarshal(raw, &out) != nil || !out.Valid() || out.MerchantID != s.Config.MerchantID || out.AppID != s.Config.AppID || !cardDigestMatches(out, digest) {
			return p.ErrInvalid
		}
		return nil
	})
	return
}
func (s *Service) completeCardSetup(ctx context.Context, id string, result effects.AdapterResult) error {
	tx, e := pg.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	cardID := ""
	if result.Completion == effects.StateExecuted {
		var artifact struct {
			CardID string `json:"card_id"`
		}
		if result.Artifact.Kind != "invoice_card_template_v1" || json.Unmarshal(result.Artifact.Payload, &artifact) != nil || artifact.CardID == "" {
			return p.ErrInvalid
		}
		cardID = artifact.CardID
	}
	_, e = tx.Exec(ctx, `UPDATE invoice_card_setup_intents SET card_id=$2,failure_code=$3 WHERE effect_id=$1`, id, cardID, result.FailureCode)
	return e
}

func cardDigestMatches(c p.CardConfig, d string) bool {
	raw, _ := json.Marshal(c)
	return d == string(effects.Hash("invoice.card.payload", string(raw)))
}
