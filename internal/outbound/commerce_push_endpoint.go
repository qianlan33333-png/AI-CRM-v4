package outbound

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// CommercePushEndpoints owns per-product destinations; runtime still owns all
// signing keys and identity disclosure policy. No network calls occur on save.
type CommercePushEndpoints struct {
	pool      *pgxpool.Pool
	runtime   CommercePushTargetResolver
	templates []string
}

func NewCommercePushEndpoints(pool *pgxpool.Pool, runtime CommercePushTargetResolver, templates []string) *CommercePushEndpoints {
	return &CommercePushEndpoints{pool: pool, runtime: runtime, templates: append([]string(nil), templates...)}
}
func validEndpointOwner(kind string, id int64) bool {
	return id > 0 && (kind == "wechat_pay" || kind == "service_period")
}
func editableCommerceEndpoint(raw string) bool {
	if len(raw) > 4096 || !validCommerceEndpoint(raw, false) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() != "" && !strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), "localhost") && !strings.ContainsAny(raw, "\r\n\t")
}
func (s *CommercePushEndpoints) ReadCommercePushEndpointWithin(ctx context.Context, kind string, id int64, ref string) (string, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", err
	}
	if !validEndpointOwner(kind, id) {
		return "", outboundport.ErrCommercePushEndpointInvalid
	}
	var endpoint, storedRef string
	err = tx.QueryRow(ctx, `SELECT endpoint,configuration_reference FROM outbound_commerce_push_endpoints WHERE owner_kind=$1 AND owner_id=$2`, kind, id).Scan(&endpoint, &storedRef)
	if err == nil {
		if ref != "" && ref != storedRef {
			return "", outboundport.ErrCommercePushEndpointInvalid
		}
		return endpoint, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if ref == "" {
		return "", nil
	}
	target, ok, err := s.runtime.CommercePushTarget(ctx, ref)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", outboundport.ErrCommercePushEndpointInvalid
	}
	return target.Endpoint, nil
}
func (s *CommercePushEndpoints) SaveCommercePushEndpointWithin(ctx context.Context, kind string, id int64, ref, endpoint string) (string, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", err
	}
	if !validEndpointOwner(kind, id) || (endpoint != "" && !editableCommerceEndpoint(endpoint)) {
		return "", outboundport.ErrCommercePushEndpointInvalid
	}
	// Serializes creation even when no destination row exists yet.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("outbound-endpoint:%s:%d", kind, id)); err != nil {
		return "", err
	}
	var template, storedRef string
	err = tx.QueryRow(ctx, `SELECT template_reference,configuration_reference FROM outbound_commerce_push_endpoints WHERE owner_kind=$1 AND owner_id=$2 FOR UPDATE`, kind, id).Scan(&template, &storedRef)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if storedRef != "" {
		if ref != "" && ref != storedRef {
			return "", outboundport.ErrCommercePushEndpointInvalid
		}
	} else {
		template = ref
		if template == "" {
			template, err = s.equivalentDefaultTemplate(ctx)
			if err != nil {
				return "", err
			}
		}
	}
	if endpoint == "" {
		_, err = tx.Exec(ctx, `DELETE FROM outbound_commerce_push_endpoints WHERE owner_kind=$1 AND owner_id=$2`, kind, id)
		return "", err
	}
	_, ok, err := s.runtime.CommercePushTarget(ctx, template)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", outboundport.ErrCommercePushEndpointInvalid
	}
	derived := fmt.Sprintf("product-endpoint:%s:%d", kind, id)
	_, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_endpoints(owner_kind,owner_id,configuration_reference,template_reference,endpoint) VALUES($1,$2,$3,$4,$5) ON CONFLICT(owner_kind,owner_id) DO UPDATE SET endpoint=excluded.endpoint,revision=outbound_commerce_push_endpoints.revision+1,updated_at=now()`, kind, id, derived, template, endpoint)
	return derived, err
}
func (s *CommercePushEndpoints) CommercePushProviderEnabled() bool {
	return s.runtime.CommercePushProviderEnabled()
}
func (s *CommercePushEndpoints) CommercePushTarget(ctx context.Context, ref string) (CommercePushTarget, bool, error) {
	if !strings.HasPrefix(ref, "product-endpoint:") {
		return s.runtime.CommercePushTarget(ctx, ref)
	}
	var endpoint, template string
	var row pgx.Row
	if tx, err := platformpostgres.RequireTransaction(ctx); err == nil {
		row = tx.QueryRow(ctx, `SELECT endpoint,template_reference FROM outbound_commerce_push_endpoints WHERE configuration_reference=$1`, ref)
	} else {
		row = s.pool.QueryRow(ctx, `SELECT endpoint,template_reference FROM outbound_commerce_push_endpoints WHERE configuration_reference=$1`, ref)
	}
	err := row.Scan(&endpoint, &template)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommercePushTarget{}, false, nil
	}
	if err != nil {
		return CommercePushTarget{}, false, err
	}
	target, ok, err := s.runtime.CommercePushTarget(ctx, template)
	if err != nil || !ok {
		return CommercePushTarget{}, ok, err
	}
	if !editableCommerceEndpoint(endpoint) {
		return CommercePushTarget{}, false, outboundport.ErrCommercePushEndpointInvalid
	}
	target.Reference = ref
	target.Slot = ref
	target.Endpoint = endpoint
	target.SigningKey = append([]byte(nil), target.SigningKey...)
	return target, true, nil
}

// Default selection is allowed only when every protected runtime field agrees.
// Endpoint, reference and logical target Slot are all replaced by this
// owner-specific override. Signing and identity disclosure policies must agree.
func (s *CommercePushEndpoints) equivalentDefaultTemplate(ctx context.Context) (string, error) {
	refs := append([]string(nil), s.templates...)
	sort.Strings(refs)
	if len(refs) == 0 {
		return "", outboundport.ErrCommercePushEndpointInvalid
	}
	var baseline CommercePushTarget
	for i, ref := range refs {
		target, ok, err := s.runtime.CommercePushTarget(ctx, ref)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", outboundport.ErrCommercePushEndpointInvalid
		}
		target.Reference = ""
		target.Slot = ""
		target.Endpoint = ""
		if i == 0 {
			baseline = target
		} else if !reflect.DeepEqual(baseline, target) {
			return "", outboundport.ErrCommercePushEndpointInvalid
		}
	}
	return refs[0], nil
}
