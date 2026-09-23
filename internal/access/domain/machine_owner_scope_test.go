package domain

import "testing"

func TestOwnerScopeNormalizesAndRequiresEveryTrustedResource(t *testing.T) {
	scope, err := NormalizeOwnerScope([]byte(`{"owner_userid":["owner-b","owner-a","owner-a"],"corp_id":"corp-main","customer_id":42}`))
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Allows(map[string]string{"owner_userid": "owner-a", "corp_id": "corp-main", "customer_id": "42"}) {
		t.Fatal("trusted matching resources must be allowed")
	}
	if scope.Allows(map[string]string{"owner_userid": "owner-a", "corp_id": "corp-main"}) {
		t.Fatal("a missing constrained resource must deny")
	}
	if scope.Allows(map[string]string{"owner_userid": "owner-c", "corp_id": "corp-main", "customer_id": "42"}) {
		t.Fatal("a mismatched owner must deny")
	}
}

func TestOwnerScopeRejectsNonScalarValues(t *testing.T) {
	if _, err := NormalizeOwnerScope([]byte(`{"owner_userid":{"value":"owner-a"}}`)); err == nil {
		t.Fatal("nested owner scope must be rejected")
	}
}
