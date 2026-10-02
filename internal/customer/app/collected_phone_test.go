package app

import (
	"context"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type collectedPhoneStub struct {
	result  identityport.DeclaredAttachResult
	err     error
	command identityport.DeclaredPhoneCommand
}

func (s *collectedPhoneStub) AttachDeclaredPhoneToCustomer(_ context.Context, c identityport.DeclaredPhoneCommand) (identityport.DeclaredAttachResult, error) {
	s.command = c
	return s.result, s.err
}

type collectedProjectionStub struct {
	calls          int
	historical     bool
	masked, source string
	err            error
}

func (p *collectedProjectionStub) ProjectCollectedPhone(_ context.Context, _ customerdomain.CustomerID, masked, source string, _ time.Time, historical bool) error {
	p.calls++
	p.masked = masked
	p.source = source
	p.historical = historical
	return p.err
}

func TestCollectedPhoneProjectsOnlyNewSuccessfulAssociation(t *testing.T) {
	for _, tc := range []struct {
		status, replay identityport.DeclaredAttachStatus
		wantCalls      int
	}{
		{identityport.DeclaredAttached, "", 1}, {identityport.DeclaredAlreadyLinked, "", 1},
		{identityport.DeclaredConflict, "", 0}, {identityport.DeclaredInvalid, "", 0},
		{identityport.DeclaredReplayed, identityport.DeclaredConflict, 0}, {identityport.DeclaredReplayed, identityport.DeclaredAttached, 0},
	} {
		t.Run(string(tc.status)+string(tc.replay), func(t *testing.T) {
			phones := &collectedPhoneStub{result: identityport.DeclaredAttachResult{Status: tc.status, ReplayOf: tc.replay, CustomerID: 7, IdentityID: 8}}
			projection := &collectedProjectionStub{}
			s, err := NewCollectedPhoneService(phones, projection)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.CollectPhoneWithin(context.Background(), customerport.CollectedPhoneCommand{DeclaredPhoneCommand: identityport.DeclaredPhoneCommand{CustomerID: 7, Phone: "+8613812345678", Source: "order", SourceEventID: "order:9:contact", IdempotencyKey: "order-phone:9"}, CollectedAt: time.Now(), Historical: true})
			if err != nil || result.Status != tc.status || result.ReplayOf != tc.replay || projection.calls != tc.wantCalls || phones.command.Phone != "13812345678" {
				t.Fatalf("status=%s replay=%s calls=%d err=%v", result.Status, result.ReplayOf, projection.calls, err)
			}
			if tc.wantCalls > 0 && (projection.masked != "138****5678" || !projection.historical || projection.source != "order") {
				t.Fatal("wrong projection contract")
			}
		})
	}
}
func TestCollectedPhoneFailsOnProjectionPersistenceOrWrongOwnership(t *testing.T) {
	fail := errors.New("projection failed")
	for _, tc := range []struct {
		owner customerdomain.CustomerID
		want  error
	}{{7, fail}, {9, ErrInvalidQuery}} {
		phones := &collectedPhoneStub{result: identityport.DeclaredAttachResult{Status: identityport.DeclaredAttached, CustomerID: tc.owner, IdentityID: 8}}
		s, _ := NewCollectedPhoneService(phones, &collectedProjectionStub{err: fail})
		_, err := s.CollectPhoneWithin(context.Background(), customerport.CollectedPhoneCommand{DeclaredPhoneCommand: identityport.DeclaredPhoneCommand{CustomerID: 7, Phone: "13812345678", Source: "survey"}, CollectedAt: time.Now()})
		if !errors.Is(err, tc.want) {
			t.Fatalf("expected transaction failure, err=%v", err)
		}
	}
}
