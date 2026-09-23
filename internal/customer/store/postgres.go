package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQL struct{}

func NewPostgreSQL() PostgreSQL { return PostgreSQL{} }

var _ customerapp.Store = PostgreSQL{}
var _ customerport.ProjectionWriter = PostgreSQL{}
var _ customerport.CallbackProjectionWriter = PostgreSQL{}
var _ customerport.ProviderProfileWriter = PostgreSQL{}
var _ customerport.AudienceReader = PostgreSQL{}
var _ customerport.AudienceRegistrationReader = PostgreSQL{}
var _ customerport.DirectoryDisplayNameReader = PostgreSQL{}
var _ customerport.DirectoryPublicProfileReader = PostgreSQL{}
var _ customerport.DirectoryContactDisplayReader = PostgreSQL{}
var _ customerport.RadarVisitorDirectoryReader = PostgreSQL{}

const maximumRadarVisitorDirectoryIDs = 500
const maximumRadarVisitorDirectorySearch = 100001

func (PostgreSQL) RadarVisitorDisplays(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.RadarVisitorDirectoryDisplay, error) {
	result := make(map[customerdomain.CustomerID]customerport.RadarVisitorDirectoryDisplay)
	if len(customerIDs) == 0 {
		return result, nil
	}
	if len(customerIDs) > maximumRadarVisitorDirectoryIDs {
		return nil, customerapp.ErrInvalidQuery
	}
	ids := make([]int64, 0, len(customerIDs))
	seen := make(map[customerdomain.CustomerID]struct{}, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 {
			return nil, customerapp.ErrInvalidQuery
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,display_name FROM customer_directory_projection WHERE customer_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id customerdomain.CustomerID
		var display customerport.RadarVisitorDirectoryDisplay
		if err = rows.Scan(&id, &display.DisplayName); err != nil {
			return nil, err
		}
		result[id] = display
	}
	return result, rows.Err()
}

func (PostgreSQL) SearchRadarVisitorCustomers(ctx context.Context, search string, limit int) ([]customerdomain.CustomerID, error) {
	search = strings.TrimSpace(search)
	if search == "" || len([]rune(search)) > 200 || limit < 1 || limit > maximumRadarVisitorDirectorySearch {
		return nil, customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	value := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)
	rows, err := tx.Query(ctx, `SELECT customer_id FROM customer_directory_projection
		WHERE display_name ILIKE '%'||$1||'%' ESCAPE '\'
		ORDER BY customer_id LIMIT $2`, value, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]customerdomain.CustomerID, 0)
	for rows.Next() {
		var id customerdomain.CustomerID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if canonical, valid := customerdomain.ParseCanonicalOneIDLabel(search); valid {
		for _, existing := range result {
			if existing == canonical {
				return result, nil
			}
		}
		// The canonical label is derived solely from CustomerID. Identity owns
		// the subsequent existence and lineage check; do not trust or repair
		// the mutable directory oneid_label cache here.
		result = append(result, canonical)
	}
	return result, nil
}

func (PostgreSQL) DisplayNames(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	result := make(map[customerdomain.CustomerID]string)
	if len(customerIDs) == 0 {
		return result, nil
	}
	if len(customerIDs) > 200 {
		return nil, customerapp.ErrInvalidQuery
	}
	ids := make([]int64, 0, len(customerIDs))
	seen := make(map[customerdomain.CustomerID]struct{}, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 {
			return nil, customerapp.ErrInvalidQuery
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,display_name FROM customer_directory_projection WHERE customer_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id customerdomain.CustomerID
		var name string
		if err = rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if name != "" {
			result[id] = name
		}
	}
	return result, rows.Err()
}

func (PostgreSQL) PublicProfiles(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.DirectoryPublicProfile, error) {
	result := make(map[customerdomain.CustomerID]customerport.DirectoryPublicProfile)
	if len(customerIDs) == 0 {
		return result, nil
	}
	if len(customerIDs) > 200 {
		return nil, customerapp.ErrInvalidQuery
	}
	ids := make([]int64, 0, len(customerIDs))
	seen := make(map[customerdomain.CustomerID]struct{}, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 {
			return nil, customerapp.ErrInvalidQuery
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,display_name,avatar_url FROM customer_directory_projection WHERE customer_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id customerdomain.CustomerID
		var value customerport.DirectoryPublicProfile
		if err = rows.Scan(&id, &value.DisplayName, &value.AvatarURL); err != nil {
			return nil, err
		}
		if value.DisplayName != "" {
			result[id] = value
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (PostgreSQL) ContactDisplays(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.DirectoryContactDisplay, error) {
	result := make(map[customerdomain.CustomerID]customerport.DirectoryContactDisplay)
	if len(customerIDs) == 0 {
		return result, nil
	}
	if len(customerIDs) > 200 {
		return nil, customerapp.ErrInvalidQuery
	}
	ids := make([]int64, 0, len(customerIDs))
	seen := make(map[customerdomain.CustomerID]struct{}, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 {
			return nil, customerapp.ErrInvalidQuery
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,display_name,phone_masked FROM customer_directory_projection WHERE customer_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id customerdomain.CustomerID
		var display customerport.DirectoryContactDisplay
		if err = rows.Scan(&id, &display.DisplayName, &display.PhoneMasked); err != nil {
			return nil, err
		}
		result[id] = display
	}
	return result, rows.Err()
}

func (PostgreSQL) AudienceRegistrationFacts(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]customerport.AudienceRegistrationFact, error) {
	if len(customerIDs) > customerport.MaxAudienceRegistrationCustomerIDs {
		return nil, customerapp.ErrInvalidQuery
	}
	facts := make(map[customerdomain.CustomerID]customerport.AudienceRegistrationFact, len(customerIDs))
	ids := make([]int64, 0, len(customerIDs))
	seen := make(map[customerdomain.CustomerID]struct{}, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 {
			return nil, customerapp.ErrInvalidQuery
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
		facts[id] = customerport.AudienceRegistrationFact{CustomerID: id}
	}
	if len(ids) == 0 {
		return facts, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,phone_masked,source,updated_at FROM customer_directory_projection WHERE customer_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id customerdomain.CustomerID
		var masked, source string
		var updated time.Time
		if err = rows.Scan(&id, &masked, &source, &updated); err != nil {
			return nil, err
		}
		facts[id] = customerport.AudienceRegistrationFact{CustomerID: id, Known: true, Registered: masked != "", Source: source, UpdatedAt: updated.UTC()}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}

func (PostgreSQL) ActiveWithin(ctx context.Context, reference time.Time, days int) ([]customerdomain.CustomerID, time.Time, error) {
	if reference.IsZero() || days < 1 || days > 999 {
		return nil, time.Time{}, customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id FROM customer_directory_projection
		WHERE customer_status='active' AND activation_status='active' AND updated_at <= $1 AND updated_at >= $1-($2::int * interval '1 day')
		ORDER BY customer_id LIMIT 100001`, reference.UTC(), days)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	ids := []customerdomain.CustomerID{}
	for rows.Next() {
		var id customerdomain.CustomerID
		if err = rows.Scan(&id); err != nil {
			return nil, time.Time{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, time.Time{}, err
	}
	var watermark time.Time
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(updated_at),to_timestamp(0)) FROM customer_directory_projection WHERE updated_at <= $1`, reference.UTC()).Scan(&watermark); err != nil {
		return nil, time.Time{}, err
	}
	return ids, watermark, nil
}

func (PostgreSQL) List(ctx context.Context, query customerapp.Query) (customerapp.PageData, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerapp.PageData{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT directory.customer_id,directory.customer_status,directory.display_name,directory.avatar_url,directory.oneid_label,directory.phone_masked,
			COALESCE(directory.phone_assurance,''),directory.activation_status,directory.last_synced_at,directory.updated_at,owner.staff_id
		FROM customer_directory_projection directory
		LEFT JOIN customer_local_owners owner ON owner.customer_id=directory.customer_id
		WHERE directory.updated_at <= $1
		  AND ($2::text='' OR directory.display_name ILIKE '%'||$2||'%' OR directory.oneid_label ILIKE '%'||$2||'%')
		  AND ($3::text='' OR directory.customer_status=$3)
		  AND ($4::text='' OR directory.activation_status=$4)
		  AND (NOT $5::boolean)
		  AND ($6::bigint=0 OR directory.customer_id=$6)
		  AND (NOT $7::boolean)
		  AND (cardinality($8::bigint[])=0 OR directory.customer_id=ANY($8::bigint[]))
		  AND (NOT $9::boolean)
		  AND (cardinality($10::bigint[])=0 OR directory.customer_id=ANY($10::bigint[]))
		  AND ($11::timestamptz IS NULL OR (directory.updated_at,directory.customer_id) < ($11,$12))
		ORDER BY directory.updated_at DESC,directory.customer_id DESC LIMIT $13`, query.Watermark, query.Filters.Keyword,
		query.Filters.Status, query.Filters.ActivationStatus, query.Filters.PhoneMatchNone,
		query.Filters.PhoneCustomerID, query.Filters.OwnerMatchNone, customerIDs(query.Filters.OwnerCustomerIDs),
		query.Filters.TagMatchNone, customerIDs(query.Filters.TagCustomerIDs), nullableTime(query.AfterAt), query.AfterID, query.Limit)
	if err != nil {
		return customerapp.PageData{}, err
	}
	defer rows.Close()
	data := customerapp.PageData{Items: []customerapp.Item{}}
	for rows.Next() {
		var item customerapp.Item
		if err = rows.Scan(&item.CustomerID, &item.CustomerStatus, &item.DisplayName, &item.AvatarURL, &item.OneIDLabel,
			&item.PhoneMasked, &item.PhoneAssurance, &item.ActivationState, &item.LastSyncedAt, &item.UpdatedAt, &item.OwnerStaffID); err != nil {
			return customerapp.PageData{}, err
		}
		data.Items = append(data.Items, item)
	}
	if err = rows.Err(); err != nil {
		return customerapp.PageData{}, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM customer_directory_projection directory
		LEFT JOIN customer_local_owners owner ON owner.customer_id=directory.customer_id
		WHERE directory.updated_at <= $1
		AND ($2::text='' OR directory.display_name ILIKE '%'||$2||'%' OR directory.oneid_label ILIKE '%'||$2||'%')
		AND ($3::text='' OR directory.customer_status=$3) AND ($4::text='' OR directory.activation_status=$4)
		AND (NOT $5::boolean) AND ($6::bigint=0 OR directory.customer_id=$6)
		AND (NOT $7::boolean) AND (cardinality($8::bigint[])=0 OR directory.customer_id=ANY($8::bigint[]))
		AND (NOT $9::boolean) AND (cardinality($10::bigint[])=0 OR directory.customer_id=ANY($10::bigint[]))
		LIMIT 10001) capped`, query.Watermark, query.Filters.Keyword, query.Filters.Status, query.Filters.ActivationStatus,
		query.Filters.PhoneMatchNone, query.Filters.PhoneCustomerID, query.Filters.OwnerMatchNone, customerIDs(query.Filters.OwnerCustomerIDs),
		query.Filters.TagMatchNone, customerIDs(query.Filters.TagCustomerIDs)).Scan(&data.Count)
	if err != nil {
		return customerapp.PageData{}, err
	}
	if data.Count > customerapp.ExactCountCap {
		data.Count = customerapp.ExactCountCap
		data.TotalIsEstimate = true
	}
	return data, nil
}

// CustomerIDsForOwner is a Customer-owned authority lookup.  It deliberately
// uses only customer_local_owners; active WeCom follow relationships remain a
// separate Provider fact and cannot be presented or filtered as CRM ownership.
func (PostgreSQL) CustomerIDsForOwner(ctx context.Context, staffID int64, limit int) ([]customerdomain.CustomerID, error) {
	if staffID < 1 || limit < 1 || limit > customerapp.MaximumFilterCandidates+1 {
		return nil, customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id FROM customer_local_owners WHERE staff_id=$1 ORDER BY customer_id LIMIT $2`, staffID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]customerdomain.CustomerID, 0)
	for rows.Next() {
		var id customerdomain.CustomerID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func customerIDs(values []customerdomain.CustomerID) []int64 {
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value > 0 {
			result = append(result, int64(value))
		}
	}
	return result
}

func (PostgreSQL) Detail(ctx context.Context, customerID customerdomain.CustomerID) (customerapp.Detail, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerapp.Detail{}, err
	}
	var detail customerapp.Detail
	err = tx.QueryRow(ctx, `SELECT customer_id,customer_status,display_name,avatar_url,oneid_label,phone_masked,
		COALESCE(phone_assurance,''),activation_status,last_synced_at,updated_at,gender,contact_type,corp_name,source
		FROM customer_directory_projection WHERE customer_id=$1`, customerID).Scan(&detail.CustomerID, &detail.CustomerStatus,
		&detail.DisplayName, &detail.AvatarURL, &detail.OneIDLabel, &detail.PhoneMasked, &detail.PhoneAssurance,
		&detail.ActivationState, &detail.LastSyncedAt, &detail.UpdatedAt, &detail.Gender, &detail.ContactType,
		&detail.CorpName, &detail.Source)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerapp.Detail{}, customerapp.ErrNotFound
	}
	return detail, err
}

func (PostgreSQL) UpsertDirectoryProjection(ctx context.Context, projection customerport.DirectoryProjection) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,avatar_url,gender,contact_type,corp_name,oneid_label,phone_masked,phone_assurance,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$13,$14,$15)
		ON CONFLICT(customer_id) DO UPDATE SET customer_status=EXCLUDED.customer_status,display_name=EXCLUDED.display_name,
		avatar_url=EXCLUDED.avatar_url,gender=EXCLUDED.gender,contact_type=EXCLUDED.contact_type,corp_name=EXCLUDED.corp_name,
		oneid_label=EXCLUDED.oneid_label,phone_masked=CASE WHEN EXCLUDED.phone_masked='' THEN customer_directory_projection.phone_masked ELSE EXCLUDED.phone_masked END,
		phone_assurance=COALESCE(EXCLUDED.phone_assurance,customer_directory_projection.phone_assurance),activation_status=EXCLUDED.activation_status,
		source=EXCLUDED.source,source_version=customer_directory_projection.source_version+1,last_synced_at=EXCLUDED.last_synced_at,updated_at=EXCLUDED.updated_at`, projection.CustomerID, projection.CustomerStatus,
		projection.DisplayName, projection.AvatarURL, projection.Gender, projection.ContactType, projection.CorpName,
		projection.OneIDLabel, projection.PhoneMasked, projection.PhoneAssurance, projection.ActivationState,
		projection.Source, projection.SourceVersion, projection.LastSyncedAt, projection.UpdatedAt)
	return err
}

func (PostgreSQL) ActivateDirectoryCustomer(ctx context.Context, customerID customerdomain.CustomerID, source string, at time.Time) error {
	if customerID < 1 || source == "" || at.IsZero() {
		return customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active','微信用户',$2,'active',$3,1,$4,$4)
		ON CONFLICT(customer_id) DO UPDATE SET customer_status='active',activation_status='active',source=EXCLUDED.source,
		source_version=customer_directory_projection.source_version+1,last_synced_at=EXCLUDED.last_synced_at,updated_at=EXCLUDED.updated_at`,
		customerID, "CID-"+strconv.FormatInt(int64(customerID), 10), source, at)
	return err
}

func (PostgreSQL) ObserveProviderProfile(ctx context.Context, customerID customerdomain.CustomerID, observation customerport.ProviderProfileObservation) error {
	observation.DisplayName = strings.TrimSpace(observation.DisplayName)
	if customerID < 1 || observation.Source == "" || len(observation.Source) > 128 || strings.ContainsAny(observation.Source, " \t\r\n\x00") || observation.ObservedAt.IsZero() {
		return customerapp.ErrInvalidQuery
	}
	if observation.DisplayName == "" {
		observation.DisplayName = "微信用户"
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,avatar_url,oneid_label,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active',$2,$3,$4,'active',$5,1,$6,$6)
		ON CONFLICT(customer_id) DO UPDATE SET
			customer_status='active',
			display_name=CASE WHEN customer_directory_projection.display_name='' OR customer_directory_projection.source IN ('identity_provision','identity_provision_backfill','wechat.payment.h5_oauth.userinfo') THEN EXCLUDED.display_name ELSE customer_directory_projection.display_name END,
			avatar_url=CASE WHEN EXCLUDED.avatar_url<>'' AND (customer_directory_projection.avatar_url='' OR customer_directory_projection.source IN ('identity_provision','identity_provision_backfill','wechat.payment.h5_oauth.userinfo')) THEN EXCLUDED.avatar_url ELSE customer_directory_projection.avatar_url END,
			oneid_label=EXCLUDED.oneid_label,
			activation_status='active',
			source=CASE WHEN customer_directory_projection.display_name='' OR customer_directory_projection.source IN ('identity_provision','identity_provision_backfill','wechat.payment.h5_oauth.userinfo') THEN EXCLUDED.source ELSE customer_directory_projection.source END,
			source_version=customer_directory_projection.source_version+1,
			last_synced_at=EXCLUDED.last_synced_at,
			updated_at=EXCLUDED.updated_at`,
		customerID, observation.DisplayName, observation.AvatarURL, customerdomain.CanonicalOneIDLabel(customerID), observation.Source, observation.ObservedAt.UTC())
	return err
}

func (PostgreSQL) MarkDirectoryStale(ctx context.Context, customerIDs []customerdomain.CustomerID, at time.Time) (int64, error) {
	if len(customerIDs) == 0 {
		return 0, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, len(customerIDs))
	for index, id := range customerIDs {
		ids[index] = int64(id)
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_directory_projection SET activation_status='stale',source_version=source_version+1,updated_at=$2 WHERE customer_id=ANY($1) AND activation_status <> 'stale'`, ids, at)
	return tag.RowsAffected(), err
}

func (PostgreSQL) UpdateDirectoryPhone(ctx context.Context, customerID customerdomain.CustomerID, masked string, assurance identitydomain.Assurance, sourceVersion int64, at time.Time) error {
	if customerID < 1 || masked == "" || (assurance != identitydomain.AssuranceDeclared && assurance != identitydomain.AssuranceVerified) || sourceVersion < 1 {
		return customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,oneid_label,phone_masked,phone_assurance,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active',$6,$2,$3,'active','survey',$4,$5,$5)
		ON CONFLICT(customer_id) DO UPDATE SET phone_masked=EXCLUDED.phone_masked,phone_assurance=EXCLUDED.phone_assurance,
		source_version=GREATEST(customer_directory_projection.source_version+1,$4),updated_at=$5`, customerID, masked, assurance, sourceVersion, at, "CID-"+strconv.FormatInt(int64(customerID), 10))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return customerapp.ErrNotFound
	}
	return nil
}

func (PostgreSQL) ClearDirectoryPhone(ctx context.Context, customerID customerdomain.CustomerID, at time.Time) error {
	if customerID < 1 {
		return customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_directory_projection SET phone_masked='',phone_assurance=NULL,source_version=source_version+1,updated_at=$2 WHERE customer_id=$1`, customerID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return customerapp.ErrNotFound
	}
	return nil
}

func nullableTime(value interface{ IsZero() bool }) any {
	if value.IsZero() {
		return nil
	}
	return value
}
