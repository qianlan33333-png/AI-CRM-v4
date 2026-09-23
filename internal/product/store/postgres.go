// Package store owns Product persistence.  Both ordinary and service-period
// products live in the products table; the service-period application is only
// a status/projection view over that row.  The store never reads orders,
// payments, entitlements, members, customers, OneID, Media, or Tag tables.
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

var (
	ErrInvalid = errors.New("invalid product persistence command")
)

var _ productport.ProductOptionReader = (*Repository)(nil)
var _ productapp.ProductTargetBatchStore = (*Repository)(nil)
var _ productport.DefinitionImporter = (*Repository)(nil)

// Repository is deliberately transaction-bound.  Every method that touches
// PostgreSQL requires the UnitOfWork context supplied by the caller; there is
// no autocommit fallback.
type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}

func (r *Repository) Within(ctx context.Context, fn func(context.Context) error) error {
	if r == nil || r.uow == nil || fn == nil {
		return ErrInvalid
	}
	return r.uow.Within(ctx, fn)
}

type rowScanner interface {
	Scan(...any) error
}

const productColumns = `id,product_code,name,description,price_minor,currency,stock_quantity,images,created_by,created_at,updated_at,version,legacy_admin_projection`

func scanProduct(row rowScanner) (productport.Product, error) {
	var (
		product    productport.Product
		imagesRaw  []byte
		projection []byte
	)
	err := row.Scan(
		&product.ID, &product.ProductCode, &product.Name, &product.Description,
		&product.PriceMinor, &product.Currency, &product.StockQuantity,
		&imagesRaw, &product.CreatedBy, &product.CreatedAt, &product.UpdatedAt,
		&product.Version, &projection,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return productport.Product{}, productport.ErrProductReadNotFound
	}
	if err != nil {
		return productport.Product{}, mapDatabaseError(err)
	}
	if json.Unmarshal(imagesRaw, &product.Images) != nil || !json.Valid(projection) {
		return productport.Product{}, productport.ErrProductReadUnavailable
	}
	product.LegacyAdminProjection = append(json.RawMessage(nil), projection...)
	if product.Images == nil {
		product.Images = []string{}
	}
	return product, nil
}

func transaction(ctx context.Context) (pgx.Tx, error) {
	return platformpostgres.RequireTransaction(ctx)
}

func mapDatabaseError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return productport.ErrProductReadNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23514":
			return productport.ErrProductConflict
		}
	}
	return err
}

// ordinaryProductKindStatusSQL identifies the ordinary Product projection
// without imposing its current visibility. Historical target/name readers and
// retained configuration reads use this classification so an archived Product
// is not presented as a nonexistent reference.
func ordinaryProductKindStatusSQL() string {
	return `legacy_admin_projection->>'status' NOT IN ('service_period_draft','service_period_enabled','service_period_disabled','service_period_archived')`
}

func ordinaryStatusSQL() string {
	return ordinaryProductKindStatusSQL() + ` AND COALESCE(legacy_admin_projection->>'status','')<>'archived'`
}

func servicePeriodStatusSQL() string {
	return `legacy_admin_projection->>'status' IN ('service_period_draft','service_period_enabled','service_period_disabled','service_period_archived')`
}

// visibleServicePeriodStatusSQL excludes retained archived rows from ordinary
// owner lists and every new-selection reader. Direct service-period reads keep
// servicePeriodStatusSQL so historical projections remain available.
func visibleServicePeriodStatusSQL() string {
	return `legacy_admin_projection->>'status' IN ('service_period_draft','service_period_enabled','service_period_disabled')`
}

func (r *Repository) List(ctx context.Context, after *productport.ID, limit int32) ([]productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > productapp.MaximumLimit+1 {
		return nil, ErrInvalid
	}
	query := `SELECT ` + productColumns + ` FROM products WHERE ` + ordinaryStatusSQL()
	args := []any{}
	if after != nil {
		query += ` AND id < $1`
		args = append(args, int64(*after))
	}
	// Product limits are small (at most 101), so format the placeholder without
	// accepting arbitrary SQL input.
	query += ` ORDER BY id DESC LIMIT $` + itoa(len(args)+1)
	args = append(args, limit)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	defer rows.Close()
	items := make([]productport.Product, 0)
	for rows.Next() {
		item, scanErr := scanProduct(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, mapDatabaseError(rows.Err())
}

func (r *Repository) ListOffset(ctx context.Context, limit, offset int32) ([]productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > productapp.MaximumLimit || offset < 0 || offset > productapp.MaximumLegacyOffset {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+productColumns+` FROM products WHERE `+ordinaryStatusSQL()+` ORDER BY id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	defer rows.Close()
	items := make([]productport.Product, 0)
	for rows.Next() {
		item, scanErr := scanProduct(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, mapDatabaseError(rows.Err())
}

func (r *Repository) Count(ctx context.Context) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE `+ordinaryStatusSQL()).Scan(&count)
	return count, mapDatabaseError(err)
}

// ListProductOptions is the Product-owned, read-only selection projection for
// other domains. It returns only CNY id/name/price facts and classifies each
// row without exposing the Product table or its legacy projection. Callers
// outside Product should use app.Service.ListProductOptions, which supplies
// the UnitOfWork boundary; this repository method remains transaction-bound.
func (r *Repository) ListProductOptions(ctx context.Context, query productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.ProductOptionPage{}, err
	}
	query.Q = strings.TrimSpace(query.Q)
	if len(query.Q) > 80 {
		return productport.ProductOptionPage{}, ErrInvalid
	}
	if query.ProductType == "" {
		query.ProductType = productport.ProductOptionAll
	}
	if query.ProductType != productport.ProductOptionAll && query.ProductType != productport.ProductOptionStandard && query.ProductType != productport.ProductOptionServicePeriod {
		return productport.ProductOptionPage{}, ErrInvalid
	}
	if query.Limit == 0 {
		query.Limit = productport.ProductOptionDefaultLimit
	}
	if query.Limit < 1 || query.Limit > productport.ProductOptionMaximumLimit || query.Offset < 0 || query.Offset > productport.ProductOptionMaximumOffset {
		return productport.ProductOptionPage{}, ErrInvalid
	}

	// The unqualified chooser is still a current/new-selection reader. It must
	// filter both owner projections; otherwise an archived item reappears as
	// soon as the consumer omits product_type.
	statusSQL := `(` + ordinaryStatusSQL() + `) OR (` + visibleServicePeriodStatusSQL() + `)`
	switch query.ProductType {
	case productport.ProductOptionStandard:
		statusSQL = ordinaryStatusSQL()
	case productport.ProductOptionServicePeriod:
		statusSQL = visibleServicePeriodStatusSQL()
	}
	whereSQL := "currency='CNY' AND (" + statusSQL + ")"
	args := make([]any, 0, 3)
	if query.Q != "" {
		args = append(args, "%"+escapeProductOptionSearch(query.Q)+"%")
		whereSQL += " AND (name ILIKE $1 ESCAPE '\\' OR product_code ILIKE $1 ESCAPE '\\')"
	}
	var total int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return productport.ProductOptionPage{}, mapDatabaseError(err)
	}

	limitPlaceholder := len(args) + 1
	offsetPlaceholder := len(args) + 2
	args = append(args, query.Limit, query.Offset)
	rows, err := tx.Query(ctx, `SELECT id,product_code,name,price_minor,currency,CASE WHEN `+servicePeriodStatusSQL()+` THEN 'service_period' ELSE 'standard' END FROM products WHERE `+whereSQL+` ORDER BY id LIMIT $`+itoa(limitPlaceholder)+` OFFSET $`+itoa(offsetPlaceholder), args...)
	if err != nil {
		return productport.ProductOptionPage{}, mapDatabaseError(err)
	}
	defer rows.Close()
	items := make([]productport.ProductOption, 0)
	for rows.Next() {
		var item productport.ProductOption
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.PriceMinor, &item.Currency, &item.ProductType); err != nil {
			return productport.ProductOptionPage{}, mapDatabaseError(err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return productport.ProductOptionPage{}, mapDatabaseError(err)
	}
	return productport.ProductOptionPage{Items: items, Total: total, Limit: query.Limit, Offset: query.Offset}, nil
}

// ReadProductTargets resolves a whole valid Coupon list page in one
// Product-owned SQL round trip. The status predicates retain the persisted
// target type: a row that was deleted or changed to the other Product type is
// deliberately reported as absent for the historical reference.
func (r *Repository) ReadProductTargets(ctx context.Context, references []productport.ProductTargetReference) ([]productport.ProductTargetLookup, error) {
	if r == nil || len(references) > productport.ProductTargetBatchMaximum {
		return nil, ErrInvalid
	}
	if len(references) == 0 {
		return []productport.ProductTargetLookup{}, nil
	}
	standardIDs := make([]int64, 0, len(references))
	servicePeriodIDs := make([]int64, 0, len(references))
	expected := make(map[productport.ProductTargetReference]struct{}, len(references))
	for _, reference := range references {
		if reference.ID < 1 || (reference.ProductType != productport.ProductOptionStandard && reference.ProductType != productport.ProductOptionServicePeriod) {
			return nil, ErrInvalid
		}
		if _, duplicate := expected[reference]; duplicate {
			return nil, ErrInvalid
		}
		expected[reference] = struct{}{}
		if reference.ProductType == productport.ProductOptionStandard {
			standardIDs = append(standardIDs, int64(reference.ID))
		} else {
			servicePeriodIDs = append(servicePeriodIDs, int64(reference.ID))
		}
	}
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,name,'standard'::text AS product_type FROM products
WHERE id=ANY($1::bigint[]) AND `+ordinaryProductKindStatusSQL()+`
UNION ALL
SELECT id,name,'service_period'::text AS product_type FROM products
WHERE id=ANY($2::bigint[]) AND `+servicePeriodStatusSQL(), standardIDs, servicePeriodIDs)
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	defer rows.Close()
	found := make(map[productport.ProductTargetReference]string, len(references))
	for rows.Next() {
		var id int64
		var name, kind string
		if err := rows.Scan(&id, &name, &kind); err != nil {
			return nil, mapDatabaseError(err)
		}
		reference := productport.ProductTargetReference{ProductType: productport.ProductOptionType(kind), ID: productport.ID(id)}
		if _, ok := expected[reference]; !ok || strings.TrimSpace(name) == "" {
			return nil, productport.ErrProductReadUnavailable
		}
		if _, duplicate := found[reference]; duplicate {
			return nil, productport.ErrProductReadUnavailable
		}
		found[reference] = name
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError(err)
	}
	result := make([]productport.ProductTargetLookup, 0, len(references))
	for _, reference := range references {
		name, ok := found[reference]
		result = append(result, productport.ProductTargetLookup{Reference: reference, Name: name, Found: ok})
	}
	return result, nil
}

func escapeProductOptionSearch(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func (r *Repository) Get(ctx context.Context, id productport.ID) (productport.Product, error) {
	return r.get(ctx, id, false, "")
}

func (r *Repository) GetByCode(ctx context.Context, code string) (productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.Product{}, err
	}
	if code == "" || code != strings.TrimSpace(code) || len(code) > 200 {
		return productport.Product{}, productport.ErrProductReadNotFound
	}
	return scanProduct(tx.QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE product_code=$1`, code))
}

func (r *Repository) GetForUpdate(ctx context.Context, id productport.ID) (productport.Product, error) {
	return r.get(ctx, id, true, "")
}

func (r *Repository) get(ctx context.Context, id productport.ID, forUpdate bool, statusSQL string) (productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.Product{}, err
	}
	if id < 1 {
		return productport.Product{}, productport.ErrProductReadNotFound
	}
	query := `SELECT ` + productColumns + ` FROM products WHERE id=$1`
	if statusSQL != "" {
		query += ` AND ` + statusSQL
	}
	if forUpdate {
		query += ` FOR UPDATE`
	}
	return scanProduct(tx.QueryRow(ctx, query, int64(id)))
}

func (r *Repository) Create(ctx context.Context, command productport.CreateCommand, now time.Time) (productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.Product{}, err
	}
	if command.Actor < 1 || command.ProductCode == "" || command.Name == "" || len(command.LegacyAdminProjection) == 0 || now.IsZero() {
		return productport.Product{}, ErrInvalid
	}
	images, err := json.Marshal(command.Images)
	if err != nil {
		return productport.Product{}, ErrInvalid
	}
	return scanProduct(tx.QueryRow(ctx, `INSERT INTO products (`+productColumns+`) VALUES (DEFAULT,$1,$2,$3,$4,$5,$6,$7,$8,$9,$9,1,$10) RETURNING `+productColumns,
		command.ProductCode, command.Name, command.Description, command.PriceMinor, command.Currency,
		command.StockQuantity, images, command.Actor, now.UTC(), command.LegacyAdminProjection))
}

// ImportDefinition writes a migration-approved local product definition using
// the caller's existing Unit of Work. It intentionally bypasses normal command
// receipts/events because batch provenance is recorded by the migration owner.
func (r *Repository) ImportDefinition(ctx context.Context, input productport.DefinitionImport) (productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.Product{}, err
	}
	if input.Actor < 1 || input.ProductCode == "" || len(input.ProductCode) > 200 || input.Name == "" || len(input.Name) > 200 ||
		len(input.Description) > 10000 || input.PriceMinor < 0 || input.StockQuantity < 0 || len(input.Currency) != 3 ||
		input.ServicePeriodDurationDays < 0 ||
		len(input.Images) != 0 || input.CreatedAt.IsZero() || input.UpdatedAt.IsZero() || input.UpdatedAt.Before(input.CreatedAt) ||
		!json.Valid(input.LegacyAdminProjection) {
		return productport.Product{}, ErrInvalid
	}
	canonical, err := productapp.CanonicalLegacyAdminProjection(input.LegacyAdminProjection)
	if err != nil {
		return productport.Product{}, ErrInvalid
	}
	var projection map[string]json.RawMessage
	if json.Unmarshal(canonical, &projection) != nil || projection == nil {
		return productport.Product{}, ErrInvalid
	}
	for _, key := range []string{"slices", "wecom_tagging"} {
		var value any
		if json.Unmarshal(projection[key], &value) != nil || !emptyProjectionValue(value) {
			return productport.Product{}, ErrInvalid
		}
	}
	for _, key := range []string{"lead_program_id", "lead_channel_id", "completion_target"} {
		if string(projection[key]) != "null" {
			return productport.Product{}, ErrInvalid
		}
	}
	var lifecycle struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(canonical, &lifecycle) != nil || strings.HasPrefix(lifecycle.Status, "service_period_") != (input.ServicePeriodDurationDays > 0) {
		return productport.Product{}, ErrInvalid
	}
	images, err := json.Marshal([]string{})
	if err != nil {
		return productport.Product{}, ErrInvalid
	}
	product, err := scanProduct(tx.QueryRow(ctx, `INSERT INTO products (product_code,name,description,price_minor,currency,stock_quantity,images,created_by,created_at,updated_at,version,legacy_admin_projection)
		VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,1,$11::jsonb) RETURNING `+productColumns,
		input.ProductCode, input.Name, input.Description, input.PriceMinor, input.Currency, input.StockQuantity, images,
		input.Actor, input.CreatedAt.UTC(), input.UpdatedAt.UTC(), canonical))
	if err != nil {
		return productport.Product{}, err
	}
	if input.ServicePeriodDurationDays > 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO product_imported_service_period_definitions(product_id,duration_days) VALUES($1,$2)`, product.ID, input.ServicePeriodDurationDays); err != nil {
			return productport.Product{}, err
		}
	}
	return product, nil
}

func emptyProjectionValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func (r *Repository) Update(ctx context.Context, command productport.UpdateCommand, now time.Time) (productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.Product{}, err
	}
	if command.ID < 1 || command.ExpectedVersion < 1 || command.Actor < 1 || command.Name == "" || len(command.LegacyAdminProjection) == 0 || now.IsZero() {
		return productport.Product{}, ErrInvalid
	}
	images, err := json.Marshal(command.Images)
	if err != nil {
		return productport.Product{}, ErrInvalid
	}
	return scanProduct(tx.QueryRow(ctx, `UPDATE products SET name=$2,description=$3,price_minor=$4,currency=$5,stock_quantity=$6,images=$7,legacy_admin_projection=$8,updated_at=$9,version=version+1 WHERE id=$1 AND version=$10 RETURNING `+productColumns,
		int64(command.ID), command.Name, command.Description, command.PriceMinor, command.Currency,
		command.StockQuantity, images, command.LegacyAdminProjection, now.UTC(), command.ExpectedVersion))
}

func (r *Repository) UpdateLocalProductLifecycle(ctx context.Context, update productapp.LocalProductLifecycleStoreUpdate, now time.Time) (productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.Product{}, err
	}
	if update.ID < 1 || update.ExpectedVersion < 1 || len(update.LegacyAdminProjection) == 0 || now.IsZero() {
		return productport.Product{}, ErrInvalid
	}
	return scanProduct(tx.QueryRow(ctx, `UPDATE products SET legacy_admin_projection=$3,updated_at=$4,version=version+1 WHERE id=$1 AND version=$2 RETURNING `+productColumns,
		int64(update.ID), update.ExpectedVersion, update.LegacyAdminProjection, now.UTC()))
}

func (r *Repository) DeleteLocalProductIfSafe(ctx context.Context, id productport.ID, expectedVersion int64) (bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return false, err
	}
	if id < 1 || expectedVersion < 1 {
		return false, ErrInvalid
	}
	var deletedID int64
	err = tx.QueryRow(ctx, `DELETE FROM products AS p
WHERE p.id=$1 AND p.version=$2
  AND p.legacy_admin_projection->>'status'='draft'
  AND p.legacy_admin_projection->>'enabled'='false'
  AND NOT EXISTS (SELECT 1 FROM product_external_push_configurations c WHERE c.product_id=p.id)
  AND NOT EXISTS (SELECT 1 FROM product_external_push_tests t WHERE t.product_id=p.id)
RETURNING p.id`, int64(id), expectedVersion).Scan(&deletedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return deletedID == int64(id), mapDatabaseError(err)
}

func (r *Repository) ListServicePeriodProducts(ctx context.Context, limit, offset int32) ([]productport.Product, int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, 0, err
	}
	if limit < 1 || limit > productapp.MaximumLimit || offset < 0 || offset > productapp.MaximumLegacyOffset {
		return nil, 0, ErrInvalid
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE `+visibleServicePeriodStatusSQL()).Scan(&total); err != nil {
		return nil, 0, mapDatabaseError(err)
	}
	rows, err := tx.Query(ctx, `SELECT `+productColumns+` FROM products WHERE `+visibleServicePeriodStatusSQL()+` ORDER BY id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, mapDatabaseError(err)
	}
	defer rows.Close()
	items := make([]productport.Product, 0)
	for rows.Next() {
		item, scanErr := scanProduct(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, item)
	}
	return items, total, mapDatabaseError(rows.Err())
}

func (r *Repository) GetServicePeriodProduct(ctx context.Context, id productport.ID) (productport.Product, error) {
	return r.get(ctx, id, false, servicePeriodStatusSQL())
}

func (r *Repository) GetServicePeriodProductByCode(ctx context.Context, code string) (productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.Product{}, err
	}
	if code == "" || len(code) > 200 {
		return productport.Product{}, ErrInvalid
	}
	return scanProduct(tx.QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE product_code=$1 AND `+servicePeriodStatusSQL(), code))
}

func (r *Repository) GetServicePeriodProductForUpdate(ctx context.Context, id productport.ID) (productport.Product, error) {
	return r.get(ctx, id, true, servicePeriodStatusSQL())
}

// ReadServicePeriodDuration intentionally reuses the original Product-owned
// imported-term table. It is now a trusted lifecycle read as well as an import
// preservation projection; no duplicate service-period aggregate is created.
func (r *Repository) ReadServicePeriodDuration(ctx context.Context, id productport.ID) (int32, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	if id < 1 {
		return 0, ErrInvalid
	}
	var duration int32
	err = tx.QueryRow(ctx, `SELECT duration_days FROM product_imported_service_period_definitions WHERE product_id=$1`, id).Scan(&duration)
	if err != nil {
		return 0, mapDatabaseError(err)
	}
	if duration < 1 {
		return 0, ErrInvalid
	}
	return duration, nil
}

// SetServicePeriodDuration is reachable only from Product's local lifecycle
// UoW. Upsert keeps imported definitions readable while allowing a newly
// created or edited service-period product to persist its duration atomically
// with Product receipts, audit and outbox facts.
func (r *Repository) SetServicePeriodDuration(ctx context.Context, id productport.ID, duration int32) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if id < 1 || duration < 1 {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_imported_service_period_definitions(product_id,duration_days) VALUES($1,$2) ON CONFLICT(product_id) DO UPDATE SET duration_days=EXCLUDED.duration_days`, id, duration)
	return mapDatabaseError(err)
}

func (r *Repository) UpdateServicePeriodProduct(ctx context.Context, update productapp.ServicePeriodStoreUpdate, now time.Time) (productport.Product, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.Product{}, err
	}
	if update.ID < 1 || update.ExpectedVersion < 1 || update.Name == "" || len(update.LegacyAdminProjection) == 0 || now.IsZero() {
		return productport.Product{}, ErrInvalid
	}
	// A zero-image product must persist a JSON array, never JSON null.
	imageList := update.Images
	if imageList == nil {
		imageList = []string{}
	}
	images, err := json.Marshal(imageList)
	if err != nil {
		return productport.Product{}, ErrInvalid
	}
	return scanProduct(tx.QueryRow(ctx, `UPDATE products SET name=$2,description=$3,price_minor=$4,currency=$5,stock_quantity=$6,images=$7,legacy_admin_projection=$8,updated_at=$9,version=version+1 WHERE id=$1 AND version=$10 AND `+servicePeriodStatusSQL()+` RETURNING `+productColumns,
		int64(update.ID), update.Name, update.Description, update.PriceMinor, update.Currency,
		update.StockQuantity, images, update.LegacyAdminProjection, now.UTC(), update.ExpectedVersion))
}

func (r *Repository) Reserve(ctx context.Context, reservation productapp.Reservation) (productapp.Receipt, bool, error) {
	return r.reserve(ctx, reservation)
}

func (r *Repository) ReserveCommerceExternalPush(ctx context.Context, reservation productapp.Reservation) (productapp.Receipt, bool, error) {
	return r.reserve(ctx, reservation)
}

func (r *Repository) reserve(ctx context.Context, reservation productapp.Reservation) (productapp.Receipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productapp.Receipt{}, false, err
	}
	if reservation.Operation == "" || reservation.ActorScope == "" || reservation.CreatedAt.IsZero() {
		return productapp.Receipt{}, false, ErrInvalid
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO product_operation_receipts(operation,actor_scope,idempotency_key_digest,payload_digest,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(operation,actor_scope,idempotency_key_digest) DO NOTHING RETURNING id`,
		reservation.Operation, reservation.ActorScope, reservation.KeyDigest[:], reservation.PayloadDigest[:], reservation.CreatedAt.UTC()).Scan(&id)
	if err == nil {
		return productapp.Receipt{ID: id, Operation: reservation.Operation, ActorScope: reservation.ActorScope, KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "in_progress"}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return productapp.Receipt{}, false, mapDatabaseError(err)
	}
	return r.readReceipt(ctx, reservation.Operation, reservation.ActorScope, reservation.KeyDigest)
}

func (r *Repository) readReceipt(ctx context.Context, operation, actorScope string, keyDigest [32]byte) (productapp.Receipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productapp.Receipt{}, false, err
	}
	var (
		receipt        productapp.Receipt
		keyRaw, payRaw []byte
		result         []byte
	)
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_scope,idempotency_key_digest,payload_digest,state,COALESCE(result_snapshot,'null'::jsonb) FROM product_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND idempotency_key_digest=$3 FOR UPDATE`, operation, actorScope, keyDigest[:]).Scan(&receipt.ID, &receipt.Operation, &receipt.ActorScope, &keyRaw, &payRaw, &receipt.State, &result)
	if err != nil {
		return productapp.Receipt{}, false, mapDatabaseError(err)
	}
	if len(keyRaw) != len(keyDigest) || len(payRaw) != len(keyDigest) {
		return productapp.Receipt{}, false, productport.ErrProductReadUnavailable
	}
	copy(receipt.KeyDigest[:], keyRaw)
	copy(receipt.PayloadDigest[:], payRaw)
	if string(result) != "null" {
		receipt.ResultSnapshot = append(json.RawMessage(nil), result...)
	}
	return receipt, false, nil
}

func (r *Repository) Complete(ctx context.Context, receiptID int64, snapshot json.RawMessage, now time.Time) (productapp.Receipt, error) {
	return r.complete(ctx, receiptID, snapshot, now)
}

func (r *Repository) CompleteCommerceExternalPush(ctx context.Context, receiptID int64, snapshot json.RawMessage, now time.Time) (productapp.Receipt, error) {
	return r.complete(ctx, receiptID, snapshot, now)
}

func (r *Repository) complete(ctx context.Context, receiptID int64, snapshot json.RawMessage, now time.Time) (productapp.Receipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productapp.Receipt{}, err
	}
	if receiptID < 1 || !json.Valid(snapshot) || now.IsZero() {
		return productapp.Receipt{}, ErrInvalid
	}
	var receipt productapp.Receipt
	var keyRaw, payRaw, result []byte
	err = tx.QueryRow(ctx, `UPDATE product_operation_receipts SET state='completed',result_snapshot=$2::jsonb,completed_at=$3 WHERE id=$1 AND state='in_progress' RETURNING id,operation,actor_scope,idempotency_key_digest,payload_digest,state,result_snapshot`, receiptID, snapshot, now.UTC()).Scan(&receipt.ID, &receipt.Operation, &receipt.ActorScope, &keyRaw, &payRaw, &receipt.State, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return productapp.Receipt{}, productport.ErrProductConflict
	}
	if err != nil {
		return productapp.Receipt{}, mapDatabaseError(err)
	}
	if len(keyRaw) != 32 || len(payRaw) != 32 {
		return productapp.Receipt{}, productport.ErrProductReadUnavailable
	}
	copy(receipt.KeyDigest[:], keyRaw)
	copy(receipt.PayloadDigest[:], payRaw)
	receipt.ResultSnapshot = append(json.RawMessage(nil), result...)
	return receipt, nil
}

func validExternalKind(kind productport.ExternalPushProductKind) bool {
	return kind == productport.ExternalPushWeChatPay || kind == productport.ExternalPushServicePeriod
}

func validExternalPushTestDeliveryID(value string) bool {
	if !strings.HasPrefix(value, "commerce_test_") || len(value) != len("commerce_test_")+32 {
		return false
	}
	for _, character := range value[len("commerce_test_"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func serviceKindStatus(kind productport.ExternalPushProductKind) string {
	if kind == productport.ExternalPushServicePeriod {
		return servicePeriodStatusSQL()
	}
	return ordinaryProductKindStatusSQL()
}

// visibleServiceKindStatus is used only by a prospective external-push write
// or test. Historical configuration GETs intentionally use serviceKindStatus
// above so an archived Product remains explainable after it leaves discovery.
func visibleServiceKindStatus(kind productport.ExternalPushProductKind) string {
	if kind == productport.ExternalPushServicePeriod {
		return visibleServicePeriodStatusSQL()
	}
	return ordinaryStatusSQL()
}

func (r *Repository) ReadCommerceExternalPushConfiguration(ctx context.Context, id productport.ID, kind productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	return r.readExternalPushConfiguration(ctx, id, kind, false)
}

func (r *Repository) LockCommerceExternalPushConfiguration(ctx context.Context, id productport.ID, kind productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	return r.readExternalPushConfiguration(ctx, id, kind, true)
}

func (r *Repository) readExternalPushConfiguration(ctx context.Context, id productport.ID, kind productport.ExternalPushProductKind, forUpdate bool) (productport.ExternalPushConfiguration, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.ExternalPushConfiguration{}, err
	}
	if id < 1 || !validExternalKind(kind) {
		return productport.ExternalPushConfiguration{}, ErrInvalid
	}
	if forUpdate {
		// Lock the durable parent before reading the optional configuration.  A
		// LEFT JOIN ... FOR UPDATE OF p has its MVCC snapshot before it waits for
		// the parent lock, so a concurrent first insert can remain invisible and
		// allow two ExpectedRevision=0 saves.  Every writer serializes on this
		// Product lock; this separate read therefore observes the committed first
		// configuration before the application performs its CAS check.
		var lockedID int64
		err = tx.QueryRow(ctx, `SELECT p.id FROM products AS p
WHERE p.id=$1 AND `+visibleServiceKindStatus(kind)+` FOR UPDATE`, int64(id)).Scan(&lockedID)
		if errors.Is(err, pgx.ErrNoRows) {
			return productport.ExternalPushConfiguration{}, productport.ErrProductReadNotFound
		}
		if err != nil {
			return productport.ExternalPushConfiguration{}, mapDatabaseError(err)
		}
	}
	query := `SELECT p.id,COALESCE(c.enabled,FALSE),COALESCE(c.configuration_reference,''),COALESCE(c.push_type,''),c.day,c.frequency,c.expires_at_ts,COALESCE(c.remark,''),COALESCE(c.custom_params,'{}'::jsonb),c.field_mapping,COALESCE(c.version,0),COALESCE(c.updated_at,p.updated_at)
FROM products p LEFT JOIN product_external_push_configurations c ON c.product_id=p.id AND c.product_kind=$2
WHERE p.id=$1 AND ` + serviceKindStatus(kind)
	var result productport.ExternalPushConfiguration
	var enabled bool
	var customRaw, mappingRaw []byte
	err = tx.QueryRow(ctx, query, int64(id), string(kind)).Scan(&result.ProductID, &enabled, &result.ConfigurationReference, &result.PushType, &result.Day, &result.Frequency, &result.ExpiresAtTS, &result.Remark, &customRaw, &mappingRaw, &result.Revision, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return productport.ExternalPushConfiguration{}, productport.ErrProductReadNotFound
	}
	if err != nil {
		return productport.ExternalPushConfiguration{}, mapDatabaseError(err)
	}
	result.ProductKind, result.Enabled = kind, enabled
	if result.CustomParams, err = decodeCommerceExternalPushParams(customRaw); err != nil {
		return productport.ExternalPushConfiguration{}, productport.ErrProductReadUnavailable
	}
	if len(mappingRaw) > 0 {
		result.FieldMapping, err = productport.DecodeFieldMapping(mappingRaw)
		if err != nil {
			return productport.ExternalPushConfiguration{}, productport.ErrProductReadUnavailable
		}
	}
	return result, nil
}

func (r *Repository) ReadCommerceExternalPushConfigurationForOrder(ctx context.Context, id productport.ID) (productport.ExternalPushConfiguration, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.ExternalPushConfiguration{}, err
	}
	if id < 1 {
		return productport.ExternalPushConfiguration{}, ErrInvalid
	}
	// Lock the Product first, then issue a second statement for the optional
	// configuration.  Payment freezes a configuration under this lock; it must
	// not inherit a pre-wait LEFT JOIN snapshot when a concurrent admin save
	// creates the first configuration row.
	var result productport.ExternalPushConfiguration
	err = tx.QueryRow(ctx, `SELECT p.id,
  CASE WHEN p.legacy_admin_projection ->> 'status' IN ('service_period_draft','service_period_enabled','service_period_disabled','service_period_archived') THEN 'service_period' ELSE 'wechat_pay' END,
  p.name
FROM products AS p
WHERE p.id=$1
FOR UPDATE`, int64(id)).Scan(&result.ProductID, &result.ProductKind, &result.ProductName)
	if errors.Is(err, pgx.ErrNoRows) {
		return productport.ExternalPushConfiguration{}, productport.ErrProductReadNotFound
	}
	if err != nil {
		return productport.ExternalPushConfiguration{}, mapDatabaseError(err)
	}
	var customRaw, mappingRaw []byte
	err = tx.QueryRow(ctx, `SELECT COALESCE(c.enabled,FALSE),COALESCE(c.configuration_reference,''),COALESCE(c.push_type,''),c.day,c.frequency,c.expires_at_ts,COALESCE(c.remark,''),COALESCE(c.custom_params,'{}'::jsonb),c.field_mapping,COALESCE(c.version,0),COALESCE(c.updated_at,p.updated_at)
FROM products AS p
LEFT JOIN product_external_push_configurations AS c ON c.product_id=p.id AND c.product_kind=$2
WHERE p.id=$1`, int64(id), string(result.ProductKind)).Scan(&result.Enabled, &result.ConfigurationReference, &result.PushType, &result.Day, &result.Frequency, &result.ExpiresAtTS, &result.Remark, &customRaw, &mappingRaw, &result.Revision, &result.UpdatedAt)
	if err != nil {
		return productport.ExternalPushConfiguration{}, mapDatabaseError(err)
	}
	if result.CustomParams, err = decodeCommerceExternalPushParams(customRaw); err != nil {
		return productport.ExternalPushConfiguration{}, productport.ErrProductReadUnavailable
	}
	if len(mappingRaw) > 0 {
		result.FieldMapping, err = productport.DecodeFieldMapping(mappingRaw)
		if err != nil {
			return productport.ExternalPushConfiguration{}, productport.ErrProductReadUnavailable
		}
	}
	return result, nil
}

func (r *Repository) SaveCommerceExternalPushConfiguration(ctx context.Context, value productport.ExternalPushConfiguration, now time.Time) (productport.ExternalPushConfiguration, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.ExternalPushConfiguration{}, err
	}
	if value.ProductID < 1 || !validExternalKind(value.ProductKind) || now.IsZero() {
		return productport.ExternalPushConfiguration{}, ErrInvalid
	}
	var productID int64
	err = tx.QueryRow(ctx, `SELECT id FROM products WHERE id=$1 AND `+visibleServiceKindStatus(value.ProductKind)+` FOR UPDATE`, int64(value.ProductID)).Scan(&productID)
	if errors.Is(err, pgx.ErrNoRows) {
		return productport.ExternalPushConfiguration{}, productport.ErrProductReadNotFound
	}
	if err != nil {
		return productport.ExternalPushConfiguration{}, mapDatabaseError(err)
	}
	customParams, err := encodeCommerceExternalPushParams(value.CustomParams)
	if err != nil {
		return productport.ExternalPushConfiguration{}, ErrInvalid
	}
	var mapping any
	if value.FieldMapping != nil {
		if productport.ValidateFieldMapping(value.FieldMapping) != nil {
			return productport.ExternalPushConfiguration{}, ErrInvalid
		}
		raw, _ := json.Marshal(value.FieldMapping)
		mapping = raw
	}
	var result productport.ExternalPushConfiguration
	var customRaw, mappingRaw []byte
	err = tx.QueryRow(ctx, `INSERT INTO product_external_push_configurations(product_id,product_kind,enabled,configuration_reference,push_type,day,frequency,expires_at_ts,remark,custom_params,field_mapping,version,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$13::jsonb,1,$11)
ON CONFLICT(product_id,product_kind) DO UPDATE SET enabled=EXCLUDED.enabled,configuration_reference=EXCLUDED.configuration_reference,push_type=EXCLUDED.push_type,day=EXCLUDED.day,frequency=EXCLUDED.frequency,expires_at_ts=EXCLUDED.expires_at_ts,remark=EXCLUDED.remark,custom_params=EXCLUDED.custom_params,field_mapping=EXCLUDED.field_mapping,version=product_external_push_configurations.version+1,updated_at=EXCLUDED.updated_at
WHERE product_external_push_configurations.version=$12
RETURNING product_id,product_kind,enabled,configuration_reference,push_type,day,frequency,expires_at_ts,remark,custom_params,field_mapping,version,updated_at`, productID, string(value.ProductKind), value.Enabled, value.ConfigurationReference, value.PushType, value.Day, value.Frequency, value.ExpiresAtTS, value.Remark, customParams, now.UTC(), value.Revision, mapping).Scan(&result.ProductID, &result.ProductKind, &result.Enabled, &result.ConfigurationReference, &result.PushType, &result.Day, &result.Frequency, &result.ExpiresAtTS, &result.Remark, &customRaw, &mappingRaw, &result.Revision, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return productport.ExternalPushConfiguration{}, productport.ErrProductConflict
	}
	if err != nil {
		return productport.ExternalPushConfiguration{}, mapDatabaseError(err)
	}
	if result.CustomParams, err = decodeCommerceExternalPushParams(customRaw); err != nil {
		return productport.ExternalPushConfiguration{}, productport.ErrProductReadUnavailable
	}
	if len(mappingRaw) > 0 {
		result.FieldMapping, err = productport.DecodeFieldMapping(mappingRaw)
		if err != nil {
			return productport.ExternalPushConfiguration{}, productport.ErrProductReadUnavailable
		}
	}
	return result, nil
}

func encodeCommerceExternalPushParams(value map[string]any) ([]byte, error) {
	if value == nil {
		value = map[string]any{}
	}
	raw, err := json.Marshal(value)
	if err != nil || !json.Valid(raw) {
		return nil, ErrInvalid
	}
	return raw, nil
}

func decodeCommerceExternalPushParams(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || value == nil {
		return nil, ErrInvalid
	}
	return value, nil
}

func (r *Repository) CommerceExternalPushTestExists(ctx context.Context, id productport.ID, kind productport.ExternalPushProductKind, configurationDigest [32]byte) (bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return false, err
	}
	if id < 1 || !validExternalKind(kind) {
		return false, ErrInvalid
	}
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_external_push_tests WHERE product_id=$1 AND product_kind=$2 AND configuration_digest=$3)`, int64(id), string(kind), configurationDigest[:]).Scan(&exists)
	return exists, mapDatabaseError(err)
}

func (r *Repository) ListCommerceExternalPushTests(ctx context.Context, id productport.ID, kind productport.ExternalPushProductKind, limit int32) ([]productport.ExternalPushTest, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if id < 1 || !validExternalKind(kind) || limit < 1 || limit > 20 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT product_id,product_kind,effect_id,delivery_id,state,provider_accepted,delivery_proven,real_external_call_executed,auto_retry_allowed,created_at
FROM product_external_push_tests WHERE product_id=$1 AND product_kind=$2
ORDER BY created_at DESC,id DESC LIMIT $3`, int64(id), string(kind), limit)
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	defer rows.Close()
	values := make([]productport.ExternalPushTest, 0, limit)
	for rows.Next() {
		var value productport.ExternalPushTest
		if err = rows.Scan(&value.ProductID, &value.ProductKind, &value.EffectID, &value.DeliveryID, &value.State, &value.ProviderAccepted, &value.DeliveryProven, &value.RealExternalCallExecuted, &value.AutoRetryAllowed, &value.CreatedAt); err != nil {
			return nil, mapDatabaseError(err)
		}
		value.UpdatedAt = value.CreatedAt
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapDatabaseError(err)
	}
	return values, nil
}

func (r *Repository) CreateCommerceExternalPushTest(ctx context.Context, value productport.ExternalPushTest, configurationDigest [32]byte, receiptID int64) (productport.ExternalPushTest, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return productport.ExternalPushTest{}, err
	}
	if value.ProductID < 1 || !validExternalKind(value.ProductKind) || receiptID < 1 || value.EffectID == "" || !validExternalPushTestDeliveryID(value.DeliveryID) || len(configurationDigest) != 32 {
		return productport.ExternalPushTest{}, ErrInvalid
	}
	var result productport.ExternalPushTest
	err = tx.QueryRow(ctx, `INSERT INTO product_external_push_tests(product_id,product_kind,configuration_digest,receipt_id,effect_id,delivery_id,state,provider_accepted,delivery_proven,real_external_call_executed,auto_retry_allowed,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,FALSE,FALSE,FALSE,FALSE,$8)
RETURNING product_id,product_kind,effect_id,delivery_id,state,provider_accepted,delivery_proven,real_external_call_executed,auto_retry_allowed,created_at`, int64(value.ProductID), string(value.ProductKind), configurationDigest[:], receiptID, value.EffectID, value.DeliveryID, value.State, value.CreatedAt.UTC()).Scan(&result.ProductID, &result.ProductKind, &result.EffectID, &result.DeliveryID, &result.State, &result.ProviderAccepted, &result.DeliveryProven, &result.RealExternalCallExecuted, &result.AutoRetryAllowed, &result.CreatedAt)
	if err != nil {
		return productport.ExternalPushTest{}, mapDatabaseError(err)
	}
	return result, nil
}

// itoa is intentionally local to avoid accepting format strings or SQL from
// callers.  Product list statements need only one or two placeholders.
func itoa(value int) string {
	return strconv.Itoa(value)
}
