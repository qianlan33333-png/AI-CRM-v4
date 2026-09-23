package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type targetBatchTestStore struct {
	refs   []productport.ProductTargetReference
	result []productport.ProductTargetLookup
	err    error
	calls  int
}

func (store *targetBatchTestStore) ReadProductTargets(_ context.Context, refs []productport.ProductTargetReference) ([]productport.ProductTargetLookup, error) {
	store.calls++
	store.refs = append([]productport.ProductTargetReference(nil), refs...)
	if store.err != nil {
		return nil, store.err
	}
	return append([]productport.ProductTargetLookup(nil), store.result...), nil
}

func TestTargetBatchReaderUsesOneProductOwnedReadAndPreservesMissingFact(t *testing.T) {
	references := []productport.ProductTargetReference{
		{ProductType: productport.ProductOptionStandard, ID: 6},
		{ProductType: productport.ProductOptionServicePeriod, ID: 8},
	}
	store := &targetBatchTestStore{result: []productport.ProductTargetLookup{
		{Reference: references[0], Name: "中文商品", Found: true},
		{Reference: references[1], Found: false},
	}}
	uow := &productTestUoW{}
	reader, err := NewTargetBatchReader(uow, store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.ReadProductTargets(context.Background(), references)
	if err != nil || store.calls != 1 || uow.calls != 1 || !reflect.DeepEqual(store.refs, references) {
		t.Fatalf("result=%+v err=%v Product calls=%d uow=%d refs=%+v", result, err, store.calls, uow.calls, store.refs)
	}
	if !result[0].Found || result[0].Name != "中文商品" || result[1].Found || result[1].Name != "" {
		t.Fatalf("found/missing presentation=%+v", result)
	}
}

func TestTargetBatchReaderRejectsBadInputOrStoreFailureWithoutPartialResult(t *testing.T) {
	reference := productport.ProductTargetReference{ProductType: productport.ProductOptionStandard, ID: 6}
	for _, tc := range []struct {
		name  string
		refs  []productport.ProductTargetReference
		store *targetBatchTestStore
	}{
		{name: "duplicate", refs: []productport.ProductTargetReference{reference, reference}, store: &targetBatchTestStore{}},
		{name: "unavailable", refs: []productport.ProductTargetReference{reference}, store: &targetBatchTestStore{err: errors.New("database unavailable")}},
		{name: "mismatched response", refs: []productport.ProductTargetReference{reference}, store: &targetBatchTestStore{result: []productport.ProductTargetLookup{{Reference: reference, Found: true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, err := NewTargetBatchReader(&productTestUoW{}, tc.store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = reader.ReadProductTargets(context.Background(), tc.refs); err == nil {
				t.Fatal("bad batch was accepted")
			}
			if tc.name == "duplicate" && tc.store.calls != 0 {
				t.Fatalf("invalid input reached store: %d", tc.store.calls)
			}
		})
	}
}
