package app

import (
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"testing"
	"time"
)

func TestCoreDecisionRejectsUnknownProductAndExecutableOutput(t *testing.T) {
	products := []segmentport.CoreProduct{{ID: 1, Enabled: true}, {ID: 2, Enabled: false}}
	for _, raw := range []string{`{"product_id":9,"reason":"a","evidence":"b"}`, `{"product_id":2,"reason":"a","evidence":"b"}`, `{"product_id":1,"reason":"a","evidence":"b","sql":"DELETE"}`, `{"product_id":1,"reason":"","evidence":"b"}`, `{"product_id":1,"reason":"a","evidence":"b"} {}`} {
		if _, e := ParseCoreDecision([]byte(raw), products); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`{"product_id":1,"reason":"适合","evidence":"问卷"}`, `{"product_id":0,"reason":"信息不足","evidence":""}`} {
		if _, e := ParseCoreDecision([]byte(raw), products); e != nil {
			t.Fatal(e)
		}
	}
}
func TestCorePushValidation(t *testing.T) {
	now := time.Now()
	p := segmentport.CorePush{Source: "node", PushID: "push", CustomerID: 1, PackageID: 1, OccurredAt: now, Status: "reported", StatusVersion: 1, Materials: []segmentport.CoreMaterialRef{{Kind: "image", ID: 1}}}
	if e := ValidateCorePush(p, now); e != nil {
		t.Fatal(e)
	}
	p.Materials = append(p.Materials, p.Materials[0])
	if ValidateCorePush(p, now) == nil {
		t.Fatal("duplicate material accepted")
	}
}
