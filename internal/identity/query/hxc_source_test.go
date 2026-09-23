package query

import (
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

func TestClassifyHXCRoots(t *testing.T) {
	subject := identityport.HXCSubject{Position: 7, UnionID: "u", Phone: "13800138000", SourceUpdatedAt: time.Now()}
	tests := []struct {
		name         string
		union, phone hxcRoot
		wantStatus   identityport.HXCDisposition
		wantBy       identityport.HXCMatchSource
		wantReason   identityport.HXCReason
		wantCustomer customerdomain.CustomerID
	}{
		{"union", hxcRoot{1, 11}, hxcRoot{}, identityport.HXCMatched, identityport.HXCMatchUnionID, identityport.HXCReasonMatchedUnionID, 11},
		{"phone", hxcRoot{}, hxcRoot{1, 12}, identityport.HXCMatched, identityport.HXCMatchPhone, identityport.HXCReasonMatchedPhone, 12},
		{"both", hxcRoot{1, 13}, hxcRoot{1, 13}, identityport.HXCMatched, identityport.HXCMatchBoth, identityport.HXCReasonMatchedBoth, 13},
		{"cross", hxcRoot{1, 13}, hxcRoot{1, 14}, identityport.HXCConflict, identityport.HXCMatchNone, identityport.HXCReasonCrossRoot, 0},
		{"ambiguous", hxcRoot{2, 13}, hxcRoot{1, 13}, identityport.HXCConflict, identityport.HXCMatchNone, identityport.HXCReasonIdentityMultipleRoots, 0},
		{"pending", hxcRoot{}, hxcRoot{}, identityport.HXCUnmatched, identityport.HXCMatchNone, identityport.HXCReasonNoMatch, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyHXCRoots(subject, test.union, test.phone)
			if got.Disposition != test.wantStatus || got.MatchedBy != test.wantBy || got.Reason != test.wantReason || got.CustomerID != test.wantCustomer {
				t.Fatalf("got=%+v", got)
			}
		})
	}
}
