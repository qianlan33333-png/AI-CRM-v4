package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

const (
	DefaultLimit  = 50
	MaximumLimit  = 200
	ExactCountCap = 10000
	// MaximumFilterCandidates keeps an explicit directory filter bounded before
	// its immutable matching set is supplied to the Customer-owned store.  A
	// cursor includes that set's digest, so a changed tag/owner fact cannot be
	// mistaken for another page of the old result.
	MaximumFilterCandidates = 100000
)

var (
	ErrInvalidQuery  = errors.New("invalid customer directory query")
	ErrInvalidCursor = errors.New("invalid customer directory cursor")
	ErrNotFound      = errors.New("customer directory record not found")
	// ErrFilterUnavailable means a valid bounded filter could not be resolved
	// from its owning local projection. It is deliberately distinct from a
	// malformed client value so the Host can retain the filter and retry.
	ErrFilterUnavailable = errors.New("customer directory filter unavailable")
)

type Filters struct {
	Keyword          string
	Status           string
	ActivationStatus string
	PhoneCustomerID  customerdomain.CustomerID
	PhoneMatchNone   bool
	OwnerStaffID     int64
	OwnerCustomerIDs []customerdomain.CustomerID
	OwnerMatchNone   bool
	TagID            int64
	TagCustomerIDs   []customerdomain.CustomerID
	TagMatchNone     bool
}

type Query struct {
	Filters   Filters
	Limit     int
	Watermark time.Time
	AfterAt   time.Time
	AfterID   customerdomain.CustomerID
}

type Item struct {
	CustomerNumber string                    `json:"customer_number,omitempty"`
	CustomerID     customerdomain.CustomerID `json:"customer_id"`
	CustomerStatus customerdomain.Status     `json:"status"`
	DisplayName    string                    `json:"display_name"`
	AvatarURL      string                    `json:"avatar_url"`
	OneIDLabel     string                    `json:"oneid"`
	PhoneMasked    string                    `json:"phone_masked"`
	PhoneAssurance string                    `json:"phone_assurance,omitempty"`
	// OwnerStaffID is the Customer-owned local assignee only.  A Provider
	// follow relationship is a separate read-model fact and must never be
	// represented as this CRM authority field.
	OwnerStaffID    *int64     `json:"owner_staff_id"`
	ActivationState string     `json:"activation_status"`
	LastSyncedAt    *time.Time `json:"last_synced_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type Detail struct {
	Item
	Gender      int16  `json:"gender"`
	ContactType int16  `json:"contact_type"`
	CorpName    string `json:"corp_name"`
	Source      string `json:"source"`
}

type PageData struct {
	Items           []Item
	Count           int64
	TotalIsEstimate bool
}

type Page struct {
	Items           []Item    `json:"items"`
	NextCursor      string    `json:"next_cursor,omitempty"`
	Total           int64     `json:"total"`
	TotalIsEstimate bool      `json:"total_is_estimate"`
	Watermark       time.Time `json:"watermark"`
}

type Store interface {
	List(context.Context, Query) (PageData, error)
	Detail(context.Context, customerdomain.CustomerID) (Detail, error)
	CustomerIDsForOwner(context.Context, int64, int) ([]customerdomain.CustomerID, error)
}

// TagCustomerMatcher is the only cross-domain input to a Customer-directory
// tag filter.  It returns canonical Customer IDs for an already-local tag ID;
// it cannot change the catalog, tag observations, identities, or customers.
type TagCustomerMatcher interface {
	CustomerIDsForTag(context.Context, int64, int) ([]customerdomain.CustomerID, error)
}

type Directory struct {
	Numbers    identityport.CustomerPublicNumbers
	Store      Store
	Now        func() time.Time
	SigningKey []byte
	Tags       TagCustomerMatcher
}

type ListRequest struct {
	Filters Filters
	Limit   int
	Cursor  string
}

type cursorPayload struct {
	Version    int    `json:"v"`
	Watermark  string `json:"w"`
	AfterAt    string `json:"a"`
	AfterID    int64  `json:"i"`
	FilterHash string `json:"f"`
}

func (directory Directory) List(ctx context.Context, request ListRequest) (Page, error) {
	request.Filters.Keyword = strings.TrimSpace(request.Filters.Keyword)
	if len(request.Filters.Keyword) > 200 || !validStatus(request.Filters.Status) || !validActivation(request.Filters.ActivationStatus) || request.Filters.OwnerStaffID < 0 || request.Filters.TagID < 0 || request.Limit < 0 || request.Limit > MaximumLimit {
		return Page{}, ErrInvalidQuery
	}
	if len(directory.SigningKey) < 32 {
		return Page{}, ErrInvalidCursor
	}
	if request.Limit == 0 {
		request.Limit = DefaultLimit
	}
	if request.Filters.OwnerStaffID > 0 {
		ids, err := directory.Store.CustomerIDsForOwner(ctx, request.Filters.OwnerStaffID, MaximumFilterCandidates+1)
		if err != nil || len(ids) > MaximumFilterCandidates {
			return Page{}, fmt.Errorf("%w: local owner projection", ErrFilterUnavailable)
		}
		request.Filters.OwnerCustomerIDs = sortedUniqueCustomerIDs(ids)
		request.Filters.OwnerMatchNone = len(request.Filters.OwnerCustomerIDs) == 0
	}
	if request.Filters.TagID > 0 {
		if directory.Tags == nil {
			return Page{}, ErrFilterUnavailable
		}
		ids, err := directory.Tags.CustomerIDsForTag(ctx, request.Filters.TagID, MaximumFilterCandidates+1)
		if err != nil || len(ids) > MaximumFilterCandidates {
			return Page{}, fmt.Errorf("%w: local tag observation", ErrFilterUnavailable)
		}
		request.Filters.TagCustomerIDs = sortedUniqueCustomerIDs(ids)
		request.Filters.TagMatchNone = len(request.Filters.TagCustomerIDs) == 0
	}
	hash := filtersHash(request.Filters)
	watermark := time.Now().UTC()
	if directory.Now != nil {
		watermark = directory.Now().UTC()
	}
	query := Query{Filters: request.Filters, Limit: request.Limit + 1, Watermark: watermark}
	if request.Cursor != "" {
		payload, err := decodeCursor(request.Cursor, directory.SigningKey)
		if err != nil || payload.FilterHash != hash || payload.AfterID < 1 {
			return Page{}, ErrInvalidCursor
		}
		query.Watermark, err = time.Parse(time.RFC3339Nano, payload.Watermark)
		if err != nil {
			return Page{}, ErrInvalidCursor
		}
		query.AfterAt, err = time.Parse(time.RFC3339Nano, payload.AfterAt)
		if err != nil || query.AfterAt.After(query.Watermark) {
			return Page{}, ErrInvalidCursor
		}
		query.AfterID = customerdomain.CustomerID(payload.AfterID)
	}
	if directory.Numbers != nil {
		if number, err := strconv.ParseInt(query.Filters.Keyword, 10, 64); err == nil && number >= 1000000 && number <= 9999999 {
			id, found, err := directory.Numbers.CustomerForPublicNumber(ctx, query.Filters.Keyword)
			if err != nil {
				return Page{}, err
			}
			if found {
				query.Filters.Keyword = ""
				if query.Filters.PhoneCustomerID > 0 && query.Filters.PhoneCustomerID != id {
					query.Filters.PhoneMatchNone = true
				}
				query.Filters.PhoneCustomerID = id
			} else {
				query.Filters.PhoneMatchNone = true
			}
		}
	}
	data, err := directory.Store.List(ctx, query)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: data.Items, Total: data.Count, TotalIsEstimate: data.TotalIsEstimate, Watermark: query.Watermark}
	if len(page.Items) > request.Limit {
		last := page.Items[request.Limit-1]
		page.Items = page.Items[:request.Limit]
		page.NextCursor, err = encodeCursor(cursorPayload{Version: 1, Watermark: query.Watermark.Format(time.RFC3339Nano), AfterAt: last.UpdatedAt.Format(time.RFC3339Nano), AfterID: int64(last.CustomerID), FilterHash: hash}, directory.SigningKey)
		if err != nil {
			return Page{}, err
		}
	}
	if directory.Numbers != nil {
		ids := make([]customerdomain.CustomerID, len(page.Items))
		for i, item := range page.Items {
			ids[i] = item.CustomerID
		}
		numbers, err := directory.Numbers.CustomerPublicNumbers(ctx, ids)
		if err != nil {
			return Page{}, err
		}
		for i := range page.Items {
			if numbers[page.Items[i].CustomerID] == "" {
				return Page{}, ErrNotFound
			}
			page.Items[i].CustomerNumber = numbers[page.Items[i].CustomerID]
		}
	}
	return page, nil
}

func filtersHash(filters Filters) string {
	payload, _ := json.Marshal([]any{filters.Keyword, filters.Status, filters.ActivationStatus, filters.PhoneCustomerID, filters.PhoneMatchNone, filters.OwnerStaffID, filters.OwnerCustomerIDs, filters.OwnerMatchNone, filters.TagID, filters.TagCustomerIDs, filters.TagMatchNone})
	digest := sha256.Sum256(payload)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func sortedUniqueCustomerIDs(values []customerdomain.CustomerID) []customerdomain.CustomerID {
	if len(values) == 0 {
		return []customerdomain.CustomerID{}
	}
	seen := make(map[customerdomain.CustomerID]struct{}, len(values))
	result := make([]customerdomain.CustomerID, 0, len(values))
	for _, value := range values {
		if value < 1 {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func encodeCursor(payload cursorPayload, key []byte) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeCursor(value string, key []byte) (cursorPayload, error) {
	if len(value) > 2048 {
		return cursorPayload{}, ErrInvalidCursor
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return cursorPayload{}, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursorPayload{}, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursorPayload{}, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursorPayload{}, ErrInvalidCursor
	}
	var payload cursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil || payload.Version != 1 || payload.Watermark == "" || payload.AfterAt == "" || payload.FilterHash == "" {
		return cursorPayload{}, ErrInvalidCursor
	}
	return payload, nil
}

func validStatus(value string) bool {
	return value == "" || value == "active" || value == "merged" || value == "closed"
}

func validActivation(value string) bool {
	return value == "" || value == "active" || value == "conflict" || value == "stale"
}
