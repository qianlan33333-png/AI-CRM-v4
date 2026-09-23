package app

import (
	"context"
	"errors"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"testing"
)

type mobileReadStore struct {
	*memoryStore
	fail bool
}

func (s mobileReadStore) ReadContactSnapshot(_ context.Context, id int64) ([]byte, int16, bool, error) {
	if s.fail {
		return nil, 0, false, errors.New("source error")
	}
	v, ok := s.contacts[id]
	return v, 1, ok, nil
}

type mobileReadCipher struct {
	contactCipherStub
	fail bool
}

func (c mobileReadCipher) Decrypt(raw []byte, _ int16) (string, error) {
	if c.fail {
		return "", errors.New("decrypt error")
	}
	return string(raw), nil
}
func TestCheckoutMobileReaderAbsentDistinctFromUnreadable(t *testing.T) {
	store := mobileReadStore{memoryStore: newMemoryStore()}
	service := NewService(directUOW{}, store)
	if _, found, err := service.ReadCheckoutMobileWithin(context.Background(), 7); found || err != nil {
		t.Fatal("absent should not fail", err)
	}
	store.contacts[7] = []byte("+8613800000000")
	if _, _, err := service.ReadCheckoutMobileWithin(context.Background(), 7); !errors.Is(err, orderport.ErrUnavailable) {
		t.Fatal("missing cipher hidden")
	}
	_ = service.SetContactCipher(mobileReadCipher{})
	if v, found, err := service.ReadCheckoutMobileWithin(context.Background(), 7); v != "+8613800000000" || !found || err != nil {
		t.Fatal("frozen contact not read", err)
	}
	_ = service.SetContactCipher(mobileReadCipher{fail: true})
	if _, _, err := service.ReadCheckoutMobileWithin(context.Background(), 7); !errors.Is(err, orderport.ErrUnavailable) {
		t.Fatal("decrypt failure hidden")
	}
	service.store = mobileReadStore{memoryStore: store.memoryStore, fail: true}
	if _, _, err := service.ReadCheckoutMobileWithin(context.Background(), 7); !errors.Is(err, orderport.ErrUnavailable) {
		t.Fatal("read failure hidden")
	}
}
