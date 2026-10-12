package port

import (
	"regexp"
	"testing"
)

func FuzzContactPhoneCanonicalRoundTrip(f *testing.F) {
	for _, seed := range []string{"13800000001", "+8613800000001", " 13900000002 ", "", "+86", "13800000001\x00", "１３８０００００００１", "\xff"} {
		f.Add(seed)
	}
	canonical := regexp.MustCompile(`^\+861[3-9][0-9]{9}$`)
	f.Fuzz(func(t *testing.T, input string) {
		phone, accepted := NormalizeContactPhone(input)
		if !accepted {
			if phone != "" {
				t.Fatal("rejected phone leaked a usable canonical value")
			}
			return
		}
		if !canonical.MatchString(phone) {
			t.Fatal("accepted phone violates the existing canonical contract")
		}
		again, valid := NormalizeContactPhone(phone)
		if !valid || again != phone {
			t.Fatal("canonical phone cannot be safely reused")
		}
		local, valid := NormalizeContactPhone(phone[3:])
		if !valid || local != phone {
			t.Fatal("H5 local-number round trip changed the contact fact")
		}
		if _, valid := NormalizeContactPhone(phone + "0"); valid {
			t.Fatal("extended phone was silently truncated")
		}
	})
}
