package http

import (
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

func TestProfileCursorBindsCustomerSectionAndFilter(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	query, limit, err := profilePageQuery("20", "", "timeline", "customer.profile_synced", customerdomain.CustomerID(42), key)
	if err != nil || limit != 20 {
		t.Fatalf("query=%+v limit=%d err=%v", query, limit, err)
	}
	cursor, err := nextProfileCursor("timeline", 42, "customer.profile_synced", query, time.Now().UTC(), 9, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = profilePageQuery("20", cursor, "timeline", "other", 42, key); err == nil {
		t.Fatal("filter drift must fail")
	}
	if _, _, err = profilePageQuery("20", cursor, "timeline", "customer.profile_synced", 43, key); err == nil {
		t.Fatal("customer drift must fail")
	}
	parts := strings.Split(cursor, ".")
	replacement := "A"
	if strings.HasPrefix(parts[1], replacement) {
		replacement = "B"
	}
	tampered := parts[0] + "." + replacement + parts[1][1:]
	if _, _, err = profilePageQuery("20", tampered, "timeline", "customer.profile_synced", 42, key); err == nil {
		t.Fatal("tampered cursor must fail")
	}
}
