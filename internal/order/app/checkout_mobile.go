package app

import (
	"context"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

func (s *Service) ReadCheckoutMobileWithin(ctx context.Context, orderID int64) (string, bool, error) {
	if s == nil || orderID < 1 {
		return "", false, orderport.ErrConflict
	}
	store, ok := s.store.(interface {
		ReadContactSnapshot(context.Context, int64) ([]byte, int16, bool, error)
	})
	if !ok {
		return "", false, orderport.ErrUnavailable
	}
	ciphertext, version, found, err := store.ReadContactSnapshot(ctx, orderID)
	if err != nil {
		return "", false, orderport.ErrUnavailable
	}
	if !found {
		return "", false, nil
	}
	cipher, ok := s.contactCipher.(interface {
		Decrypt([]byte, int16) (string, error)
	})
	if !ok {
		return "", false, orderport.ErrUnavailable
	}
	mobile, err := cipher.Decrypt(ciphertext, version)
	if err != nil {
		return "", false, orderport.ErrUnavailable
	}
	return mobile, true, nil
}
