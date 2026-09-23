package domain

import "testing"

func TestCanonicalOneIDLabelRoundTripsOnlyCanonicalCustomerIDs(t *testing.T) {
	if actual := CanonicalOneIDLabel(CustomerID(42)); actual != "CID-42" {
		t.Fatalf("label=%q", actual)
	}
	for _, testCase := range []struct {
		value string
		want  CustomerID
		valid bool
	}{
		{value: "CID-42", want: 42, valid: true},
		{value: "CID-1", want: 1, valid: true},
		{value: "cid-42"},
		{value: "CID-042"},
		{value: "CID-0"},
		{value: "CID--42"},
		{value: "CID-42 "},
		{value: "CID-9223372036854775808"},
	} {
		got, valid := ParseCanonicalOneIDLabel(testCase.value)
		if got != testCase.want || valid != testCase.valid {
			t.Fatalf("parse %q = (%d,%t), want (%d,%t)", testCase.value, got, valid, testCase.want, testCase.valid)
		}
	}
	if actual := CanonicalOneIDLabel(0); actual != "" {
		t.Fatalf("invalid label=%q", actual)
	}
}
