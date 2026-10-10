package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type financialInvitationSubjects struct {
	retired bool
	root    int64
}

func (s *financialInvitationSubjects) VerifiedIdentitySubject(ctx context.Context, id int64) (identityport.VerifiedIdentitySubject, bool, error) {
	return s.WriteLockedVerifiedIdentitySubject(ctx, id)
}
func (s *financialInvitationSubjects) WriteLockedVerifiedIdentitySubject(_ context.Context, id int64) (identityport.VerifiedIdentitySubject, bool, error) {
	if s.retired || id != 10011 && id != 11011 {
		return identityport.VerifiedIdentitySubject{}, false, nil
	}
	return identityport.VerifiedIdentitySubject{CustomerID: customerdomain.CustomerID(s.root), Lineage: []customerdomain.CustomerID{customerdomain.CustomerID(s.root)}}, true, nil
}

// Real Referral UoW/receipts/store, with a controllable Identity port for
// retirement and incorrect-owner rejection. Real OneID is covered by checkout.
func TestPostgreSQLPaidInvitationFinancialIssuerPreservesLegacyAndRejectsRetiredOwner(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	ctx := context.Background()
	c, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "financial issuer", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 51, ProductType: "standard_product", LeaderboardMetric: referraldomain.LeaderboardInvites, IdempotencyKey: "financial-issuer-campaign"})
	if err != nil {
		t.Fatal(err)
	}
	c, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: c.ID, ExpectedVersion: c.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "financial-issuer-active"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.uow.Within(ctx, func(tx context.Context) error {
		_, e := h.repository.InsertParticipationWithin(tx, referraldomain.Participation{CampaignID: c.ID, CustomerID: 11, State: referraldomain.ParticipationActive, JoinedAt: h.clock.Add(-time.Minute)})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	legacy := h.issue(t, c.ID, 11, "legacy-financial-issuer")
	subjects := &financialInvitationSubjects{root: 11}
	if err = h.service.BindIdentitySubjects(subjects); err != nil {
		t.Fatal(err)
	}
	actor := referralActor(11, h.clock)
	command := referralport.IssueInvitationCommand{Actor: actor, CampaignID: c.ID, IdempotencyKey: "actual-financial-issuer"}
	link, err := h.service.IssueInvitation(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(link.URL[len("https://referral.test/referral/invite/"):]))
	read := func(d [32]byte, product int64, at time.Time) (referralport.PaidInvitationCheckout, error) {
		var result referralport.PaidInvitationCheckout
		e := h.uow.Within(ctx, func(tx context.Context) error {
			var e error
			result, e = h.service.ReadPaidInvitationCheckoutWithin(tx, d, product, "standard_product", at)
			return e
		})
		return result, e
	}
	got, err := read(digest, 51, h.clock)
	if err != nil || got.Inviter.IdentityID != actor.IdentityID || got.Inviter.CustomerID != 11 || got.Inviter.AppID != actor.AppID || got.CampaignID != c.ID {
		t.Fatalf("actual issuer=%+v err=%v", got, err)
	}
	// Receipt replay through another handle of this same subject cannot replace
	// the original receiver authority or add another invitation.
	command.Actor.IdentityID = 11011
	replay, err := h.service.IssueInvitation(ctx, command)
	if err != nil || replay != link {
		t.Fatalf("same-subject replay: %+v %v", replay, err)
	}
	got, err = read(digest, 51, h.clock)
	if err != nil || got.Inviter.IdentityID != actor.IdentityID {
		t.Fatalf("issuer snapshot rewritten: %+v %v", got, err)
	}
	for _, bad := range []struct {
		digest  [32]byte
		product int64
		at      time.Time
	}{{sha256.Sum256([]byte(legacy)), 51, h.clock}, {digest, 52, h.clock}, {digest, 51, c.EndsAt}} {
		if _, err = read(bad.digest, bad.product, bad.at); !errors.Is(err, referralport.ErrNotFound) {
			t.Fatalf("invalid financial authority err=%v", err)
		}
	}
	subjects.retired = true
	if _, err = read(digest, 51, h.clock); !errors.Is(err, referralport.ErrNotFound) {
		t.Fatalf("retired issuer err=%v", err)
	}
	subjects.retired = false
	subjects.root = 99
	if _, err = read(digest, 51, h.clock); !errors.Is(err, referralport.ErrUnavailable) {
		t.Fatalf("incorrect-owner issuer err=%v", err)
	}
	if _, err = h.service.ReadPaidInvitationCheckoutWithin(ctx, digest, 51, "standard_product", h.clock); !errors.Is(err, referralport.ErrUnavailable) {
		t.Fatalf("non-atomic financial read err=%v", err)
	}
}
