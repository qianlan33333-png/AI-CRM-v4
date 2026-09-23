package port

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/readshare"
)

type ScopedMemberGridShares interface {
	ScopedRowReferences(context.Context, int64, []string) (map[string]string, error)
	ScopedShareToken(context.Context, int64, string) (string, error)
	SaveScopedShare(context.Context, readshare.Share, MemberGridActor, string) (readshare.Share, error)
	ReadScopedShare(context.Context, []byte) (readshare.Share, error)
	ListScopedShares(context.Context, ID) ([]readshare.Share, error)
}
