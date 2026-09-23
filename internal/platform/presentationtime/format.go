// Package presentationtime formats persisted instants for business-facing
// downloads. Transport contracts continue to use RFC3339/UTC.
package presentationtime

import (
	"time"
	_ "time/tzdata"
)

// tzdata embeds the IANA database in release binaries so the formatting
// contract does not depend on an operating-system zoneinfo installation.
var shanghai = mustLocation("Asia/Shanghai")

func mustLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic("presentation time zone unavailable: " + err.Error())
	}
	return location
}

// FormatShanghaiDateTime returns the fixed business display form used by
// human-readable exports. It deliberately omits the offset and fractions.
func FormatShanghaiDateTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.In(shanghai).Format("2006-01-02 15:04:05")
}
