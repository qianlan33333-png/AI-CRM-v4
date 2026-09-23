package oaproof

import (
	"strings"
	"testing"
	"time"
)

func TestScopedProofNeverTreatsOpaqueSourceAsUnionFact(t *testing.T) {
	s := Snapshot{Version: 1, SourceSHA256: strings.Repeat("a", 64), VerifiedAt: time.Now(), Rows: []Row{}}
	for n, v := range []string{"first", "first", "second"} {
		s.Rows = append(s.Rows, Row{SourceRef: v, MerchantOrderNo: string(rune('a' + n)), AmountMinor: 100, AppID: "wx-app", OpenID: v, EvidenceDigest: "sha256:" + strings.Repeat("b", 64)})
	}
	refs, e := s.References()
	if e != nil || len(refs) != 2 {
		t.Fatal(e)
	}
	for _, r := range refs {
		if r.Kind != "oa_openid" || r.Scope != "wechat-app:wx-app" {
			t.Fatal("wrong scoped kind")
		}
	}
	s.Rows[1].OpenID = "other"
	if _, e = s.References(); e == nil {
		t.Fatal("conflicting payer accepted")
	}
}
