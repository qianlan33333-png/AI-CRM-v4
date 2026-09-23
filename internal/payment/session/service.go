package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"time"
)

var ErrInvalid = errors.New("invalid payment session")
var ErrIdentityConflict = errors.New("payment OAuth identities require review")
var ErrExpired = errors.New("payment session expired")
var ErrConsumed = errors.New("payment session consumed")

type Record struct {
	UnionIDVerified                        bool
	ID                                     int64
	TokenDigest                            [32]byte
	PayerIdentityID                        int64
	PayerCustomerID, BeneficiaryCustomerID customerdomain.CustomerID
	BeneficiarySelection                   paymentport.BeneficiarySelection
	BeneficiarySelectedAt                  *time.Time
	AppScopeDigest                         [32]byte
	Channel                                paymentdomain.Channel
	ExpiresAt                              time.Time
	ConsumedAt                             *time.Time
	CreatedAt                              time.Time
}
type Store interface {
	Insert(context.Context, Record) (Record, error)
	Consume(context.Context, [32]byte, time.Time) (Record, error)
	Lookup(context.Context, [32]byte, time.Time) (Record, error)
	SelectPayerSelf(context.Context, [32]byte, time.Time) (Record, error)
}
type IssueCommand struct {
	UnionID               identitydomain.VerifiedFact
	Fact                  identitydomain.VerifiedFact
	BeneficiaryCustomerID customerdomain.CustomerID
	AdminAssisted         bool
	DisplayName           string
	AvatarURL             string
	IdempotencyKey        string
}
type Issued struct {
	Token                                  string
	ExpiresAt                              time.Time
	PayerIdentityID                        int64
	PayerCustomerID, BeneficiaryCustomerID customerdomain.CustomerID
	BeneficiarySelection                   paymentport.BeneficiarySelection
	Channel                                paymentdomain.Channel
}
type Service struct {
	uow       platformport.UnitOfWork
	provision identityport.VerifiedProvisioner
	profiles  customerport.ProviderProfileWriter
	store     Store
	ttl       time.Duration
	now       func() time.Time
}

func NewService(uow platformport.UnitOfWork, p identityport.VerifiedProvisioner, profiles customerport.ProviderProfileWriter, s Store, ttl time.Duration) (*Service, error) {
	if uow == nil || p == nil || profiles == nil || s == nil || ttl < time.Minute || ttl > 30*time.Minute {
		return nil, ErrInvalid
	}
	return &Service{uow: uow, provision: p, profiles: profiles, store: s, ttl: ttl, now: time.Now}, nil
}
func (s *Service) IssueTrusted(ctx context.Context, c IssueCommand) (Issued, error) {
	if s == nil || !c.Fact.Valid() || len(c.IdempotencyKey) < 16 {
		return Issued{}, ErrInvalid
	}
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		return Issued{}, e
	}
	token := "pays_" + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	ref := c.Fact.Reference()
	channel := paymentdomain.ChannelMiniProgram
	if ref.Kind == identitydomain.KindOAOpenID {
		channel = paymentdomain.ChannelH5Official
	} else if ref.Kind != identitydomain.KindMPOpenID {
		return Issued{}, ErrInvalid
	}
	scopeDigest := sha256.Sum256([]byte(string(ref.Kind) + "\x00" + ref.Scope))
	now := s.now().UTC()
	var out Issued
	var identityConflict bool
	e := s.uow.Within(ctx, func(tx context.Context) error {
		var p identityport.ProvisionResult
		var e error
		if channel == paymentdomain.ChannelH5Official {
			provisioner, ok := s.provision.(identityport.VerifiedOAuthSubjectProvisioner)
			if !ok || !c.UnionID.Valid() || c.UnionID.Reference().Kind != identitydomain.KindUnionID {
				return ErrInvalid
			}
			paired, pairErr := provisioner.ProvisionVerifiedOAuthSubject(tx, identityport.OAuthSubjectCommand{OpenID: c.Fact, UnionID: c.UnionID, EventID: c.IdempotencyKey})
			if pairErr != nil {
				return pairErr
			}
			if paired.Conflict {
				identityConflict = true
				return nil
			}
			p = paired.ProvisionResult
		} else {
			p, e = s.provision.ProvisionVerifiedIdentity(tx, identityport.ProvisionCommand{Fact: c.Fact, IdempotencyKey: c.IdempotencyKey})
			if e != nil {
				return e
			}
		}
		if e = s.profiles.ObserveProviderProfile(tx, p.CustomerID, customerport.ProviderProfileObservation{DisplayName: c.DisplayName, AvatarURL: c.AvatarURL, Source: ref.Source, ObservedAt: now}); e != nil {
			return e
		}

		beneficiary := customerdomain.CustomerID(0)
		selection := paymentport.BeneficiarySelectionUnresolved
		var selectedAt *time.Time
		if c.BeneficiaryCustomerID > 0 {
			if !c.AdminAssisted {
				return ErrInvalid
			}
			beneficiary = c.BeneficiaryCustomerID
			selection = paymentport.BeneficiarySelectionAdminAssisted
			selected := now
			selectedAt = &selected
		} else if c.AdminAssisted {
			return ErrInvalid
		}
		record, e := s.store.Insert(tx, Record{UnionIDVerified: channel == paymentdomain.ChannelH5Official, TokenDigest: digest, PayerIdentityID: p.IdentityID, PayerCustomerID: p.CustomerID, BeneficiaryCustomerID: beneficiary, BeneficiarySelection: selection, BeneficiarySelectedAt: selectedAt, AppScopeDigest: scopeDigest, Channel: channel, ExpiresAt: now.Add(s.ttl), CreatedAt: now})
		if e != nil {
			return e
		}
		out = Issued{Token: token, ExpiresAt: record.ExpiresAt, PayerIdentityID: record.PayerIdentityID, PayerCustomerID: record.PayerCustomerID, BeneficiaryCustomerID: record.BeneficiaryCustomerID, BeneficiarySelection: record.BeneficiarySelection, Channel: record.Channel}
		return nil
	})
	if e == nil && identityConflict {
		return Issued{}, ErrIdentityConflict
	}
	return out, e
}
func (s *Service) Consume(ctx context.Context, token string) (Record, error) {
	if s == nil || len(token) < 20 || len(token) > 100 {
		return Record{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var out Record
	e := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.Consume(tx, digest, now); return e })
	return out, e
}

func (s *Service) ConsumeWithin(ctx context.Context, token string, now time.Time) (paymentport.SessionActor, error) {
	if s == nil || s.store == nil || len(token) < 20 || len(token) > 100 || now.IsZero() {
		return paymentport.SessionActor{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(token))
	record, err := s.store.Consume(ctx, digest, now.UTC())
	if err != nil {
		if errors.Is(err, ErrExpired) {
			return paymentport.SessionActor{}, paymentport.ErrSessionRequired
		}
		return paymentport.SessionActor{}, err
	}
	if record.Channel == paymentdomain.ChannelH5Official && !record.UnionIDVerified {
		return paymentport.SessionActor{}, paymentport.ErrSessionRequired
	}
	return actor(record), nil
}

func (s *Service) LookupWithin(ctx context.Context, token string, now time.Time) (paymentport.SessionActor, error) {
	if s == nil || s.store == nil || len(token) < 20 || len(token) > 100 || now.IsZero() {
		return paymentport.SessionActor{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(token))
	record, err := s.store.Lookup(ctx, digest, now.UTC())
	if err != nil {
		if errors.Is(err, ErrExpired) {
			return paymentport.SessionActor{}, paymentport.ErrSessionRequired
		}
		return paymentport.SessionActor{}, err
	}
	if record.Channel == paymentdomain.ChannelH5Official && !record.UnionIDVerified {
		return paymentport.SessionActor{}, paymentport.ErrSessionRequired
	}
	return actor(record), nil
}

// SelectPayerSelfWithin is the public, server-derived recipient choice. Its
// Store implementation uses a conditional update so two checkout requests
// cannot replace a selected recipient with a different one.
func (s *Service) SelectPayerSelfWithin(ctx context.Context, token string, now time.Time) (paymentport.SessionActor, error) {
	if s == nil || s.store == nil || len(token) < 20 || len(token) > 100 || now.IsZero() {
		return paymentport.SessionActor{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(token))
	record, err := s.store.SelectPayerSelf(ctx, digest, now.UTC())
	if err != nil {
		if errors.Is(err, ErrExpired) {
			return paymentport.SessionActor{}, paymentport.ErrSessionRequired
		}
		return paymentport.SessionActor{}, err
	}
	if record.Channel == paymentdomain.ChannelH5Official && !record.UnionIDVerified {
		return paymentport.SessionActor{}, paymentport.ErrSessionRequired
	}
	return actor(record), nil
}

func actor(record Record) paymentport.SessionActor {
	return paymentport.SessionActor{PayerIdentityID: record.PayerIdentityID, PayerCustomerID: int64(record.PayerCustomerID), BeneficiaryCustomerID: int64(record.BeneficiaryCustomerID), BeneficiarySelection: record.BeneficiarySelection, Channel: record.Channel}
}
