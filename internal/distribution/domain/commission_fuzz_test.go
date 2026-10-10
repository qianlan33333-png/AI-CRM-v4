package domain

import (
	"math"
	"math/big"
	"testing"
)

// A separate arbitrary-precision oracle checks rounding and overflow, including
// the largest supported paid amount and invalid signed inputs.
func FuzzCommissionMatchesExactMinorUnitArithmetic(f *testing.F) {
	for _, p := range []int64{-1, 0, 1, 2, 680, 1001, math.MaxInt64 / 3000, math.MaxInt64} {
		for _, r := range []int32{-1, 0, 333, 3000, 3001} {
			f.Add(p, r)
		}
	}
	f.Fuzz(func(t *testing.T, paid int64, rate int32) {
		got, err := CalculateCommission(paid, rate)
		valid := paid >= 0 && paid <= math.MaxInt64/3000 && rate >= 0 && rate <= 3000
		if !valid {
			if err == nil {
				t.Fatal("invalid financial input accepted")
			}
			return
		}
		want := new(big.Int).Mul(big.NewInt(paid), big.NewInt(int64(rate)))
		want.Quo(want, big.NewInt(10000))
		if err != nil || got != want.Int64() || got < 0 || got > paid {
			t.Fatalf("paid=%d rate=%d got=%d err=%v want=%s", paid, rate, got, err, want)
		}
	})
}
