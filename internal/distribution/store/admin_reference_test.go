package store

import "testing"

func TestValidInstructionReferenceUsesPaymentStableProjection(t *testing.T) {
	for _, value := range []struct {
		value string
		want  bool
	}{
		{value: "psinst_8", want: true},
		{value: "psinst_1", want: true},
		{value: "psinstr_8", want: false},
		{value: "psinst_0", want: false},
		{value: "psinst_08", want: false},
		{value: "psinst_8x", want: false},
	} {
		if got := validInstructionReference(value.value); got != value.want {
			t.Fatalf("reference %q valid=%v want %v", value.value, got, value.want)
		}
	}
}
