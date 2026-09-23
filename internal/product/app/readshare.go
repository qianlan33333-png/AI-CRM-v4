package app

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/readshare"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func (s *MemberGridWorkspaceService) SaveScopedShare(ctx context.Context, v readshare.Share, a productport.MemberGridActor, key string) (out readshare.Share, err error) {
	store, ok := s.store.(readshare.Store)
	if !ok || !(a.IsAdmin || a.IsSuperAdmin) || key == "" {
		return out, ErrUnavailable
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		if e := s.lockWritableMemberGridProduct(tx, productport.ID(v.ResourceID)); e != nil {
			return e
		}
		receipt, replay, e := s.reserveMemberGrid(tx, "scoped_share.set", a, key, struct {
			Share  readshare.Share
			Digest []byte
		}{v, v.Digest})
		if e != nil {
			return e
		}
		if replay {
			return s.replayMemberGrid(receipt, &out)
		}
		out, e = store.SaveReadShare(tx, v)
		if e != nil {
			return e
		}
		if e = s.event(tx, "scoped_share.set", key, a, productport.MemberGridShare{ProductID: productport.ID(v.ResourceID), Enabled: out.Enabled, Version: out.Version}); e != nil {
			return e
		}
		return s.completeMemberGrid(tx, receipt, out)
	})
	return out, classify(err)
}
func (s *MemberGridWorkspaceService) ReadScopedShare(ctx context.Context, digest []byte) (readshare.Share, error) {
	store, ok := s.store.(readshare.Store)
	if !ok {
		return readshare.Share{}, ErrUnavailable
	}
	var out readshare.Share
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = store.ReadShare(tx, digest); return e })
	return out, err
}
func (s *MemberGridWorkspaceService) ListScopedShares(ctx context.Context, id productport.ID) ([]readshare.Share, error) {
	store, ok := s.store.(readshare.Store)
	if !ok {
		return nil, ErrUnavailable
	}
	var out []readshare.Share
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = store.ListReadShares(tx, int64(id)); return e })
	return out, err
}

func (s *MemberGridWorkspaceService) ScopedShareToken(ctx context.Context, actor int64, key string) (string, error) {
	store, ok := s.store.(readshare.Store)
	if !ok {
		return "", ErrUnavailable
	}
	var secret []byte
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; secret, e = store.ReadShareKey(tx); return e })
	if err != nil || len(secret) != 32 {
		return "", ErrUnavailable
	}
	return readshare.Token(secret, "product", actor, key), nil
}

// ScopedRowReferences uses a server-private key, never the visitor's bearer
// capability, so visitors cannot enumerate internal record IDs from references.
func (s *MemberGridWorkspaceService) ScopedRowReferences(ctx context.Context, shareID int64, records []string) (map[string]string, error) {
	store, ok := s.store.(readshare.Store)
	if !ok || shareID < 1 || len(records) > 100 {
		return nil, ErrUnavailable
	}
	var key []byte
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; key, e = store.ReadShareKey(tx); return e })
	if err != nil || len(key) != 32 {
		return nil, ErrUnavailable
	}
	out := make(map[string]string, len(records))
	for _, ref := range records {
		out[ref] = readshare.Token(key, "product-row", shareID, ref)[:22]
	}
	return out, nil
}
