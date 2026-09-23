package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type fakeDirectoryStore struct {
	calls    []Query
	ownerIDs []customerdomain.CustomerID
	ownerErr error
}

func (store *fakeDirectoryStore) List(_ context.Context, query Query) (PageData, error) {
	store.calls = append(store.calls, query)
	items := []Item{{CustomerID: 1, UpdatedAt: query.Watermark.Add(-time.Minute)}, {CustomerID: 2, UpdatedAt: query.Watermark.Add(-2 * time.Minute)}, {CustomerID: 3, UpdatedAt: query.Watermark.Add(-3 * time.Minute)}}
	if !query.AfterAt.IsZero() {
		items = []Item{{CustomerID: 4, UpdatedAt: query.AfterAt.Add(-time.Minute)}}
	}
	return PageData{Items: items, Count: 4}, nil
}

func (store *fakeDirectoryStore) CustomerIDsForOwner(context.Context, int64, int) ([]customerdomain.CustomerID, error) {
	return append([]customerdomain.CustomerID(nil), store.ownerIDs...), store.ownerErr
}
func (*fakeDirectoryStore) Detail(context.Context, customerdomain.CustomerID) (Detail, error) {
	return Detail{}, ErrNotFound
}

func TestDirectoryCursorBindsWatermarkSortAndFilters(t *testing.T) {
	store := &fakeDirectoryStore{}
	now := time.Date(2026, 9, 3, 2, 30, 0, 0, time.UTC)
	directory := Directory{Store: store, Now: func() time.Time { return now }, SigningKey: []byte("0123456789abcdef0123456789abcdef")}
	first, err := directory.List(context.Background(), ListRequest{Limit: 2, Filters: Filters{Keyword: "Alice", Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" || len(first.Items) != 2 || !first.Watermark.Equal(now) {
		t.Fatalf("first=%+v", first)
	}
	second, err := directory.List(context.Background(), ListRequest{Limit: 2, Cursor: first.NextCursor, Filters: Filters{Keyword: "Alice", Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || !store.calls[1].Watermark.Equal(now) || store.calls[1].AfterID != 2 {
		t.Fatalf("second=%+v query=%+v", second, store.calls[1])
	}
	_, err = directory.List(context.Background(), ListRequest{Limit: 2, Cursor: first.NextCursor, Filters: Filters{Keyword: "Bob", Status: "active"}})
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("filter drift err=%v", err)
	}
	tampered := first.NextCursor[:len(first.NextCursor)-1] + "x"
	if _, err = directory.List(context.Background(), ListRequest{Limit: 2, Cursor: tampered, Filters: Filters{Keyword: "Alice", Status: "active"}}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tamper err=%v", err)
	}
}

func TestDirectoryRejectsInvalidBounds(t *testing.T) {
	store := &fakeDirectoryStore{}
	directory := Directory{Store: store, SigningKey: []byte("0123456789abcdef0123456789abcdef")}
	for _, request := range []ListRequest{{Limit: 201}, {Filters: Filters{Status: "unknown"}}, {Filters: Filters{ActivationStatus: "closed"}}} {
		if _, err := directory.List(context.Background(), request); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("request=%+v err=%v", request, err)
		}
	}
}

type fakeDirectoryTagMatcher struct {
	ids []customerdomain.CustomerID
	err error
}

func (matcher *fakeDirectoryTagMatcher) CustomerIDsForTag(context.Context, int64, int) ([]customerdomain.CustomerID, error) {
	return append([]customerdomain.CustomerID(nil), matcher.ids...), matcher.err
}

func TestDirectoryResolvesOwnerAndTagBeforeStorePagination(t *testing.T) {
	store := &fakeDirectoryStore{ownerIDs: []customerdomain.CustomerID{7, 2, 7, 0}}
	tags := &fakeDirectoryTagMatcher{ids: []customerdomain.CustomerID{9, 2, 2}}
	directory := Directory{Store: store, Tags: tags, Now: func() time.Time { return time.Date(2026, 9, 8, 1, 2, 3, 0, time.UTC) }, SigningKey: []byte("0123456789abcdef0123456789abcdef")}
	page, err := directory.List(context.Background(), ListRequest{Limit: 2, Filters: Filters{OwnerStaffID: 8, TagID: 12}})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 || page.NextCursor == "" {
		t.Fatalf("calls=%d page=%+v", len(store.calls), page)
	}
	filters := store.calls[0].Filters
	if !slices.Equal(filters.OwnerCustomerIDs, []customerdomain.CustomerID{2, 7}) || filters.OwnerMatchNone || !slices.Equal(filters.TagCustomerIDs, []customerdomain.CustomerID{2, 9}) || filters.TagMatchNone {
		t.Fatalf("filters=%+v", filters)
	}
	// A later tag projection must not be mistaken for the next page of the
	// old filter, even though the visible numeric tag ID did not change.
	tags.ids = []customerdomain.CustomerID{2, 8}
	if _, err = directory.List(context.Background(), ListRequest{Limit: 2, Cursor: page.NextCursor, Filters: Filters{OwnerStaffID: 8, TagID: 12}}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("changed membership err=%v", err)
	}
}

func TestDirectoryEmptyResolvedTagIsAnExactEmptyStorePredicate(t *testing.T) {
	store := &fakeDirectoryStore{}
	directory := Directory{Store: store, Tags: &fakeDirectoryTagMatcher{}, SigningKey: []byte("0123456789abcdef0123456789abcdef")}
	if _, err := directory.List(context.Background(), ListRequest{Filters: Filters{TagID: 10}}); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 || !store.calls[0].Filters.TagMatchNone || len(store.calls[0].Filters.TagCustomerIDs) != 0 {
		t.Fatalf("query=%+v", store.calls)
	}
}

func TestDirectoryKeepsValidFilterFailureRetryable(t *testing.T) {
	store := &fakeDirectoryStore{ownerErr: errors.New("local owner read failed")}
	directory := Directory{Store: store, Tags: &fakeDirectoryTagMatcher{err: errors.New("tag observation unavailable")}, SigningKey: []byte("0123456789abcdef0123456789abcdef")}
	if _, err := directory.List(context.Background(), ListRequest{Filters: Filters{OwnerStaffID: 8}}); !errors.Is(err, ErrFilterUnavailable) {
		t.Fatalf("owner err=%v", err)
	}
	store.ownerErr = nil
	if _, err := directory.List(context.Background(), ListRequest{Filters: Filters{TagID: 10}}); !errors.Is(err, ErrFilterUnavailable) {
		t.Fatalf("tag err=%v", err)
	}
}
