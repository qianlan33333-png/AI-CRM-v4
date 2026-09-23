package app

import "testing"

func TestCheckoutOrderVersionCompatibleWithPaidEvent(t *testing.T) {
	for _, test := range []struct {
		checkout, paid int64
		want           bool
	}{
		{checkout: 1, paid: 2, want: true},
		{checkout: 2, paid: 2, want: true},
		{checkout: 3, paid: 2, want: false},
		{checkout: 0, paid: 2, want: false},
		{checkout: 1, paid: 0, want: false},
	} {
		if got := checkoutOrderVersionCompatible(test.checkout, test.paid); got != test.want {
			t.Fatalf("checkout=%d paid=%d got=%t want=%t", test.checkout, test.paid, got, test.want)
		}
	}
}
