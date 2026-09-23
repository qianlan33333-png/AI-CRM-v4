package presentationtime

import (
	"testing"
	"time"
)

func TestFormatShanghaiDateTimeFormatsTheSameInstantWithoutBrowserTimezone(t *testing.T) {
	for _, value := range []time.Time{
		time.Date(2026, time.September, 5, 0, 1, 2, 611265000, time.UTC),
		time.Date(2026, time.September, 4, 17, 1, 2, 611265000, time.FixedZone("Los Angeles summer", -7*60*60)),
	} {
		if got, want := FormatShanghaiDateTime(value), "2026-09-05 08:01:02"; got != want {
			t.Fatalf("FormatShanghaiDateTime(%s) = %q, want %q", value, got, want)
		}
	}
}

func TestFormatShanghaiDateTimeDoesNotRenderGoZeroTime(t *testing.T) {
	if got, want := FormatShanghaiDateTime(time.Time{}), "—"; got != want {
		t.Fatalf("FormatShanghaiDateTime(zero) = %q, want %q", got, want)
	}
}
