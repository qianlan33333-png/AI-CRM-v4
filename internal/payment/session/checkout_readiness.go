package session

import (
	"context"
	"crypto/sha256"
	"errors"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"time"
)

func (s *Service) CanCreateCheckoutWithin(ctx context.Context, token string, now time.Time) (bool, error) {
	if s == nil || s.store == nil || len(token) < 20 || len(token) > 100 || now.IsZero() {
		return false, paymentport.ErrSessionRequired
	}
	record, err := s.store.Lookup(ctx, sha256.Sum256([]byte(token)), now.UTC())
	if err != nil {
		if errors.Is(err, ErrExpired) {
			return false, paymentport.ErrSessionRequired
		}
		return false, err
	}
	if record.Channel == paymentdomain.ChannelH5Official && !record.UnionIDVerified {
		return false, paymentport.ErrSessionRequired
	}
	return record.ConsumedAt == nil, nil
}
