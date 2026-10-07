package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
)

func TestCouponRuleUnicodeLimitsMatchPersistedCharacterContract(t *testing.T) {
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	store, events := newCouponTestStore(), &couponTestEvents{}
	service := couponTestService(now, store, events)
	input := couponTestItem(0, now)
	input.Name = strings.Repeat("券", 45)
	input.Instructions = strings.Repeat("说", 200)
	created, err := service.Create(context.Background(), couponport.UpsertCommand{Coupon: input, Actor: 7, IdempotencyKey: "unicode-rule-create-0001"})
	if err != nil || created.Name != input.Name || created.Instructions != input.Instructions {
		t.Fatalf("character-limit create=%+v err=%v", created, err)
	}
	read, err := service.Get(context.Background(), created.ID)
	if err != nil || read.Name != input.Name || read.Instructions != input.Instructions {
		t.Fatalf("character-limit read=%+v err=%v", read, err)
	}
	copied, err := service.Copy(context.Background(), created.ID, 7, "unicode-rule-copy-0001")
	if err != nil || copied.Name != strings.Repeat("券", 42)+" 副本" || utf8.RuneCountInString(copied.Name) != 45 || copied.Instructions != input.Instructions {
		t.Fatalf("character-limit copy=%+v err=%v", copied, err)
	}
	for _, field := range []string{"name", "instructions"} {
		t.Run(field+"_overflow", func(t *testing.T) {
			invalid := input
			if field == "name" {
				invalid.Name += "券"
			} else {
				invalid.Instructions += "说"
			}
			if _, _, err := normalize(couponport.UpsertCommand{Coupon: invalid}); !errors.Is(err, ErrInvalidCoupon) {
				t.Fatalf("overflow error=%v", err)
			}
		})
	}
}

func TestCouponCopyOfExistingChineseNameRemainsReadable(t *testing.T) {
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	store, events := newCouponTestStore(), &couponTestEvents{}
	service := couponTestService(now, store, events)
	item := couponTestItem(7, now)
	item.Name = strings.Repeat("券", 15)
	store.coupons[item.ID] = item
	copied, err := service.Copy(context.Background(), item.ID, 7, "unicode-existing-copy-0001")
	if err != nil || copied.Name != item.Name+" 副本" {
		t.Fatalf("existing Chinese copy=%+v err=%v", copied, err)
	}
}
