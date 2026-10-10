package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

var _ referralport.PaidInvitationCheckoutReader = (*Service)(nil)

// ReadPaidInvitationCheckoutWithin does not infer the issuer of an old link.
// Payment independently verifies the exact scoped identity before accepting
// AddReceiver; the browser cannot provide or replace these stored fields.
func (s *Service) ReadPaidInvitationCheckoutWithin(ctx context.Context, digest [sha256.Size]byte, productID int64, productType string, at time.Time) (referralport.PaidInvitationCheckout, error) {
	if s == nil || s.store == nil || s.subjects == nil || digest == ([sha256.Size]byte{}) || productID < 1 || (productType != "standard_product" && productType != "service_period") || at.IsZero() {
		return referralport.PaidInvitationCheckout{}, referralport.ErrUnavailable
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return referralport.PaidInvitationCheckout{}, referralport.ErrUnavailable
	}
	invitation, err := s.store.ReadInvitationByDigestWithin(ctx, digest, true)
	if err != nil {
		return referralport.PaidInvitationCheckout{}, err
	}
	if invitation.IssuerIdentityID < 1 || !invitation.Valid() || invitation.State != referraldomain.InvitationActive || invitation.CreatedAt.After(at) || !invitation.ExpiresAt.After(at) {
		return referralport.PaidInvitationCheckout{}, referralport.ErrNotFound
	}
	campaign, err := s.store.ReadCampaignWithin(ctx, invitation.CampaignID, true)
	if err != nil {
		return referralport.PaidInvitationCheckout{}, err
	}
	config := campaign.Config()
	if !campaign.AcceptingAt(at) || config.QualificationMode != referraldomain.QualificationProductPurchase || config.ProductID != productID || config.ProductType != productType {
		return referralport.PaidInvitationCheckout{}, referralport.ErrNotFound
	}
	issuer := referralport.TrustedSessionActor{CustomerID: invitation.InviterCustomerID, IdentityID: invitation.IssuerIdentityID, AppID: invitation.IssuerAppID, AppScope: invitation.IssuerAppScope, Channel: invitation.IssuerChannel, OccurredAt: invitation.CreatedAt}
	if !issuer.Valid() {
		return referralport.PaidInvitationCheckout{}, referralport.ErrUnavailable
	}
	lineage, err := s.actorLineageWithin(ctx, issuer, true)
	if err != nil {
		if errors.Is(err, referralport.ErrUnauthorized) {
			return referralport.PaidInvitationCheckout{}, referralport.ErrNotFound
		}
		return referralport.PaidInvitationCheckout{}, err
	}
	if len(lineage) == 0 || !slices.Contains(lineage, invitation.InviterCustomerID) {
		return referralport.PaidInvitationCheckout{}, referralport.ErrUnavailable
	}
	participation, err := s.ownParticipationWithin(ctx, campaign.ID, lineage, true)
	if errors.Is(err, referralport.ErrNotFound) || err == nil && (participation.State != referraldomain.ParticipationActive || participation.JoinedAt.After(at)) {
		return referralport.PaidInvitationCheckout{}, referralport.ErrNotFound
	}
	if err != nil {
		return referralport.PaidInvitationCheckout{}, err
	}
	issuer.CustomerID = lineage[0]
	return referralport.PaidInvitationCheckout{CampaignID: campaign.ID, InvitationID: invitation.ID, ProductID: productID, ProductType: productType, Inviter: issuer, ExpiresAt: invitation.ExpiresAt}, nil
}
