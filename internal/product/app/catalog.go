package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const (
	DefaultLimit        int32 = 50
	MaximumLimit        int32 = 100
	MaximumLegacyOffset int32 = 1_000_000
	cursorOperation           = "listProducts"
)

var (
	ErrInvalidProduct = errors.New("invalid product")
	ErrInvalidCursor  = errors.New("invalid product cursor")
	ErrNotFound       = errors.New("product not found")
	ErrConflict       = errors.New("product command conflict")
	ErrUnavailable    = errors.New("product catalog unavailable")
)

type Receipt struct {
	ID                       int64
	Operation                string
	ActorScope               string
	KeyDigest, PayloadDigest [32]byte
	State                    string
	ResultSnapshot           json.RawMessage
}
type Reservation struct {
	Operation                string
	ActorScope               string
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}
type Store interface {
	List(context.Context, *productport.ID, int32) ([]productport.Product, error)
	ListOffset(context.Context, int32, int32) ([]productport.Product, error)
	Count(context.Context) (int64, error)
	Get(context.Context, productport.ID) (productport.Product, error)
	GetByCode(context.Context, string) (productport.Product, error)
	GetForUpdate(context.Context, productport.ID) (productport.Product, error)
	Create(context.Context, productport.CreateCommand, time.Time) (productport.Product, error)
	Update(context.Context, productport.UpdateCommand, time.Time) (productport.Product, error)
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
}
type Service struct {
	uow    platformport.UnitOfWork
	store  Store
	events productport.EventAppender
	sales  productport.SalesSummaryReader
	policy productPolicyWriter
	now    func() time.Time
}

func NewService(uow platformport.UnitOfWork, store Store, events productport.EventAppender) *Service {
	return &Service{uow: uow, store: store, events: events, now: time.Now}
}

func (s *Service) SetDistributionPolicyWriter(writer distributionport.ProductPolicyService) error {
	if s == nil || writer == nil {
		return ErrUnavailable
	}
	s.policy = writer
	return nil
}

func (s *Service) SetSalesSummaryReader(reader productport.SalesSummaryReader) error {
	if s == nil || reader == nil {
		return ErrUnavailable
	}
	s.sales = reader
	return nil
}

func (s *Service) attachSales(ctx context.Context, products []productport.Product) error {
	if s.sales == nil || len(products) == 0 {
		return nil
	}
	keys := make([]productport.SalesKey, 0, len(products))
	for _, product := range products {
		keys = append(keys, productport.SalesKey{ProductID: product.ID, ProductCode: product.ProductCode})
	}
	summaries, err := s.sales.ReadSalesSummariesWithin(ctx, keys)
	if err != nil {
		return err
	}
	for index := range products {
		summary, ok := summaries[products[index].ID]
		if !ok || summary.PaidOrderCount < 0 || summary.RefundOrderCount < 0 || summary.SoldCount < 0 || summary.SoldCount != max64(0, summary.PaidOrderCount-summary.RefundOrderCount) {
			return ErrUnavailable
		}
		products[index].PaidOrderCount = summary.PaidOrderCount
		products[index].RefundOrderCount = summary.RefundOrderCount
		products[index].SoldCount = summary.SoldCount
	}
	return nil
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func (s *Service) List(ctx context.Context, cursor string, limit int32) (productport.Page, error) {
	if !ready(s) {
		return productport.Page{}, ErrInvalidCursor
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaximumLimit {
		return productport.Page{}, ErrInvalidCursor
	}
	var after *productport.ID
	if cursor != "" {
		id, err := decodeCursor(cursor)
		if err != nil {
			return productport.Page{}, err
		}
		typed := productport.ID(id)
		after = &typed
	}
	var rows []productport.Product
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		rows, err = s.store.List(tx, after, limit+1)
		if err != nil {
			return err
		}
		return s.attachSales(tx, rows)
	}); err != nil {
		return productport.Page{}, classify(err)
	}
	if len(rows) > int(limit)+1 || !validOrdinaryProducts(rows) {
		return productport.Page{}, ErrUnavailable
	}
	page := productport.Page{Items: rows}
	if len(rows) > int(limit) {
		page.Items = rows[:limit]
		page.NextCursor = encodeCursor(int64(page.Items[len(page.Items)-1].ID))
	}
	return page, nil
}

func (s *Service) ListLegacy(ctx context.Context, limit, offset int32) (productport.LegacyPage, error) {
	if !ready(s) || limit < 1 || limit > MaximumLimit || offset < 0 || offset > MaximumLegacyOffset {
		return productport.LegacyPage{}, ErrInvalidCursor
	}
	result := productport.LegacyPage{Limit: limit, Offset: offset}
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result.Items, err = s.store.ListOffset(tx, limit, offset)
		if err != nil {
			return err
		}
		if err = s.attachSales(tx, result.Items); err != nil {
			return err
		}
		result.Total, err = s.store.Count(tx)
		return err
	}); err != nil {
		return productport.LegacyPage{}, classify(err)
	}
	if result.Total < 0 || int64(offset) > result.Total && len(result.Items) != 0 || len(result.Items) > int(limit) || !validOrdinaryProducts(result.Items) {
		return productport.LegacyPage{}, ErrUnavailable
	}
	return result, nil
}

// Get reuses an existing composition transaction when a downstream domain
// performs its final lifecycle check before recording its own fact.  Opening a
// second transaction here would fail closed and make that current-product
// check impossible; the Product store remains the sole owner of its rows.
func (s *Service) Get(ctx context.Context, id productport.ID) (productport.Product, error) {
	if !ready(s) || id < 1 {
		return productport.Product{}, ErrNotFound
	}
	var (
		p   productport.Product
		err error
	)
	if _, transactionErr := platformpostgres.RequireTransaction(ctx); transactionErr == nil {
		p, err = s.getWithin(ctx, id)
	} else {
		err = s.uow.Within(ctx, func(tx context.Context) error {
			p, err = s.getWithin(tx, id)
			return err
		})
	}
	if err != nil {
		return productport.Product{}, classify(err)
	}
	if IsServicePeriodProjection(p.LegacyAdminProjection) {
		return productport.Product{}, ErrNotFound
	}
	if !validProduct(p) {
		return productport.Product{}, ErrUnavailable
	}
	return p, nil
}

func (s *Service) getWithin(ctx context.Context, id productport.ID) (productport.Product, error) {
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return productport.Product{}, err
	}
	values := []productport.Product{p}
	if err = s.attachSales(ctx, values); err != nil {
		return productport.Product{}, err
	}
	return values[0], nil
}

// GetByCode resolves the stable public product identifier. Callers must still
// apply their own lifecycle visibility rule before exposing the result.
func (s *Service) GetByCode(ctx context.Context, code string) (productport.Product, error) {
	if !ready(s) || code == "" || code != strings.TrimSpace(code) || len(code) > 200 {
		return productport.Product{}, ErrNotFound
	}
	var p productport.Product
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		p, err = s.store.GetByCode(tx, code)
		if err != nil {
			return err
		}
		values := []productport.Product{p}
		if err = s.attachSales(tx, values); err != nil {
			return err
		}
		p = values[0]
		return nil
	})
	if err != nil {
		return productport.Product{}, classify(err)
	}
	if IsServicePeriodProjection(p.LegacyAdminProjection) {
		return productport.Product{}, ErrNotFound
	}
	if !validProduct(p) {
		return productport.Product{}, ErrUnavailable
	}
	return p, nil
}
func (s *Service) Create(ctx context.Context, command productport.CreateCommand) (productport.Product, error) {
	command, digest, err := normalize(command)
	if err != nil {
		return productport.Product{}, err
	}
	if IsServicePeriodProjection(command.LegacyAdminProjection) {
		return productport.Product{}, ErrInvalidProduct
	}
	policy, err := normalizedDistributionPolicy(command.DistributionPolicy, true)
	if err != nil {
		return productport.Product{}, err
	}
	if !ready(s) {
		return productport.Product{}, ErrUnavailable
	}
	now := s.now().UTC()
	if now.IsZero() {
		return productport.Product{}, ErrUnavailable
	}
	actor := fmt.Sprintf("admin:%d", command.Actor)
	reservation := Reservation{Operation: "create", ActorScope: actor, KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: digest, CreatedAt: now}
	var result productport.Product
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.store.Reserve(tx, reservation)
		if e != nil {
			return e
		}
		if !validReceipt(receipt, reservation) {
			return ErrUnavailable
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], digest[:]) != 1 {
			return ErrConflict
		}
		if !owned {
			if receipt.State != "completed" {
				return ErrUnavailable
			}
			var snapshot productport.Product
			if json.Unmarshal(receipt.ResultSnapshot, &snapshot) != nil || !validProduct(snapshot) {
				return ErrUnavailable
			}
			canonical, e := snapshotJSON(snapshot)
			if e != nil || !jsonEquivalent(canonical, receipt.ResultSnapshot) {
				return ErrUnavailable
			}
			result = snapshot
			return nil
		}
		result, e = s.store.Create(tx, command, now)
		if e != nil {
			return e
		}
		if !validProduct(result) {
			return ErrUnavailable
		}
		if s.policy != nil {
			if e = saveDistributionPolicyWithin(tx, s.policy, result.ID, distributiondomain.ProductTypeStandard, policy, command.Actor, command.IdempotencyKey); e != nil {
				return e
			}
		}
		snapshot, e := snapshotJSON(result)
		if e != nil {
			return e
		}
		eventPayload, e := json.Marshal(struct {
			ProductID productport.ID `json:"product_id"`
			Actor     int64          `json:"actor"`
		}{result.ID, command.Actor})
		if e != nil {
			return e
		}
		eventDigest := sha256.Sum256([]byte(actor + "\x00" + command.IdempotencyKey))
		if _, e = s.events.Append(tx, productport.Event{Type: productport.EventProductCreated, Payload: eventPayload, OccurredAt: now, IdempotencyKey: "product.create:" + hex.EncodeToString(eventDigest[:])}); e != nil {
			return e
		}
		completed, e := s.store.Complete(tx, receipt.ID, snapshot, now)
		if e != nil || completed.State != "completed" || !jsonEquivalent(snapshot, completed.ResultSnapshot) {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return productport.Product{}, classify(err)
	}
	return result, nil
}

// Update performs a native v2 compare-and-swap. Product code, images, and the
// legacy compatibility projection intentionally remain outside this contract.
func (s *Service) Update(ctx context.Context, command productport.UpdateCommand) (productport.Product, error) {
	command, digest, err := normalizeUpdate(command)
	policy, policyErr := normalizedDistributionPolicy(command.DistributionPolicy, false)
	if policyErr != nil {
		return productport.Product{}, policyErr
	}
	if err != nil || !ready(s) {
		if err != nil {
			return productport.Product{}, err
		}
		return productport.Product{}, ErrUnavailable
	}
	now := s.now().UTC()
	if now.IsZero() {
		return productport.Product{}, ErrUnavailable
	}
	reservation := Reservation{Operation: "update", ActorScope: fmt.Sprintf("admin:%d", command.Actor), KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: digest, CreatedAt: now}
	var result productport.Product
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.store.Reserve(tx, reservation)
		if e != nil {
			return e
		}
		if !validReceipt(receipt, reservation) {
			return ErrUnavailable
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], digest[:]) != 1 {
			return ErrConflict
		}
		if !owned {
			if receipt.State != "completed" {
				return ErrUnavailable
			}
			if json.Unmarshal(receipt.ResultSnapshot, &result) != nil || !validProduct(result) {
				return ErrUnavailable
			}
			canonical, e := snapshotJSON(result)
			if e != nil || !jsonEquivalent(canonical, receipt.ResultSnapshot) {
				return ErrUnavailable
			}
			return nil
		}
		current, e := s.store.GetForUpdate(tx, command.ID)
		if e != nil {
			return e
		}
		if IsServicePeriodProjection(current.LegacyAdminProjection) {
			return ErrNotFound
		}
		if !validProduct(current) {
			return ErrUnavailable
		}
		currentLifecycle, projectionErr := projectLocalProduct(current)
		if projectionErr != nil {
			return projectionErr
		}
		if currentLifecycle.Lifecycle == productport.LocalProductArchived {
			return ErrNotFound
		}
		if current.Version != int64(command.ExpectedVersion) {
			return ErrConflict
		}
		if command.Images == nil {
			command.Images = slices.Clone(current.Images)
		}
		if len(command.LegacyAdminProjection) == 0 {
			command.LegacyAdminProjection = append(json.RawMessage(nil), current.LegacyAdminProjection...)
		}
		nextLifecycle, projectionErr := projectLocalProduct(productport.Product{
			ID: current.ID, ProductCode: current.ProductCode, Name: command.Name, Description: command.Description,
			PriceMinor: command.PriceMinor, Currency: command.Currency, StockQuantity: command.StockQuantity, Images: command.Images,
			CreatedBy: current.CreatedBy, CreatedAt: current.CreatedAt, UpdatedAt: current.UpdatedAt, Version: current.Version,
			LegacyAdminProjection: command.LegacyAdminProjection,
		})
		if projectionErr != nil {
			return projectionErr
		}
		if nextLifecycle.Lifecycle == productport.LocalProductArchived {
			return ErrInvalidProduct
		}
		result, e = s.store.Update(tx, command, now)
		if e != nil {
			return e
		}
		if !validOrdinaryProduct(result) || result.Version != current.Version+1 ||
			result.ProductCode != current.ProductCode || result.CreatedBy != current.CreatedBy ||
			!result.CreatedAt.Equal(current.CreatedAt) || !reflect.DeepEqual(result.Images, command.Images) ||
			!jsonEquivalent(result.LegacyAdminProjection, command.LegacyAdminProjection) ||
			result.Name != command.Name || result.Description != command.Description ||
			result.PriceMinor != command.PriceMinor || result.Currency != command.Currency ||
			result.StockQuantity != command.StockQuantity {
			return ErrUnavailable
		}
		if s.policy != nil {
			if e = saveDistributionPolicyWithin(tx, s.policy, result.ID, distributiondomain.ProductTypeStandard, policy, command.Actor, command.IdempotencyKey); e != nil {
				return e
			}
		}
		snapshot, e := snapshotJSON(result)
		if e != nil {
			return e
		}
		payload, e := json.Marshal(struct {
			ProductID productport.ID `json:"product_id"`
			Version   int64          `json:"version"`
			Actor     int64          `json:"actor"`
		}{result.ID, result.Version, command.Actor})
		if e != nil {
			return e
		}
		eventDigest := sha256.Sum256([]byte(reservation.ActorScope + "\x00" + command.IdempotencyKey))
		if _, e = s.events.Append(tx, productport.Event{Type: productport.EventProductUpdated, Payload: payload, OccurredAt: now, IdempotencyKey: "product.update:" + hex.EncodeToString(eventDigest[:])}); e != nil {
			return e
		}
		completed, e := s.store.Complete(tx, receipt.ID, snapshot, now)
		if e != nil || completed.State != "completed" || !jsonEquivalent(snapshot, completed.ResultSnapshot) {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return productport.Product{}, classify(err)
	}
	return result, nil
}

func ready(s *Service) bool { return s != nil && s.uow != nil && s.store != nil && s.events != nil }

func normalizeUpdate(c productport.UpdateCommand) (productport.UpdateCommand, [32]byte, error) {
	c.Images = slices.Clone(c.Images)
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	c.Currency = strings.ToUpper(strings.TrimSpace(c.Currency))
	var projection json.RawMessage
	var err error
	if len(c.LegacyAdminProjection) > 0 {
		projection, err = CanonicalLegacyAdminProjection(c.LegacyAdminProjection)
	}
	if c.ID < 1 || c.ExpectedVersion < 1 || c.ExpectedVersion == math.MaxInt64 || c.Actor < 1 || c.Name == "" || len(c.Name) > 200 || len(c.Description) > 10000 || c.PriceMinor < 0 || c.StockQuantity < 0 || len(c.Currency) != 3 || len(c.Images) > 20 || !validIdempotencyKey(c.IdempotencyKey) || err != nil {
		return productport.UpdateCommand{}, [32]byte{}, ErrInvalidProduct
	}
	if projection != nil {
		c.LegacyAdminProjection = projection
	}
	for i := range c.Images {
		c.Images[i] = strings.TrimSpace(c.Images[i])
		if c.Images[i] == "" || len(c.Images[i]) > 2048 {
			return productport.UpdateCommand{}, [32]byte{}, ErrInvalidProduct
		}
	}
	canonical, err := json.Marshal(struct {
		ID                          int64
		ExpectedVersion             int64
		Name, Description, Currency string
		PriceMinor                  int64
		StockQuantity               int32
		Images                      []string
		LegacyAdminProjection       json.RawMessage
		DistributionPolicy          *productport.DistributionPolicy
	}{int64(c.ID), c.ExpectedVersion, c.Name, c.Description, c.Currency, c.PriceMinor, c.StockQuantity, c.Images, c.LegacyAdminProjection, c.DistributionPolicy})
	if err != nil {
		return productport.UpdateCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return c, sha256.Sum256(canonical), nil
}

func validIdempotencyKey(key string) bool {
	return len(key) >= 16 && len(key) <= 128 && strings.TrimSpace(key) == key
}

func normalize(c productport.CreateCommand) (productport.CreateCommand, [32]byte, error) {
	c.Images = slices.Clone(c.Images)
	c.Name = strings.TrimSpace(c.Name)
	c.ProductCode = strings.TrimSpace(c.ProductCode)
	c.Description = strings.TrimSpace(c.Description)
	c.Currency = strings.ToUpper(strings.TrimSpace(c.Currency))
	projection, err := CanonicalLegacyAdminProjection(c.LegacyAdminProjection)
	if c.Actor < 1 || c.ProductCode == "" || len(c.ProductCode) > 200 || c.Name == "" || len(c.Name) > 200 || len(c.Description) > 10000 || c.PriceMinor < 0 || c.StockQuantity < 0 || len(c.Currency) != 3 || len(c.Images) > 20 || !validIdempotencyKey(c.IdempotencyKey) || err != nil {
		return productport.CreateCommand{}, [32]byte{}, ErrInvalidProduct
	}
	c.LegacyAdminProjection = projection
	for i := range c.Images {
		c.Images[i] = strings.TrimSpace(c.Images[i])
		if c.Images[i] == "" || len(c.Images[i]) > 2048 {
			return productport.CreateCommand{}, [32]byte{}, ErrInvalidProduct
		}
	}
	canonical, err := json.Marshal(struct {
		ProductCode                 string
		Name, Description, Currency string
		PriceMinor                  int64
		StockQuantity               int32
		Images                      []string
		LegacyAdminProjection       json.RawMessage
		DistributionPolicy          *productport.DistributionPolicy
	}{c.ProductCode, c.Name, c.Description, c.Currency, c.PriceMinor, c.StockQuantity, c.Images, c.LegacyAdminProjection, c.DistributionPolicy})
	if err != nil {
		return productport.CreateCommand{}, [32]byte{}, ErrInvalidProduct
	}
	return c, sha256.Sum256(canonical), nil
}
func validProduct(p productport.Product) bool {
	normalized, _, e := normalize(productport.CreateCommand{ProductCode: p.ProductCode, Name: p.Name, Description: p.Description, Currency: p.Currency, PriceMinor: p.PriceMinor, StockQuantity: p.StockQuantity, Images: p.Images, LegacyAdminProjection: p.LegacyAdminProjection, Actor: p.CreatedBy, IdempotencyKey: strings.Repeat("v", 16)})
	// New disabled projection keys may be introduced after an existing Product
	// was persisted. Canonicalization is lossless for those rows, and 0117
	// backfills this particular pair on upgrade. Do not make an already-paid
	// event fail merely because its old JSON has not yet been rewritten.
	return e == nil && len(normalized.LegacyAdminProjection) > 0 && p.ID > 0 && p.Version > 0 && !p.CreatedAt.IsZero() && !p.UpdatedAt.IsZero() && !p.UpdatedAt.Before(p.CreatedAt)
}
func validProducts(ps []productport.Product) bool {
	var prev productport.ID
	for index, p := range ps {
		if !validProduct(p) || index > 0 && p.ID >= prev {
			return false
		}
		prev = p.ID
	}
	return true
}

func validOrdinaryProduct(product productport.Product) bool {
	return validProduct(product) && !IsServicePeriodProjection(product.LegacyAdminProjection)
}

func validOrdinaryProducts(products []productport.Product) bool {
	for _, product := range products {
		if !validOrdinaryProduct(product) {
			return false
		}
	}
	return validProducts(products)
}
func validReceipt(r Receipt, x Reservation) bool {
	return r.ID > 0 && r.Operation == x.Operation && r.ActorScope == x.ActorScope && subtle.ConstantTimeCompare(r.KeyDigest[:], x.KeyDigest[:]) == 1
}
func snapshotJSON(p productport.Product) (json.RawMessage, error) { return json.Marshal(p) }
func jsonEquivalent(a, b []byte) bool {
	x, ok := decodeJSON(a)
	if !ok {
		return false
	}
	y, ok := decodeJSON(b)
	return ok && reflect.DeepEqual(x, y)
}

func decodeJSON(raw []byte) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	return value, errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}

func DefaultLegacyAdminProjection() json.RawMessage {
	projection, _ := CanonicalLegacyAdminProjection(json.RawMessage(`{"schema_version":1}`))
	return projection
}

// EnabledLegacyAdminProjectionForCreate applies the product-create default
// without changing update, copy, import, or service-period semantics.
func EnabledLegacyAdminProjectionForCreate(raw json.RawMessage) (json.RawMessage, error) {
	canonical, err := CanonicalLegacyAdminProjection(raw)
	if err != nil {
		return nil, err
	}
	var projection map[string]json.RawMessage
	if json.Unmarshal(canonical, &projection) != nil {
		return nil, ErrInvalidProduct
	}
	projection["status"] = json.RawMessage(`"active"`)
	projection["enabled"] = json.RawMessage(`true`)
	return CanonicalLegacyAdminProjection(mustJSON(projection))
}

func mustJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func CanonicalLegacyAdminProjection(raw json.RawMessage) (json.RawMessage, error) {
	defaults := map[string]json.RawMessage{
		"schema_version":              json.RawMessage(`1`),
		"status":                      json.RawMessage(`"draft"`),
		"enabled":                     json.RawMessage(`false`),
		"buy_button_text":             json.RawMessage(`""`),
		"require_mobile":              json.RawMessage(`false`),
		"contact_collection_level":    json.RawMessage(`"none"`),
		"lead_program_id":             json.RawMessage(`null`),
		"lead_channel_id":             json.RawMessage(`null`),
		"lead_qr_title":               json.RawMessage(`""`),
		"lead_qr_subtitle":            json.RawMessage(`""`),
		"completion_redirect_enabled": json.RawMessage(`false`),
		"completion_redirect_url":     json.RawMessage(`""`),
		"completion_target":           json.RawMessage(`null`),
		"purchase_action_enabled":     json.RawMessage(`false`),
		"purchase_action_mode":        json.RawMessage(`""`),
		"wecom_tagging":               json.RawMessage(`{}`),
		"slices":                      json.RawMessage(`[]`),
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var supplied map[string]json.RawMessage
	if len(raw) == 0 || decoder.Decode(&supplied) != nil || supplied == nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return nil, ErrInvalidProduct
	}
	for key, value := range supplied {
		if _, ok := defaults[key]; !ok || !json.Valid(value) {
			return nil, ErrInvalidProduct
		}
		defaults[key] = value
	}
	var schemaVersion int
	if json.Unmarshal(defaults["schema_version"], &schemaVersion) != nil || schemaVersion != 1 {
		return nil, ErrInvalidProduct
	}
	for _, key := range []string{"status", "buy_button_text", "lead_qr_title", "lead_qr_subtitle", "completion_redirect_url", "purchase_action_mode"} {
		var value string
		if json.Unmarshal(defaults[key], &value) != nil || len(value) > 2048 || key == "status" && (strings.TrimSpace(value) == "" || len(value) > 64) {
			return nil, ErrInvalidProduct
		}
	}
	for _, key := range []string{"enabled", "require_mobile", "completion_redirect_enabled", "purchase_action_enabled"} {
		var value bool
		if json.Unmarshal(defaults[key], &value) != nil {
			return nil, ErrInvalidProduct
		}
	}
	var requireMobile bool
	if json.Unmarshal(defaults["require_mobile"], &requireMobile) != nil {
		return nil, ErrInvalidProduct
	}
	level := ""
	if rawLevel, ok := supplied["contact_collection_level"]; ok {
		if json.Unmarshal(rawLevel, &level) != nil || (level != "none" && level != "mobile" && level != "shipping_address") {
			return nil, ErrInvalidProduct
		}
	} else if requireMobile {
		level = "mobile"
	} else {
		level = "none"
	}
	if level != "none" {
		requireMobile = true
	} else {
		requireMobile = false
	}
	defaults["contact_collection_level"] = mustJSON(level)
	defaults["require_mobile"] = mustJSON(requireMobile)
	for _, key := range []string{"lead_program_id", "lead_channel_id"} {
		if string(defaults[key]) == "null" {
			continue
		}
		var value int64
		if json.Unmarshal(defaults[key], &value) != nil || value < 1 {
			return nil, ErrInvalidProduct
		}
	}
	if !jsonKind(defaults["completion_target"], "object", "null") || !jsonKind(defaults["wecom_tagging"], "object", "array", "null") || !jsonKind(defaults["slices"], "array") {
		return nil, ErrInvalidProduct
	}
	if _, err := completionTargetFromProjection(defaults["completion_target"]); err != nil {
		return nil, ErrInvalidProduct
	}
	if !validExplicitPaidPurchaseTagging(defaults["wecom_tagging"]) {
		return nil, ErrInvalidProduct
	}
	var purchaseEnabled bool
	var purchaseMode string
	if json.Unmarshal(defaults["purchase_action_enabled"], &purchaseEnabled) != nil || json.Unmarshal(defaults["purchase_action_mode"], &purchaseMode) != nil ||
		(purchaseMode != "" && purchaseMode != string(productport.PaidPurchaseActionQR) && purchaseMode != string(productport.PaidPurchaseActionRedirect)) ||
		(purchaseEnabled && purchaseMode == "") {
		return nil, ErrInvalidProduct
	}
	// A paid event freezes this configuration after settlement.  Validate every
	// enabled presentation here, at the Product write boundary, so an accepted
	// Product cannot later turn a valid payment fact into a configuration error.
	if purchaseEnabled {
		switch purchaseMode {
		case string(productport.PaidPurchaseActionQR):
			var leadChannelID int64
			if json.Unmarshal(defaults["lead_channel_id"], &leadChannelID) != nil || leadChannelID < 1 {
				return nil, ErrInvalidProduct
			}
		case string(productport.PaidPurchaseActionRedirect):
			target, targetErr := completionTargetFromProjection(defaults["completion_target"])
			if targetErr != nil {
				return nil, ErrInvalidProduct
			}
			var redirectURL string
			if json.Unmarshal(defaults["completion_redirect_url"], &redirectURL) != nil || !target.Enabled && !validPaidPurchaseRedirect(redirectURL) {
				return nil, ErrInvalidProduct
			}
		}
	}
	canonical, err := json.Marshal(defaults)
	if err != nil {
		return nil, ErrInvalidProduct
	}
	return canonical, nil
}

// validExplicitPaidPurchaseTagging only applies the new paid-action contract
// when a configuration explicitly enables tagging. Older projections did not
// carry that switch, so they remain readable and retain their prior semantics.
// A disabled draft may retain selected IDs without making them executable.
func validExplicitPaidPurchaseTagging(raw json.RawMessage) bool {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return true
	}
	enabledRaw, explicit := values["enabled"]
	if !explicit {
		return true
	}
	var enabled bool
	if json.Unmarshal(enabledRaw, &enabled) != nil {
		return false
	}
	if !enabled {
		return true
	}
	tagIDsRaw, present := values["tag_ids"]
	if !present {
		return false
	}
	var tagIDs []int64
	if json.Unmarshal(tagIDsRaw, &tagIDs) != nil || len(tagIDs) == 0 || len(tagIDs) > 100 {
		return false
	}
	seen := make(map[int64]struct{}, len(tagIDs))
	for _, id := range tagIDs {
		if id < 1 {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func jsonKind(raw json.RawMessage, allowed ...string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	kind := "scalar"
	switch value.(type) {
	case nil:
		kind = "null"
	case map[string]any:
		kind = "object"
	case []any:
		kind = "array"
	}
	for _, candidate := range allowed {
		if kind == candidate {
			return true
		}
	}
	return false
}
func encodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorOperation + ":" + fmt.Sprint(id)))
}
func decodeCursor(v string) (int64, error) {
	raw, e := base64.RawURLEncoding.DecodeString(v)
	if e != nil {
		return 0, ErrInvalidCursor
	}
	var id int64
	if _, e = fmt.Sscanf(string(raw), cursorOperation+":%d", &id); e != nil || id < 1 || encodeCursor(id) != v {
		return 0, ErrInvalidCursor
	}
	return id, nil
}
func classify(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, ErrNotFound) || errors.Is(e, ErrConflict) || errors.Is(e, ErrInvalidProduct) {
		return e
	}
	if errors.Is(e, productport.ErrProductReadNotFound) {
		return ErrNotFound
	}
	if errors.Is(e, productport.ErrProductConflict) {
		return ErrConflict
	}
	return ErrUnavailable
}
