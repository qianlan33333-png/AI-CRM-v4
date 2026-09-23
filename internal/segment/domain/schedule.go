package domain

import "time"

type ScheduledConfiguration struct {
	PackageID              int64
	ConfigurationVersionID int64
	CronUTC                string
	Kind                   string
	// Actor is the legacy administrative compatibility projection. A machine
	// subject has Actor zero and is identified by ActorKind/ActorReference.
	Actor                  int64
	ActorKind              string
	ActorReference         string
	ConfigurationCreatedAt time.Time
	NextDueAt              *time.Time
	ScheduleVersion        int64
}
