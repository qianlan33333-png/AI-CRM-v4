package main

import (
	"context"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	"testing"
	"time"
)

func TestHistoryChoosesLatestSeparatelyBeforeFill(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	shipping := &customerport.ShippingContact{DetailAddress: "latest address"}
	sourceA := func(_ context.Context, after int64) ([]customerport.ContactHistory, error) {
		if after > 0 {
			return nil, nil
		}
		return []customerport.ContactHistory{{Cursor: 1, Update: customerport.ContactUpdate{CustomerID: 1, MobileE164: "old", SourceAt: at, Source: "order:1"}}, {Cursor: 2, Update: customerport.ContactUpdate{CustomerID: 1, MobileE164: "new", SourceAt: at.Add(time.Hour), Source: "order:2"}}}, nil
	}
	sourceB := func(_ context.Context, after int64) ([]customerport.ContactHistory, error) {
		if after > 0 {
			return nil, nil
		}
		return []customerport.ContactHistory{{Cursor: 3, Update: customerport.ContactUpdate{CustomerID: 1, MobileE164: "middle", Shipping: shipping, SourceAt: at.Add(time.Minute), Source: "survey:3"}}, {Cursor: 4, SkipReason: "unmapped"}, {Cursor: 5, SkipReason: "unreadable"}}, nil
	}
	values, skipped, e := collect(context.Background(), []source{sourceA, sourceB})
	if e != nil {
		t.Fatal(e)
	}
	if values[1].phone.MobileE164 != "new" || values[1].phone.Shipping != nil || values[1].address.Shipping != shipping || values[1].address.MobileE164 != "" || skipped["unmapped"] != 1 || skipped["unreadable"] != 1 {
		t.Fatal("history mixed sources or treated skips as success")
	}
}
