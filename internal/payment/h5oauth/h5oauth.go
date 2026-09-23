// Package h5oauth owns the one-time Official Account OAuth bridge used only to
// issue trusted Payment Sessions. It never accepts an OpenID from the browser.
package h5oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrUnavailable = errors.New("payment H5 OAuth unavailable")
var ErrInvalid = errors.New("invalid payment H5 OAuth request")
var ErrIdentityConflict = errors.New("payment H5 OAuth identities require review")

// Public commerce routes use one escaped code/slug segment only and keep
// OAuth returns same-origin. /p is the same-page standard-product checkout;
// /pay remains compatible with previously shared payment links. /distribution
// is the fixed first-level distributor center, needed only to bridge a trusted
// short Payment session into Distribution's separate browser session.
var returnPathPattern = regexp.MustCompile(`^/(?:p/[^/?#]+|pay/[^/?#]+|s/[^/?#]+(?:/pay)?|c/[a-z][a-z0-9-]{5,119})$`)
var distributionApplicationReturnPathPattern = regexp.MustCompile(`^/distribution\?product_id=([1-9][0-9]*)&product_type=(standard_product|service_period)$`)

// A Referral return keeps just the server-issued opaque invitation capability
// across the WeChat login redirect.  The canonical query order is part of the
// security boundary: it prevents an OAuth return from becoming a generic
// browser-controlled next URL.
var referralReturnPathPattern = regexp.MustCompile(`^/referral\?campaign=([1-9][0-9]*)&invite=(rfi_[A-Za-z0-9_-]{43})$`)
var referralCampaignReturnPathPattern = regexp.MustCompile(`^/referral\?campaign=([1-9][0-9]*)$`)

// A promotion credential is an opaque, fixed-size Distribution capability.
// It may follow the public standard-product or service-period route, but no
// other query key is allowed on an OAuth return. Keeping this separate from
// returnPathPattern preserves the existing query-free commerce return paths.
var promotionReturnPathPattern = regexp.MustCompile(`^/(?:p/([^/?#]+)|pay/([^/?#]+)|s/([^/?#]+)(?:/pay)?)\?promotion_context=(dpc_[A-Za-z0-9_-]{43})$`)

type Provider interface {
	Enabled() bool
	AuthorizationURL(string) string
	Exchange(context.Context, string) (paymentport.H5OAuthFacts, error)
}

type State struct {
	ReturnPath string
	ExpiresAt  time.Time
}

type Store interface {
	Create(context.Context, [32]byte, State, time.Time) error
	Consume(context.Context, [32]byte, time.Time) (State, error)
}

type PostgreSQL struct{}

func (PostgreSQL) Create(ctx context.Context, digest [32]byte, state State, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,$3,$4)`, digest[:], state.ReturnPath, state.ExpiresAt, now)
	return err
}

func (PostgreSQL) Consume(ctx context.Context, digest [32]byte, now time.Time) (State, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return State{}, err
	}
	var state State
	err = tx.QueryRow(ctx, `UPDATE payment_h5_oauth_states SET consumed_at=$2 WHERE state_digest=$1 AND consumed_at IS NULL AND expires_at>$2 RETURNING return_path,expires_at`, digest[:], now).Scan(&state.ReturnPath, &state.ExpiresAt)
	if err != nil || !validReturnPath(state.ReturnPath) {
		return State{}, ErrInvalid
	}
	return state, nil
}

type Service struct {
	uow      platformport.UnitOfWork
	store    Store
	provider Provider
	issuer   interface {
		IssueTrusted(context.Context, paymentsession.IssueCommand) (paymentsession.Issued, error)
	}
	now func() time.Time
}

func NewService(uow platformport.UnitOfWork, store Store, provider Provider, issuer interface {
	IssueTrusted(context.Context, paymentsession.IssueCommand) (paymentsession.Issued, error)
}) (*Service, error) {
	if uow == nil || store == nil || provider == nil || issuer == nil {
		return nil, ErrInvalid
	}
	return &Service{uow: uow, store: store, provider: provider, issuer: issuer, now: time.Now}, nil
}

func (s *Service) Enabled() bool { return s != nil && s.provider != nil && s.provider.Enabled() }

func (s *Service) Start(ctx context.Context, returnPath string) (string, error) {
	if !s.Enabled() || !validReturnPath(returnPath) {
		return "", ErrInvalid
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", ErrUnavailable
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		return s.store.Create(tx, digest, State{ReturnPath: returnPath, ExpiresAt: now.Add(10 * time.Minute)}, now)
	}); err != nil {
		return "", ErrUnavailable
	}
	return s.provider.AuthorizationURL(token), nil
}

func (s *Service) Complete(ctx context.Context, stateToken, code string) (paymentsession.Issued, string, error) {
	if !s.Enabled() || !safe(stateToken, 128) || !safe(code, 512) {
		return paymentsession.Issued{}, "", ErrInvalid
	}
	digest := sha256.Sum256([]byte(stateToken))
	now := s.now().UTC()
	var state State
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		state, err = s.store.Consume(tx, digest, now)
		return err
	}); err != nil {
		return paymentsession.Issued{}, "", ErrInvalid
	}
	fact, err := s.provider.Exchange(ctx, code) // Provider call is outside PostgreSQL transaction.
	if err != nil || !fact.OpenID.Valid() || !fact.UnionID.Valid() {
		return paymentsession.Issued{}, "", ErrUnavailable
	}
	issued, err := s.issuer.IssueTrusted(ctx, paymentsession.IssueCommand{Fact: fact.OpenID, UnionID: fact.UnionID, DisplayName: fact.DisplayName, AvatarURL: fact.AvatarURL, IdempotencyKey: "payment-h5-oauth:" + base64.RawURLEncoding.EncodeToString(digest[:])})
	if errors.Is(err, paymentsession.ErrIdentityConflict) {
		return paymentsession.Issued{}, "", ErrIdentityConflict
	}
	if err != nil || issued.Channel != "h5_official_account" {
		return paymentsession.Issued{}, "", ErrUnavailable
	}
	return issued, state.ReturnPath, nil
}

func safe(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

func validReturnPath(value string) bool {
	if value == "/distribution" {
		return true
	}
	if value == "/referral" {
		return true
	}
	if matches := referralReturnPathPattern.FindStringSubmatch(value); matches != nil {
		campaignID, err := strconv.ParseInt(matches[1], 10, 64)
		return err == nil && campaignID > 0
	}
	if matches := referralCampaignReturnPathPattern.FindStringSubmatch(value); matches != nil {
		campaignID, err := strconv.ParseInt(matches[1], 10, 64)
		return err == nil && campaignID > 0
	}
	// A distributor application can carry only the immutable public product
	// reference. Keep the raw canonical form closed: it rejects duplicate or
	// unknown keys, alternative encodings, fragments and every other next URL.
	if matches := distributionApplicationReturnPathPattern.FindStringSubmatch(value); matches != nil {
		productID, err := strconv.ParseInt(matches[1], 10, 64)
		return err == nil && productID > 0
	}
	// A promotion must remain in the exact page chain that Product accepts.
	// Reject all percent encoding here, including harmless-looking escaped
	// characters, so an alternate raw URL cannot be accepted and then decoded
	// into the same public product route by a later layer.
	if matches := promotionReturnPathPattern.FindStringSubmatch(value); matches != nil {
		code := matches[1]
		if code == "" {
			code = matches[2]
		}
		if code == "" {
			code = matches[3]
		}
		return !strings.Contains(code, "%") && safe(code, 200) && !strings.ContainsAny(code, "/\\?#")
	}
	if !returnPathPattern.MatchString(value) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(value, "/"), "/")
	if len(parts) < 2 || len(parts) > 3 || (parts[0] != "p" && parts[0] != "pay" && parts[0] != "s" && parts[0] != "c") || (len(parts) == 3 && (parts[0] != "s" || parts[2] != "pay")) {
		return false
	}
	code, err := url.PathUnescape(parts[1])
	// PathUnescape happens after the raw one-segment regex. Reject separators
	// introduced by percent encoding as well; otherwise /s/a%2Fb would pass the
	// regex and later redirect to a different multi-segment route.
	return err == nil && safe(code, 200) && !strings.ContainsAny(code, "/\\?#")
}
