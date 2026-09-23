package outbound

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

var (
	ErrCommercePushInvalid  = errors.New("invalid commerce push intent")
	ErrCommercePushConflict = errors.New("commerce push intent conflict")
)

// CommercePushIdentity selects one explicitly scoped OneID value. Non-phone
// values use the generic verified reader. Phone is a separate, CN11-only
// vault read and must still be active and verified; it never falls through the
// generic reader or an administrator phone-reveal path.
type CommercePushIdentity struct {
	Kind  identitydomain.Kind `json:"kind"`
	Scope string              `json:"scope"`
}

func (v CommercePushIdentity) valid() bool {
	return identitydomain.ValidateNamespace(v.Kind, v.Scope) == nil
}

// CommercePushTarget is deployment configuration resolved from the opaque
// Product reference. Its target Slot survives endpoint/secret rotation, so it
// rather than a mutable policy digest participates in paid-event idempotency.
type CommercePushTarget struct {
	Reference  string
	Slot       string
	Endpoint   string
	SigningKey []byte
	Version    string
	TenantID   string

	BuyerID, BuyerOpenID, BuyerUnionID, BuyerPhone, BeneficiaryPhone CommercePushIdentity
	PushType, Remark                                                 string
	Day, Frequency                                                   *int64
	CustomParams                                                     map[string]any
	AllowLoopbackHTTP                                                bool // fixture-only
}

// policyDigest freezes only the protected dispatch policy: the target selected
// by the stable slot, its current protocol version and the scoped identity
// selectors. Product-owned type/day/frequency/remark/custom_params belong to
// the immutable encrypted payload and product revision; they must not make a
// paid delivery look revoked after an administrator edits the Product row.
// Signing material deliberately stays outside this digest so a protected key
// rotation signs the existing delivery without rewriting its identity or body.
func (t CommercePushTarget) policyDigest() [32]byte {
	value := struct {
		Reference, Slot, Endpoint, Version, TenantID                     string
		BuyerID, BuyerOpenID, BuyerUnionID, BuyerPhone, BeneficiaryPhone CommercePushIdentity
	}{t.Reference, t.Slot, t.Endpoint, t.Version, t.TenantID, t.BuyerID, t.BuyerOpenID, t.BuyerUnionID, t.BuyerPhone, t.BeneficiaryPhone}
	raw, _ := json.Marshal(value)
	return sha256.Sum256(raw)
}

// ValidateCommercePushTarget is intentionally narrow: Composition validates
// its protected deployment targets before any Product reference can select one.
func ValidateCommercePushTarget(value CommercePushTarget) error {
	if !value.valid() {
		return ErrCommercePushInvalid
	}
	return nil
}

func (t CommercePushTarget) valid() bool {
	// The frozen commerce sender has one transaction.paid shape for every
	// product. Custom parameters belong only to synthetic test deliveries.
	if !validCommerceText(t.Reference, 128) || !validCommerceText(t.Slot, 128) || !validCommerceText(t.Version, 128) ||
		!t.BuyerID.valid() || !t.BuyerOpenID.valid() || !t.BuyerUnionID.valid() || !t.BuyerPhone.valid() || !t.BeneficiaryPhone.valid() ||
		len(t.SigningKey) > 4096 || strings.TrimSpace(t.PushType) != t.PushType || strings.TrimSpace(t.Remark) != t.Remark || len(t.PushType) > 200 || len(t.Remark) > 2000 ||
		(t.Day != nil && *t.Day < 0) || (t.Frequency != nil && *t.Frequency < 0) || !validCommerceParams(t.CustomParams) {
		return false
	}
	return validCommerceEndpoint(t.Endpoint, t.AllowLoopbackHTTP)
}

func cloneCommercePushParams(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return nil
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&out) != nil {
		return nil
	}
	return out
}
func validCommerceParams(values map[string]any) bool {
	if len(values) > 64 {
		return false
	}
	for key, value := range values {
		if !validCommerceText(key, 128) {
			return false
		}
		raw, err := json.Marshal(value)
		if err != nil || !json.Valid(raw) || len(raw) > 4096 {
			return false
		}
	}
	return true
}
func validCommerceText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

// CommercePushTargetResolver belongs to composition. Product persists only a
// reference; endpoint, secret, active state, and identity scopes stay in the
// runtime adapter and can be revoked before a queued Provider call.
type CommercePushTargetResolver interface {
	CommercePushProviderEnabled() bool
	CommercePushTarget(context.Context, string) (CommercePushTarget, bool, error)
}

// CommercePayloadCipher holds a configuration-owned content key in memory.
// It never serializes raw payloads, so the durable intent is safe to read from
// the operator timeline without exposing identity values.
type CommercePayloadCipher interface {
	EncryptCommercePayload([]byte, []byte) ([]byte, int16, error)
	DecryptCommercePayload([]byte, int16, []byte) ([]byte, error)
}

type CommercePayloadAESGCM struct{ key []byte }

func NewCommercePayloadAESGCM(encoded string) (*CommercePayloadAESGCM, error) {
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, ErrCommercePushInvalid
	}
	return &CommercePayloadAESGCM{key: append([]byte(nil), key...)}, nil
}
func (c *CommercePayloadAESGCM) EncryptCommercePayload(raw, aad []byte) ([]byte, int16, error) {
	if c == nil || len(c.key) != 32 || len(raw) == 0 || len(raw) > 64<<10 {
		return nil, 0, ErrCommercePushInvalid
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, 0, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, 0, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, err
	}
	return append(nonce, aead.Seal(nil, nonce, raw, aad)...), 1, nil
}
func (c *CommercePayloadAESGCM) DecryptCommercePayload(ciphertext []byte, version int16, aad []byte) ([]byte, error) {
	if c == nil || len(c.key) != 32 || version != 1 {
		return nil, ErrCommercePushInvalid
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) <= aead.NonceSize() {
		return nil, ErrCommercePushInvalid
	}
	return aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], aad)
}

// CommercePushService owns the encrypted intent and its EER binding. It is
// called only with an existing Order/Product transaction, so Order settlement,
// intent, audit/outbox, EER acceptance, and River enqueue commit together.
type CommercePushService struct {
	checkoutMobile orderport.CheckoutMobileReader
	displayNames   customerport.DirectoryDisplayNameReader
	pool           *pgxpool.Pool
	uow            platformport.UnitOfWork
	effects        effectport.TransactionalAccepter
	products       productport.ExternalPushConfigurationReader
	identities     commercePushIdentityReader
	targets        CommercePushTargetResolver
	cipher         CommercePayloadCipher
	now            func() time.Time
}

// commercePushIdentityReader keeps phone out of the generic external-identity
// seam. The protected target has already selected its fixed identity scope;
// this reader can only materialize a value transiently while the current
// payment UoW accepts its encrypted Outbound intent and audit facts.
type commercePushIdentityReader interface {
	identityport.ExternalIdentityValueReader
	identityport.VerifiedOutboundPhoneReader
}

func NewCommercePushService(pool *pgxpool.Pool, uow platformport.UnitOfWork, effects effectport.TransactionalAccepter, products productport.ExternalPushConfigurationReader, identities commercePushIdentityReader, targets CommercePushTargetResolver, cipher CommercePayloadCipher) (*CommercePushService, error) {
	if pool == nil || uow == nil || effects == nil || products == nil || identities == nil || targets == nil {
		return nil, ErrCommercePushInvalid
	}
	return &CommercePushService{pool: pool, uow: uow, effects: effects, products: products, identities: identities, targets: targets, cipher: cipher, now: time.Now}, nil
}

func (s *CommercePushService) ConsumePaidEventWithin(ctx context.Context, event orderport.PaidEvent) error {
	if s == nil || !event.Valid() {
		return ErrCommercePushInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return err
	}
	for _, item := range event.Order.Items {
		if item.ProductID == nil || *item.ProductID < 1 {
			// An Order line without a canonical Product cannot have Product-owned
			// configuration. It is deliberately not guessed or dispatched.
			continue
		}
		if err := s.consumeOrderItemWithin(ctx, event, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *CommercePushService) consumeOrderItemWithin(ctx context.Context, event orderport.PaidEvent, item orderdomain.ItemSnapshot) error {
	sourceReference := commerceOrderSourceReference(event, item.LineNo)
	targetSlot := commerceProductSlot(*item.ProductID)
	if _, found, err := commerceIntentExists(ctx, sourceReference, targetSlot, event.SourceDigest, *item.ProductID); err != nil || found {
		return err
	}
	configuration, err := s.products.ReadExternalPushConfigurationForOrder(ctx, productport.ID(*item.ProductID))
	if err != nil {
		return err
	}
	// Product's Port holds its row lock for this transaction. Re-check after
	// that lock: a concurrent first consumer may have committed the immutable
	// dispatch while this consumer waited, and a later configuration revision
	// must never create a competing EER envelope for the same paid event.
	if _, found, err := commerceIntentExists(ctx, sourceReference, targetSlot, event.SourceDigest, *item.ProductID); err != nil || found {
		return err
	}
	if !configuration.Enabled || !s.targets.CommercePushProviderEnabled() {
		return s.planCommercePushWithin(ctx, commercePlannedIntent{sourceKind: "order_paid", sourceReference: sourceReference, orderEventID: event.ID, productID: *item.ProductID, productKind: configuration.ProductKind, targetReference: commerceTargetReference(configuration), targetSlot: targetSlot, revision: configuration.Revision, sourceDigest: event.SourceDigest, state: "planned_disabled"})
	}
	target, found, err := s.targets.CommercePushTarget(ctx, configuration.ConfigurationReference)
	if err != nil {
		return err
	}
	target = commercePushTargetWithProductBusiness(target, configuration)
	if !found || !target.valid() {
		return s.planCommercePushWithin(ctx, commercePlannedIntent{sourceKind: "order_paid", sourceReference: sourceReference, orderEventID: event.ID, productID: *item.ProductID, productKind: configuration.ProductKind, targetReference: configuration.ConfigurationReference, targetSlot: targetSlot, revision: configuration.Revision, sourceDigest: event.SourceDigest, state: "planned_target_unavailable"})
	}
	// The frozen transaction.paid contract is fixed.  Historic mapping rows are
	// retained for readback and already-encrypted intents retain their original
	// payloads, but a newly accepted paid event always uses the legacy body.
	body, missing, err := s.paidPayload(ctx, event, item, target, commerceDeliveryID(event.ID, item.LineNo, targetSlot))
	if err != nil {
		return err
	}
	if missing || s.cipher == nil {
		state := "planned_identity_unavailable"
		if !missing {
			state = "planned_payload_protection_unavailable"
		}
		return s.planCommercePushWithin(ctx, commercePlannedIntent{sourceKind: "order_paid", sourceReference: sourceReference, orderEventID: event.ID, productID: *item.ProductID, productKind: configuration.ProductKind, targetReference: configuration.ConfigurationReference, targetSlot: targetSlot, revision: configuration.Revision, sourceDigest: event.SourceDigest, state: state})
	}
	_, err = s.acceptCommercePushWithin(ctx, commerceAcceptedIntent{sourceKind: "order_paid", sourceReference: sourceReference, orderEventID: event.ID, productID: *item.ProductID, productKind: configuration.ProductKind, targetReference: configuration.ConfigurationReference, targetSlot: targetSlot, revision: configuration.Revision, sourceDigest: event.SourceDigest, target: target, body: body, payloadMode: "legacy"})
	return err
}

func (s *CommercePushService) AcceptExternalPushTestWithin(ctx context.Context, in productport.ExternalPushTestIntent) (productport.ExternalPushTest, error) {
	if s == nil || in.ProductID < 1 || in.ConfigurationRevision < 1 || in.ReceiptKeyDigest == ([32]byte{}) || !validCommercePushKind(in.ProductKind) || !validCommerceText(in.ConfigurationReference, 128) {
		return productport.ExternalPushTest{}, ErrCommercePushInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return productport.ExternalPushTest{}, err
	}
	configuration, err := s.products.ReadExternalPushConfigurationForOrder(ctx, in.ProductID)
	if err != nil || !configuration.Enabled || configuration.ProductKind != in.ProductKind || configuration.ConfigurationReference != in.ConfigurationReference || configuration.Revision != in.ConfigurationRevision {
		return productport.ExternalPushTest{}, ErrCommercePushConflict
	}
	target, found, err := s.targets.CommercePushTarget(ctx, in.ConfigurationReference)
	if err != nil {
		return productport.ExternalPushTest{}, err
	}
	target = commercePushTargetWithProductBusiness(target, configuration)
	if !found || !target.valid() || !s.targets.CommercePushProviderEnabled() || s.cipher == nil {
		return productport.ExternalPushTest{}, ErrCommercePushConflict
	}
	sourceDigest := sha256.Sum256(append([]byte("commerce-push-test\x00"), in.ReceiptKeyDigest[:]...))
	sourceReference := "synthetic:" + hex.EncodeToString(in.ReceiptKeyDigest[:])
	targetSlot := commerceProductSlot(int64(in.ProductID))
	deliveryID := commerceDeliveryIDFromDigest(in.ReceiptKeyDigest, targetSlot)
	if existing, found, err := commerceIntentExists(ctx, sourceReference, targetSlot, sourceDigest, int64(in.ProductID)); err != nil {
		return productport.ExternalPushTest{}, err
	} else if found {
		// A test operation is keyed by the same Product receipt digest. The
		// wire delivery identifier is therefore stable across a deduplicated
		// acceptance without reading or rebuilding its protected body.
		return productport.ExternalPushTest{ProductID: in.ProductID, ProductKind: in.ProductKind, EffectID: existing.effectID, DeliveryID: deliveryID, State: existing.state, CreatedAt: existing.createdAt}, nil
	}
	// The legacy test button always emits its fixed test protocol. A retained
	// V3 field mapping remains readable with the configuration but never changes
	// a newly accepted test delivery; existing encrypted intents retain their
	// original mode and payload untouched.
	body, err := commerceSyntheticPayload(in.ProductID, configuration.ProductName, target, deliveryID, s.now().UTC())
	if err != nil {
		return productport.ExternalPushTest{}, err
	}
	accepted, err := s.acceptCommercePushWithin(ctx, commerceAcceptedIntent{sourceKind: "synthetic_test", sourceReference: sourceReference, productID: int64(in.ProductID), productKind: in.ProductKind, targetReference: in.ConfigurationReference, targetSlot: targetSlot, revision: in.ConfigurationRevision, sourceDigest: sourceDigest, target: target, body: body, payloadMode: "legacy"})
	if err != nil {
		return productport.ExternalPushTest{}, err
	}
	return productport.ExternalPushTest{ProductID: in.ProductID, ProductKind: in.ProductKind, EffectID: accepted.effectID, DeliveryID: deliveryID, State: accepted.state, CreatedAt: accepted.createdAt}, nil
}

func commerceOrderSourceReference(event orderport.PaidEvent, lineNo int32) string {
	return "order-paid:" + strconv.FormatInt(event.ID, 10) + ":line:" + strconv.FormatInt(int64(lineNo), 10)
}
func validCommercePushEffectID(value string) bool {
	if !strings.HasPrefix(value, "eer_") || len(value) < 5 {
		return false
	}
	for index, character := range value[4:] {
		if character < '0' || character > '9' || index == 0 && character == '0' {
			return false
		}
	}
	return true
}

func commerceProductSlot(productID int64) string {
	return "product:" + strconv.FormatInt(productID, 10)
}
func commerceTargetReference(configuration productport.ExternalPushConfiguration) string {
	if configuration.ConfigurationReference != "" {
		return configuration.ConfigurationReference
	}
	return "unconfigured"
}

func commercePushTargetWithProductBusiness(target CommercePushTarget, configuration productport.ExternalPushConfiguration) CommercePushTarget {
	target.PushType, target.Day, target.Frequency, target.Remark = configuration.PushType, configuration.Day, configuration.Frequency, configuration.Remark
	target.CustomParams = cloneCommercePushParams(configuration.CustomParams)
	return target
}
func commerceDeliveryID(eventID int64, lineNo int32, slot string) string {
	d := sha256.Sum256([]byte("commerce-push-delivery.v1\x00" + strconv.FormatInt(eventID, 10) + "\x00" + strconv.FormatInt(int64(lineNo), 10) + "\x00" + slot))
	return "commerce_" + hex.EncodeToString(d[:16])
}
func commerceDeliveryIDFromDigest(digest [32]byte, slot string) string {
	d := sha256.Sum256(append(append([]byte("commerce-push-test-delivery.v1\x00"), digest[:]...), []byte("\x00"+slot)...))
	return "commerce_test_" + hex.EncodeToString(d[:16])
}
func commercePayloadAAD(sourceReference, slot string) []byte {
	return []byte("commerce-push.payload.v1\x00" + sourceReference + "\x00" + slot)
}
func validCommercePushKind(value productport.ExternalPushProductKind) bool {
	return value == productport.ExternalPushWeChatPay || value == productport.ExternalPushServicePeriod
}

type commerceIntentRecord struct {
	id        int64
	effectID  string
	state     string
	createdAt time.Time
}

func commerceIntentExists(ctx context.Context, sourceReference, targetSlot string, sourceDigest [32]byte, productID int64) (commerceIntentRecord, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return commerceIntentRecord{}, false, err
	}
	var out commerceIntentRecord
	var stored []byte
	var storedProductID int64
	err = tx.QueryRow(ctx, `SELECT id,source_digest,product_id,COALESCE(effect_id,''),state,created_at FROM outbound_commerce_push_intents WHERE source_reference=$1 AND target_slot=$2 FOR UPDATE`, sourceReference, targetSlot).Scan(&out.id, &stored, &storedProductID, &out.effectID, &out.state, &out.createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return commerceIntentRecord{}, false, nil
	}
	if err != nil {
		return commerceIntentRecord{}, false, err
	}
	if len(stored) != 32 || !hmac.Equal(stored, sourceDigest[:]) || storedProductID != productID || productID < 1 {
		return commerceIntentRecord{}, false, ErrCommercePushConflict
	}
	return out, true, nil
}

type commercePlannedIntent struct {
	sourceKind, sourceReference, targetReference, targetSlot, state string
	orderEventID                                                    int64
	productID                                                       int64
	productKind                                                     productport.ExternalPushProductKind
	revision                                                        int64
	sourceDigest                                                    [32]byte
}

func (s *CommercePushService) planCommercePushWithin(ctx context.Context, in commercePlannedIntent) error {
	if s == nil || in.productID < 1 || !validCommercePushKind(in.productKind) || in.revision < 0 || in.sourceDigest == ([32]byte{}) ||
		!validCommerceText(in.sourceReference, 200) || !validCommerceText(in.targetSlot, 128) || !validCommerceText(in.targetReference, 128) ||
		!validCommercePlannedState(in.state) {
		return ErrCommercePushInvalid
	}
	if _, found, err := commerceIntentExists(ctx, in.sourceReference, in.targetSlot, in.sourceDigest, in.productID); err != nil || found {
		return err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	if now.IsZero() {
		return ErrCommercePushInvalid
	}
	var eventID any
	if in.orderEventID > 0 {
		eventID = in.orderEventID
	}
	intentDigest := commerceIntentDigest(in.sourceKind, in.sourceReference, in.productID, in.targetSlot, in.revision, in.sourceDigest, [32]byte{}, [32]byte{}, [32]byte{})
	keyDigest := sha256.Sum256([]byte("commerce-push.intent.v1\x00" + in.sourceReference + "\x00" + in.targetSlot))
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO outbound_commerce_push_intents(source_kind,source_reference,order_paid_event_id,product_id,product_kind,target_reference,target_slot,product_configuration_revision,source_digest,target_digest,payload_digest,policy_digest,receipt_key_digest,intent_digest,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16) ON CONFLICT(source_reference,target_slot) DO NOTHING RETURNING id`, in.sourceKind, in.sourceReference, eventID, in.productID, string(in.productKind), in.targetReference, in.targetSlot, in.revision, in.sourceDigest[:], zeroCommerceDigest(), zeroCommerceDigest(), zeroCommerceDigest(), keyDigest[:], intentDigest[:], in.state, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		_, found, readErr := commerceIntentExists(ctx, in.sourceReference, in.targetSlot, in.sourceDigest, in.productID)
		if readErr != nil || !found {
			return readErr
		}
		return nil
	}
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"commerce_push_intent_id": id, "source_reference": in.sourceReference, "state": in.state})
	key := sha256.Sum256([]byte("planned:" + strconv.FormatInt(id, 10)))
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_audit_events(intent_id,operation,payload_digest,occurred_at) VALUES($1,'planned',$2,$3)`, id, sha256Bytes(payload), now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_outbox(event_type,intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.commerce_push.planned.v1',$1,$2::jsonb,$3,$4)`, id, payload, key[:], now); err != nil {
		return err
	}
	return nil
}

func zeroCommerceDigest() []byte    { return make([]byte, 32) }
func sha256Bytes(raw []byte) []byte { d := sha256.Sum256(raw); return d[:] }
func validCommercePlannedState(v string) bool {
	switch v {
	case "planned_disabled", "planned_config_expired", "planned_target_unavailable", "planned_identity_unavailable", "planned_payload_protection_unavailable":
		return true
	}
	return false
}

type commerceAcceptedIntent struct {
	payloadMode                                              string
	sourceKind, sourceReference, targetReference, targetSlot string
	orderEventID                                             int64
	productID                                                int64
	productKind                                              productport.ExternalPushProductKind
	revision                                                 int64
	sourceDigest                                             [32]byte
	target                                                   CommercePushTarget
	body                                                     []byte
}

func (s *CommercePushService) acceptCommercePushWithin(ctx context.Context, in commerceAcceptedIntent) (commerceIntentRecord, error) {
	if in.payloadMode == "" {
		in.payloadMode = "legacy"
	}
	if in.payloadMode != "legacy" && in.payloadMode != "custom_fields_v1" {
		return commerceIntentRecord{}, ErrCommercePushInvalid
	}
	if s == nil || in.productID < 1 || in.revision < 1 || in.sourceDigest == ([32]byte{}) || !validCommercePushKind(in.productKind) || !in.target.valid() || len(in.body) == 0 || len(in.body) > 64<<10 || !json.Valid(in.body) {
		return commerceIntentRecord{}, ErrCommercePushInvalid
	}
	if existing, found, err := commerceIntentExists(ctx, in.sourceReference, in.targetSlot, in.sourceDigest, in.productID); err != nil || found {
		return existing, err
	}
	payloadDigest := sha256.Sum256(in.body)
	targetDigest := sha256.Sum256([]byte("commerce-push.target.v1\x00" + in.target.Reference + "\x00" + in.target.Slot))
	policyDigest := in.target.policyDigest()
	ciphertext, keyVersion, err := s.cipher.EncryptCommercePayload(in.body, commercePayloadAAD(in.sourceReference, in.targetSlot))
	if err != nil || keyVersion != 1 || len(ciphertext) < 29 {
		return commerceIntentRecord{}, ErrCommercePushInvalid
	}
	envelope := commerceEnvelope(in.sourceDigest, targetDigest, payloadDigest, policyDigest)
	if !envelope.Valid() {
		return commerceIntentRecord{}, ErrCommercePushInvalid
	}
	projection, receipt, err := s.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("outbound.commerce_push.accept.v1", in.sourceReference, in.targetSlot), Envelope: envelope})
	if err != nil {
		return commerceIntentRecord{}, err
	}
	if projection.ID == "" || receipt.QueueReceiptID == "" || (projection.State != effectport.StateAccepted && projection.State != effectport.StateQueued) {
		return commerceIntentRecord{}, ErrCommercePushConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return commerceIntentRecord{}, err
	}
	now := s.now().UTC()
	var eventID any
	if in.orderEventID > 0 {
		eventID = in.orderEventID
	}
	keyDigest := sha256.Sum256([]byte("commerce-push.intent.v1\x00" + in.sourceReference + "\x00" + in.targetSlot))
	intentDigest := commerceIntentDigest(in.sourceKind, in.sourceReference, in.productID, in.targetSlot, in.revision, in.sourceDigest, targetDigest, payloadDigest, policyDigest)
	var out commerceIntentRecord
	err = tx.QueryRow(ctx, `INSERT INTO outbound_commerce_push_intents(source_kind,source_reference,order_paid_event_id,product_id,product_kind,target_reference,target_slot,product_configuration_revision,source_digest,target_digest,payload_digest,policy_digest,receipt_key_digest,intent_digest,envelope_fingerprint,payload_ciphertext,payload_key_version,effect_id,queue_receipt_id,state,created_at,updated_at,payload_mode) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,'queued',$20,$20,$21) ON CONFLICT(source_reference,target_slot) DO NOTHING RETURNING id,effect_id,state,created_at`, in.sourceKind, in.sourceReference, eventID, in.productID, string(in.productKind), in.targetReference, in.targetSlot, in.revision, in.sourceDigest[:], targetDigest[:], payloadDigest[:], policyDigest[:], keyDigest[:], intentDigest[:], string(envelope.Fingerprint()), ciphertext, keyVersion, projection.ID, receipt.QueueReceiptID, now, in.payloadMode).Scan(&out.id, &out.effectID, &out.state, &out.createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		stored, found, readErr := commerceIntentExists(ctx, in.sourceReference, in.targetSlot, in.sourceDigest, in.productID)
		if readErr != nil || !found {
			return commerceIntentRecord{}, readErr
		}
		return stored, nil
	}
	if err != nil {
		return commerceIntentRecord{}, err
	}
	payload, _ := json.Marshal(map[string]any{"commerce_push_intent_id": out.id, "source_reference": in.sourceReference, "effect_id": out.effectID, "state": out.state})
	key := sha256.Sum256([]byte("queued:" + strconv.FormatInt(out.id, 10)))
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_audit_events(intent_id,operation,payload_digest,occurred_at) VALUES($1,'accepted',$2,$3)`, out.id, sha256Bytes(payload), now); err != nil {
		return commerceIntentRecord{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_outbox(event_type,intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.commerce_push.queued.v1',$1,$2::jsonb,$3,$4)`, out.id, payload, key[:], now); err != nil {
		return commerceIntentRecord{}, err
	}
	return out, nil
}

func commerceIntentDigest(sourceKind, sourceReference string, productID int64, slot string, revision int64, source, target, payload, policy [32]byte) [32]byte {
	raw, _ := json.Marshal([]any{sourceKind, sourceReference, productID, slot, revision, hex.EncodeToString(source[:]), hex.EncodeToString(target[:]), hex.EncodeToString(payload[:]), hex.EncodeToString(policy[:])})
	return sha256.Sum256(raw)
}
func commerceEnvelope(source, target, payload, policy [32]byte) effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindCommerceProductPush, SourceRefDigest: effectport.Hash("commerce.push.source.v1", hex.EncodeToString(source[:])), TargetRefDigest: effectport.Hash("commerce.push.target.v1", hex.EncodeToString(target[:])), PayloadDigest: effectport.Hash("commerce.push.payload.v1", hex.EncodeToString(payload[:])), PolicyVersionHash: effectport.Hash("commerce.push.policy.v1", hex.EncodeToString(policy[:]))}
}

func (s *CommercePushService) paidPayload(ctx context.Context, event orderport.PaidEvent, item orderdomain.ItemSnapshot, target CommercePushTarget, deliveryID string) ([]byte, bool, error) {
	buyerID, err := s.optionalIdentity(ctx, event.Order.PayerCustomerID, target.BuyerID)
	if err != nil {
		return nil, false, err
	}
	openID, err := s.optionalIdentity(ctx, event.Order.PayerCustomerID, target.BuyerOpenID)
	if err != nil {
		return nil, false, err
	}
	unionID, err := s.optionalIdentity(ctx, event.Order.PayerCustomerID, target.BuyerUnionID)
	if err != nil {
		return nil, false, err
	}
	buyerPhone, buyerPhoneUnavailable, err := s.optionalPhone(ctx, event.Order.PayerCustomerID, target.BuyerPhone)
	if err != nil {
		return nil, false, err
	}
	// The frozen sender emits the configured beneficiary selector when it has a
	// trusted value and otherwise preserves the old empty-string behavior. It
	// never derives a phone from order metadata or an unverified identity.
	beneficiaryPhone, beneficiaryPhoneUnavailable, err := s.optionalPhone(ctx, event.Order.BeneficiaryCustomerID, target.BeneficiaryPhone)
	if err != nil {
		return nil, false, err
	}
	if buyerPhoneUnavailable || beneficiaryPhoneUnavailable {
		return nil, true, nil
	}
	order := struct {
		ID         string `json:"id"`
		OrderNo    string `json:"order_no"`
		OutTradeNo string `json:"out_trade_no"`
		Status     string `json:"status"`
		PaidAmount int64  `json:"paid_amount"`
		PaidAt     string `json:"paid_at"`
		PayChannel string `json:"pay_channel"`
	}{
		ID: strconv.FormatInt(event.OrderID, 10), OrderNo: event.Order.MerchantOrderNo, OutTradeNo: event.Order.MerchantOrderNo,
		Status:     "paid", // Order.Amount is the checkout's frozen payable (after coupon) amount, verified on settlement.
		PaidAmount: event.Order.Amount.AmountMinor, PaidAt: commerceUTC(event.OccurredAt), PayChannel: "wechat",
	}
	productPrice := item.UnitAmountMinor
	// Old payloads used the catalog price while paid_amount used payer_total.
	// Order's immutable checkout snapshot is the equivalent frozen catalog fact;
	// direct native orders without it retain their immutable item fallback.
	if event.CheckoutProductID == *item.ProductID && event.CheckoutGrossAmountMinor > 0 {
		productPrice = event.CheckoutGrossAmountMinor
	}
	product := struct {
		ID    string `json:"id"`
		Code  string `json:"code"`
		Name  string `json:"name"`
		Price int64  `json:"price"`
	}{ID: strconv.FormatInt(*item.ProductID, 10), Code: item.ProductCode, Name: item.ProductName, Price: productPrice}
	buyer := struct {
		ID      string `json:"id"`
		OpenID  string `json:"openid"`
		UnionID string `json:"unionid"`
		Phone   string `json:"phone"`
	}{ID: buyerID, OpenID: maskCommerceOpenID(openID), UnionID: unionID, Phone: buyerPhone}
	body := struct {
		PhoneNumber        string `json:"phone_number"`
		PushType           string `json:"type"`
		Day                *int64 `json:"day"`
		Frequency          *int64 `json:"frequency"`
		Remark             string `json:"remark"`
		SubmittedAt        string `json:"submitted_at"`
		QuestionnaireTitle string `json:"questionnaire_title"`
		DeliveryID         string `json:"delivery_id"`
		Event              string `json:"event"`
		Order              any    `json:"order"`
		Product            any    `json:"product"`
		Buyer              any    `json:"buyer"`
		Transaction        struct {
			TransactionID string `json:"transaction_id"`
			TradeState    string `json:"trade_state"`
			SuccessTime   string `json:"success_time"`
		} `json:"transaction"`
		DomainEventOutboxID int64 `json:"domain_event_outbox_id"`
	}{
		PhoneNumber: beneficiaryPhone, PushType: target.PushType, Day: target.Day, Frequency: target.Frequency, Remark: target.Remark,
		SubmittedAt: commerceShanghai(event.OccurredAt), QuestionnaireTitle: "微信支付开通黄小璨会员", DeliveryID: deliveryID,
		Event: "transaction.paid", Order: order, Product: product, Buyer: buyer, DomainEventOutboxID: event.DomainEventOutboxID,
	}
	body.Transaction.TransactionID = event.Order.ProviderTransactionNo
	body.Transaction.TradeState = "SUCCESS"
	body.Transaction.SuccessTime = commerceUTC(event.OccurredAt)
	raw, marshalErr := json.Marshal(body)
	return raw, false, marshalErr
}

func (s *CommercePushService) optionalIdentity(ctx context.Context, customerID *int64, selector CommercePushIdentity) (string, error) {
	if customerID == nil || *customerID < 1 {
		return "", nil
	}
	if selector.Kind == identitydomain.KindPhone {
		return "", ErrCommercePushInvalid
	}
	value, _, err := s.identities.VerifiedExternalIdentityValue(ctx, customerdomain.CustomerID(*customerID), selector.Kind, selector.Scope)
	return value, err
}

// optionalPhone deliberately has different absence semantics from generic
// external identities. A configured phone selector without a single verified
// vault fact must not produce a partly populated Provider payload; its
// enclosing paid event records planned_identity_unavailable instead.
func (s *CommercePushService) optionalPhone(ctx context.Context, customerID *int64, selector CommercePushIdentity) (string, bool, error) {
	if selector.Kind == "" && selector.Scope == "" {
		return "", false, nil
	}
	if selector.Kind != identitydomain.KindPhone || selector.Scope != "phone:cn11" || customerID == nil || *customerID < 1 {
		return "", true, nil
	}
	value, found, err := s.identities.VerifiedOutboundPhone(ctx, customerdomain.CustomerID(*customerID), selector.Scope)
	if err != nil {
		return "", false, err
	}
	return value, !found, nil
}
func commerceUTC(at time.Time) string { return at.UTC().Format(time.RFC3339) }
func commerceShanghai(at time.Time) string {
	return at.UTC().In(time.FixedZone("CST", 8*60*60)).Format(time.RFC3339)
}
func maskCommerceOpenID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		if value == "" {
			return ""
		}
		if len(value) < 2 {
			return value + "***"
		}
		return value[:2] + "***"
	}
	return value[:4] + "***" + value[len(value)-4:]
}

func commerceSyntheticPayload(productID productport.ID, productName string, target CommercePushTarget, deliveryID string, occurredAt time.Time) ([]byte, error) {
	if productID < 1 || !target.valid() || deliveryID == "" || occurredAt.IsZero() {
		return nil, ErrCommercePushInvalid
	}
	body := struct {
		Event      string `json:"event"`
		DeliveryID string `json:"delivery_id"`
		OccurredAt string `json:"occurred_at"`
		Tenant     struct {
			ID string `json:"id"`
		} `json:"tenant"`
		Product struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"product"`
		CustomParams map[string]any `json:"custom_params"`
	}{Event: "external_push.test", DeliveryID: deliveryID, OccurredAt: commerceUTC(occurredAt), CustomParams: cloneCommercePushParams(target.CustomParams)}
	if body.CustomParams == nil {
		body.CustomParams = map[string]any{}
	}
	body.Tenant.ID = target.TenantID
	if body.Tenant.ID == "" {
		body.Tenant.ID = "aicrm"
	}
	body.Product.ID, body.Product.Name = strconv.FormatInt(int64(productID), 10), productName
	return json.Marshal(body)
}

func validCommerceEndpoint(raw string, allowLoopback bool) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" || raw != strings.TrimSpace(raw) {
		return false
	}
	port := parsed.Port()
	if parsed.Scheme == "https" && (port == "" || port == "443") && !isDisallowedCommerceHost(parsed.Hostname(), false) {
		return true
	}
	return allowLoopback && parsed.Scheme == "http" && isLoopbackCommerceHost(parsed.Hostname()) && (port == "" || validCommercePort(port))
}
func validCommercePort(value string) bool {
	p, err := strconv.Atoi(value)
	return err == nil && p >= 1 && p <= 65535
}
func isLoopbackCommerceHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
func isDisallowedCommerceHost(host string, allowLoopback bool) bool {
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return disallowedCommerceIP(ip, allowLoopback)
}
func disallowedCommerceIP(ip netip.Addr, allowLoopback bool) bool {
	if allowLoopback && ip.IsLoopback() {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.Is6() && ip.Is4In6() && disallowedCommerceIP(ip.Unmap(), allowLoopback)
}

// CommercePushExecution is the minimum encrypted, frozen dispatch read needed
// by the Provider adapter after EER marks an attempt. It intentionally has no
// decoded identity or provider credential fields.
type CommercePushExecution struct {
	PayloadMode                                             string
	IntentID, ProductID                                     int64
	SourceReference, TargetSlot                             string
	TargetReference                                         string
	Ciphertext                                              []byte
	KeyVersion                                              int16
	SourceDigest, TargetDigest, PayloadDigest, PolicyDigest [32]byte
}

func (s *CommercePushService) CommercePushExecution(ctx context.Context, fingerprint string) (CommercePushExecution, bool, error) {
	if s == nil || !effectport.ValidDigest(effectport.Digest(fingerprint)) {
		return CommercePushExecution{}, false, ErrCommercePushInvalid
	}
	var out CommercePushExecution
	var source, target, payload, policy []byte
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		return tx.QueryRow(txctx, `SELECT id,product_id,source_reference,target_slot,target_reference,payload_ciphertext,payload_key_version,source_digest,target_digest,payload_digest,policy_digest,payload_mode FROM outbound_commerce_push_intents WHERE envelope_fingerprint=$1 AND state IN ('queued','attempted')`, fingerprint).Scan(&out.IntentID, &out.ProductID, &out.SourceReference, &out.TargetSlot, &out.TargetReference, &out.Ciphertext, &out.KeyVersion, &source, &target, &payload, &policy, &out.PayloadMode)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CommercePushExecution{}, false, nil
	}
	if err != nil {
		return CommercePushExecution{}, false, err
	}
	if out.IntentID < 1 || out.ProductID < 1 || !validCommerceText(out.SourceReference, 200) || !validCommerceText(out.TargetSlot, 128) || !validCommerceText(out.TargetReference, 128) || len(out.Ciphertext) < 29 || out.KeyVersion != 1 || len(source) != 32 || len(target) != 32 || len(payload) != 32 || len(policy) != 32 {
		return CommercePushExecution{}, false, ErrCommercePushConflict
	}
	copy(out.SourceDigest[:], source)
	copy(out.TargetDigest[:], target)
	copy(out.PayloadDigest[:], payload)
	copy(out.PolicyDigest[:], policy)
	return out, true, nil
}

// ReadExternalPushTestStatus is the Product-facing read Port. It has a fixed
// Product/effect binding and returns only safe result-state facts from the
// Outbound-owned immutable intent.
func (s *CommercePushService) ReadExternalPushTestStatus(ctx context.Context, productID productport.ID, effectID string) (productport.ExternalPushTestStatus, error) {
	if s == nil || s.uow == nil || productID < 1 || !validCommercePushEffectID(effectID) {
		return productport.ExternalPushTestStatus{}, ErrCommercePushInvalid
	}
	var result productport.ExternalPushTestStatus
	var received *bool
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		return tx.QueryRow(txctx, `SELECT effect_id,state,attempt_count,provider_call_attempted,provider_real_call_executed,provider_result_received,updated_at
FROM outbound_commerce_push_intents WHERE product_id=$1 AND effect_id=$2`, int64(productID), effectID).Scan(
			&result.EffectID, &result.State, &result.AttemptCount, &result.ProviderCallAttempted, &result.RealExternalCallExecuted, &received, &result.UpdatedAt,
		)
	})
	if err != nil {
		return productport.ExternalPushTestStatus{}, err
	}
	result.ProviderResultReceived = received
	return result, nil
}

type CommercePushProvider struct {
	enabled    bool
	executions *CommercePushService
	targets    CommercePushTargetResolver
	cipher     CommercePayloadCipher
	now        func() time.Time
}

// commercePushHTTPResponseArtifactKind is a bounded, integrity-checked
// projection of a response that actually reached the legacy receiver. It
// deliberately excludes the response body and all dynamic error text.
const commercePushHTTPResponseArtifactKind = "commerce.push.http_response.v1"

type commercePushHTTPResponseFact struct {
	Status  int
	Outcome string
}

func commercePushResponseOutcome(status int) (string, bool) {
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		return "provider_accepted", true
	case status >= http.StatusMultipleChoices && status < http.StatusInternalServerError:
		return "provider_rejected", true
	case status >= http.StatusInternalServerError && status <= 599:
		return "response_unknown", true
	default:
		return "", false
	}
}

func commercePushHTTPResponseArtifact(status int) effectport.ResultArtifact {
	outcome, ok := commercePushResponseOutcome(status)
	if !ok {
		return effectport.ResultArtifact{}
	}
	payload, err := json.Marshal(map[string]any{"status": status, "outcome": outcome})
	if err != nil {
		return effectport.ResultArtifact{}
	}
	return effectport.ResultArtifact{
		Kind:    commercePushHTTPResponseArtifactKind,
		Payload: payload,
		Digest:  effectport.Hash("external-effect.artifact.v1", commercePushHTTPResponseArtifactKind, string(payload)),
	}
}

func commercePushResponseArtifactFacts(artifact effectport.ResultArtifact) (status int, outcome string, ok bool) {
	if artifact.Kind != commercePushHTTPResponseArtifactKind || !artifact.Valid() {
		return 0, "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(artifact.Payload))
	decoder.DisallowUnknownFields()
	var value commercePushHTTPResponseFact
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return 0, "", false
	}
	expected, valid := commercePushResponseOutcome(value.Status)
	if !valid || value.Outcome != expected {
		return 0, "", false
	}
	return value.Status, value.Outcome, true
}

func NewCommercePushProvider(enabled bool, executions *CommercePushService, targets CommercePushTargetResolver, cipher CommercePayloadCipher) (*CommercePushProvider, error) {
	if executions == nil || targets == nil {
		return nil, ErrCommercePushInvalid
	}
	return &CommercePushProvider{enabled: enabled, executions: executions, targets: targets, cipher: cipher, now: time.Now}, nil
}

func (p *CommercePushProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	base := effectport.Hash("commerce.push.provider.v1", string(envelope.Fingerprint()))
	if p == nil || p.executions == nil || p.targets == nil || p.cipher == nil || envelope.Kind != effectport.KindCommerceProductPush || !envelope.Valid() || attempt.EffectID == "" || attempt.Number < 1 {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "invalid")}, nil
	}
	if !p.enabled || !p.targets.CommercePushProviderEnabled() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "provider-disabled")}, nil
	}
	execution, found, err := p.executions.CommercePushExecution(ctx, string(envelope.Fingerprint()))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash(string(base), "intent-unavailable")}, errors.New("commerce push intent unavailable")
	}
	if !found {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "intent-missing")}, nil
	}
	if expected := commerceEnvelope(execution.SourceDigest, execution.TargetDigest, execution.PayloadDigest, execution.PolicyDigest); expected.Fingerprint() != envelope.Fingerprint() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "intent-drift")}, nil
	}
	target, found, err := p.targets.CommercePushTarget(ctx, execution.TargetReference)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash(string(base), "target-unavailable")}, errors.New("commerce push target unavailable")
	}
	if !found || !target.valid() || target.policyDigest() != execution.PolicyDigest {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "target-revoked")}, nil
	}
	body, err := p.cipher.DecryptCommercePayload(execution.Ciphertext, execution.KeyVersion, commercePayloadAAD(execution.SourceReference, execution.TargetSlot))
	if err != nil || !json.Valid(body) || sha256.Sum256(body) != execution.PayloadDigest {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "payload-unavailable")}, nil
	}
	event, deliveryID, ok := commerceExecutionHeaderValues(execution, body)
	if !ok {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "payload-invalid")}, nil
	}
	timestamp := strconv.FormatInt(p.now().UTC().Unix(), 10)
	signature := commercePushSignature(target.SigningKey, timestamp, body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.Endpoint, bytes.NewReader(body))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "request-invalid")}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AICRM-Event", event)
	req.Header.Set("X-AICRM-Delivery-Id", deliveryID)
	req.Header.Set("X-AICRM-Timestamp", timestamp)
	req.Header.Set("X-AICRM-Signature", signature)
	response, err := commerceHTTPClient(target.AllowLoopbackHTTP).Do(req)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash(string(base), "request-unknown"), CallAttempted: true, RealExternalCallExecuted: true}, errors.New("commerce push request outcome unknown")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	artifact := commercePushHTTPResponseArtifact(response.StatusCode)
	if !artifact.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash(string(base), "response-invalid"), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	if response.StatusCode >= 500 {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash(string(base), "response-unknown", strconv.Itoa(response.StatusCode)), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "provider-rejected", strconv.Itoa(response.StatusCode)), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash(string(base), "provider-accepted", strconv.Itoa(response.StatusCode), string(envelope.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

func commercePayloadHeaderValues(body []byte) (string, string, bool) {
	var value struct {
		Event      string `json:"event"`
		DeliveryID string `json:"delivery_id"`
	}
	if json.Unmarshal(body, &value) != nil || !validCommerceText(value.Event, 120) || !validCommerceText(value.DeliveryID, 200) {
		return "", "", false
	}
	return value.Event, value.DeliveryID, true
}
func commercePushSignature(key []byte, timestamp string, body []byte) string {
	if strings.TrimSpace(string(key)) == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(string(key))))
	_, _ = mac.Write([]byte(strings.TrimSpace(timestamp)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func commerceHTTPClient(allowLoopback bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !validCommercePort(port) {
			return nil, errors.New("commerce target dial rejected")
		}
		if isDisallowedCommerceHost(host, allowLoopback) {
			return nil, errors.New("commerce target dial rejected")
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("commerce target resolution unavailable")
		}
		for _, ip := range addresses {
			if disallowedCommerceIP(ip, allowLoopback) {
				return nil, errors.New("commerce target dial rejected")
			}
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// CommercePushCompletionSink owns no Provider behavior. It projects the
// terminal EER result into the encrypted outbound intent in that same EER
// completion transaction, keeping response facts separate from acceptance.
type CommercePushCompletionSink struct{ service *CommercePushService }

func NewCommercePushCompletionSink(service *CommercePushService) (*CommercePushCompletionSink, error) {
	if service == nil {
		return nil, ErrCommercePushInvalid
	}
	return &CommercePushCompletionSink{service: service}, nil
}
func (s *CommercePushCompletionSink) CompleteEffect(ctx context.Context, effectID string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || s.service == nil || effectID == "" || envelope.Kind != effectport.KindCommerceProductPush || attempt.Number < 1 || !effectport.ValidDigest(result.ReceiptDigest) {
		return ErrCommercePushInvalid
	}
	state := "final_failed"
	var received, responseStatus, resultCode any
	switch result.Completion {
	case effectport.StateExecuted:
		state, received = "provider_accepted", result.CallAttempted && result.RealExternalCallExecuted
	case effectport.StateUnknown:
		state = "outcome_unknown"
	case effectport.StateRetryable:
		state = "attempted"
	case effectport.StateFinalFailed:
		state, received = "final_failed", result.CallAttempted
	case effectport.StateReconciled:
		state = "reconciled"
	default:
		return ErrCommercePushInvalid
	}
	if result.Artifact.Kind != "" || len(result.Artifact.Payload) != 0 || result.Artifact.Digest != "" {
		status, outcome, valid := commercePushResponseArtifactFacts(result.Artifact)
		if !valid {
			return ErrCommercePushInvalid
		}
		switch {
		case outcome == "provider_accepted" && result.Completion == effectport.StateExecuted:
		case outcome == "provider_rejected" && result.Completion == effectport.StateFinalFailed:
		case outcome == "response_unknown" && result.Completion == effectport.StateUnknown:
		default:
			return ErrCommercePushInvalid
		}
		received, responseStatus, resultCode = true, status, outcome
	}
	raw, err := effectDigestBytes(result.ReceiptDigest)
	if err != nil {
		return err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	now := s.service.now().UTC()
	var intentID int64
	err = tx.QueryRow(ctx, `UPDATE outbound_commerce_push_intents SET state=$2,attempt_count=$3,provider_call_attempted=$4,provider_real_call_executed=$5,provider_result_received=$6,provider_response_status=$7,provider_result_code=$8,receipt_digest=$9,updated_at=$10 WHERE effect_id=$1 RETURNING id`, effectID, state, attempt.Number, result.CallAttempted, result.RealExternalCallExecuted, received, responseStatus, resultCode, raw, now).Scan(&intentID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"commerce_push_intent_id": intentID, "effect_id": effectID, "state": state, "attempt_count": attempt.Number})
	key := sha256.Sum256([]byte("complete:" + effectID + ":" + strconv.FormatInt(attempt.Generation, 10)))
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_audit_events(intent_id,operation,payload_digest,occurred_at) VALUES($1,'completed',$2,$3)`, intentID, sha256Bytes(payload), now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_outbox(event_type,intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.commerce_push.completed.v1',$1,$2::jsonb,$3,$4) ON CONFLICT(event_type,idempotency_digest) DO NOTHING`, intentID, payload, key[:], now)
	return err
}

var _ orderport.PaidEventConsumer = (*CommercePushService)(nil)
var _ productport.ExternalPushTestAccepter = (*CommercePushService)(nil)
var _ productport.ExternalPushTestStatusReader = (*CommercePushService)(nil)
var _ effectport.ProviderAdapter = (*CommercePushProvider)(nil)
var _ effectport.CompletionSink = (*CommercePushCompletionSink)(nil)

// ListCommercePushDeliveries is the read-only Outbound Port behind the legacy
// payment-order page. Current rows join only this Outbound-owned intent to its
// Order paid-event ID. Historical rows require an exact Order-owned source
// coordinate, never a numeric V2/V3 primary-key comparison.
func (s *CommercePushService) ListCommercePushDeliveries(ctx context.Context, query outboundport.CommercePushDeliveryQuery) ([]outboundport.CommercePushDelivery, error) {
	if s == nil || s.uow == nil || (query.PaidEventID < 1 && (query.HistoricalSourceKind == "" || query.HistoricalSourceSystem == "" || query.HistoricalSourceKey == "")) || (query.PaidEventID > 0 && (query.HistoricalSourceKind != "" || query.HistoricalSourceSystem != "" || query.HistoricalSourceKey != "")) || len(query.HistoricalSourceKind) > 80 || len(query.HistoricalSourceSystem) > 160 || len(query.HistoricalSourceKey) > 240 || strings.TrimSpace(query.HistoricalSourceKind) != query.HistoricalSourceKind || strings.TrimSpace(query.HistoricalSourceSystem) != query.HistoricalSourceSystem || strings.TrimSpace(query.HistoricalSourceKey) != query.HistoricalSourceKey {
		return nil, ErrCommercePushInvalid
	}
	out := []outboundport.CommercePushDelivery{}
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		if query.PaidEventID > 0 {
			rows, readErr := tx.Query(txctx, `SELECT id,COALESCE(effect_id,''),state,attempt_count,provider_call_attempted,provider_real_call_executed,provider_result_received,provider_response_status,provider_result_code,created_at,updated_at
FROM outbound_commerce_push_intents WHERE order_paid_event_id=$1 ORDER BY created_at,id`, query.PaidEventID)
			if readErr != nil {
				return readErr
			}
			defer rows.Close()
			for rows.Next() {
				var row outboundport.CommercePushDelivery
				var id int64
				var callAttempted, realExternalCallExecuted bool
				if err = rows.Scan(&id, &row.EffectID, &row.State, &row.AttemptCount, &callAttempted, &realExternalCallExecuted, &row.ProviderResultReceived, &row.ResponseStatus, &row.ResultCode, &row.CreatedAt, &row.UpdatedAt); err != nil {
					return err
				}
				row.ProviderCallAttempted, row.RealExternalCallExecuted = &callAttempted, &realExternalCallExecuted
				row.ID, row.Source = "current:"+strconv.FormatInt(id, 10), "current"
				out = append(out, row)
			}
			return rows.Err()
		}
		rows, readErr := tx.Query(txctx, `SELECT r.id,r.source_delivery_id,r.source_effect_job_id,r.source_state,r.source_attempt_count,r.source_effect_state,r.source_response_status,r.source_error_message,r.source_response_body_protected,r.source_created_at,r.source_updated_at
FROM outbound_commerce_push_history_rows r
WHERE r.source_kind='delivery' AND r.source_order_kind=$1 AND r.source_order_scope=$2 AND r.source_order_key=$3
  AND EXISTS (SELECT 1 FROM outbound_commerce_push_history_batch_rows membership
              JOIN outbound_commerce_push_history_batches batch ON batch.id=membership.batch_id
              WHERE membership.source_row_id=r.id AND batch.status IN ('applied','reconciled'))
ORDER BY r.source_created_at,r.id`, query.HistoricalSourceKind, query.HistoricalSourceSystem, query.HistoricalSourceKey)
		if readErr != nil {
			return readErr
		}
		defer rows.Close()
		for rows.Next() {
			var row outboundport.CommercePushDelivery
			var id int64
			var legacyEffectState *string
			if err = rows.Scan(&id, &row.HistoricalDeliveryID, &row.LegacyEffectJobID, &row.State, &row.AttemptCount, &legacyEffectState, &row.ResponseStatus, &row.ErrorMessage, &row.ResponseBodyProtected, &row.CreatedAt, &row.UpdatedAt); err != nil {
				return err
			}
			row.ProviderCallAttempted, row.RealExternalCallExecuted, row.ProviderResultReceived = legacyCommercePushProviderFacts(legacyEffectState, row.ResponseStatus)
			row.ID, row.Source = "history:"+strconv.FormatInt(id, 10), "history"
			out = append(out, row)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// legacyCommercePushProviderFacts only projects facts that the frozen V2
// delivery/effect relation can prove. In particular, the old settlement path
// assigns attempt_count=1 to blocked, cancelled, and simulated jobs, so that
// counter alone cannot establish a Provider call.
func legacyCommercePushProviderFacts(effectState *string, responseStatus *int) (attempted, realExternalCallExecuted, resultReceived *bool) {
	trueValue := true
	falseValue := false
	if responseStatus != nil {
		return &trueValue, &trueValue, &trueValue
	}
	if effectState == nil {
		return nil, nil, nil
	}
	switch *effectState {
	case "succeeded":
		// V2's webhook effect reports success only after its adapter completion.
		return &trueValue, &trueValue, &trueValue
	case "unknown_after_dispatch":
		// The legacy kernel explicitly records that dispatch began but leaves the
		// call/result outcome unresolved.
		return &trueValue, nil, nil
	case "simulated":
		// V2 records simulated work without entering its Provider adapter.
		return &falseValue, &falseValue, &falseValue
	case "blocked", "cancelled":
		// A blocked policy may follow a prior retryable send and V2 permits
		// cancellation from failed_retryable. Their final state therefore cannot
		// prove that no Provider call happened.
		return nil, nil, nil
	default:
		return nil, nil, nil
	}
}

var _ outboundport.CommercePushDeliveryReader = (*CommercePushService)(nil)
