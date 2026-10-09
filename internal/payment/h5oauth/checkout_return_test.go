package h5oauth

import "testing"

func TestOriginalCheckoutReturnRemainsClosed(t *testing.T) {
	for _, path := range []string{"/pay/course-7?resume_order=5606", "/s/term-31/pay?resume_order=7"} {
		if !ValidReturnPath(path) {
			t.Fatal("resume rejected", path)
		}
	}
	for _, path := range []string{"/p/course-7?resume_order=7", "/s/term-31?resume_order=7", "/pay/course-7?resume_order=0", "/pay/course-7?resume_order=01", "/pay/course-7?resume_order=7&resume_order=8", "/pay/course-7?resume_order=7&next=/admin", "/pay/course-7?resume_order=9223372036854775808", "/pay/course-7?resume_order=7#fragment", "https://evil.test/pay/course-7?resume_order=7", "/pay/course-7?resume_order=%37"} {
		if ValidReturnPath(path) {
			t.Fatal("unsafe return", path)
		}
	}
}
