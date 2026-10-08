package store

import (
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"strings"
	"testing"
)

func TestSelfOrdersRequireOwnersAndLiteralProductSearch(t *testing.T) {
	if _, _, e := selfPredicate(p.SelfQuery{Limit: 20}); e == nil {
		t.Fatal("empty owner scope became an unbounded query")
	}
	sql, args, e := selfPredicate(p.SelfQuery{CustomerIDs: []int64{11, 22}, State: "paid", Keyword: "100%_课程", OrderNo: "ORDER-1", Limit: 21})
	if e != nil || !strings.Contains(sql, "payer_customer_id=ANY($1)") || !strings.Contains(sql, "beneficiary_customer_id=ANY($1)") || args[1] != `%100\%\_课程%` || !strings.Contains(sql, "merchant_order_no=$3") || !strings.Contains(sql, "partially_refunded") {
		t.Fatalf("scoped literal search=%s %v %v", sql, args, e)
	}
	if _, _, e = selfPredicate(p.SelfQuery{CustomerIDs: []int64{-1}, Limit: 20}); e == nil {
		t.Fatal("invalid scope accepted")
	}
}
