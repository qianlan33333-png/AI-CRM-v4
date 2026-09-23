package port

import (
	"context"
	"time"
)

// Catalog is the shared directory, owned only by Group Ops. Consumers never
// import its store or infer customer identity from group membership.
type CatalogGroup struct {
	ExternalMemberCount *int32     `json:"-"`
	OwnerStaffID        int64      `json:"owner_staff_id"`
	ChatID              string     `json:"chat_id"`
	Name                string     `json:"name"`
	OwnerUserID         string     `json:"owner_user_id"`
	MemberCount         int        `json:"member_count"`
	State               string     `json:"sync_state"`
	ObservedAt          *time.Time `json:"observed_at"`
	CheckedAt           time.Time  `json:"checked_at"`
}
type CatalogPage struct {
	Items  []CatalogGroup `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}
type CatalogRun struct {
	ID         int64      `json:"id"`
	State      string     `json:"state"`
	Cursor     string     `json:"-"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Discovered int        `json:"discovered"`
	Added      int        `json:"added"`
	Updated    int        `json:"updated"`
	Failed     int        `json:"failed"`
}
type CatalogStatus struct {
	Run        *CatalogRun `json:"run"`
	NextAutoAt time.Time   `json:"next_auto_at"`
	Rerun      bool        `json:"rerun_requested"`
	Enabled    bool        `json:"enabled"`
}
type CatalogReader interface {
	ListCatalog(context.Context, string, int, int) (CatalogPage, error)
	ReadCatalogGroup(context.Context, string) (CatalogGroup, error)
}
type Catalog interface {
	CatalogReader
	RequestCatalogSync(context.Context, bool) (CatalogStatus, error)
	CatalogStatus(context.Context) (CatalogStatus, error)
	RefreshCatalogGroup(context.Context, string) (CatalogGroup, error)
}
