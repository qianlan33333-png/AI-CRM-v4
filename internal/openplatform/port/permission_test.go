package port

import "testing"

func TestPermissionQueryRejectsAmbiguousAuthorizationFields(t *testing.T) {
	for _, input := range []string{
		`{"operation_id":"customer.context.get","Operation_ID":"admin.listUsers"}`,
		`{"Operation_ID":"customer.context.get"}`,
		`{"operation_id":"customer.context.get","resource":{},"Resource":{"customer_id":8}}`,
		`{"operation_id":"customer.context.get","operation_id":"admin.listUsers"}`,
		`{"operation_id":"customer.context.get","resource":null}`,
		`{"operation_id":"customer.context.get","resource":[]}`,
		`{"operation_id":"customer.context.get","resource":{"customer_id":1,"customer_id":8}}`,
		`{"operation_id":""}`,
		`{}`,
	} {
		if _, err := ParsePermissionQuery([]byte(input)); err == nil {
			t.Fatalf("ambiguous permission input accepted: %s", input)
		}
	}
	if query, err := ParsePermissionQuery([]byte(`{"operation_id":"customer.context.get","resource":{"customer_id":7}}`)); err != nil || query.OperationID != "customer.context.get" {
		t.Fatalf("canonical query=%v err=%v", query, err)
	}
}
