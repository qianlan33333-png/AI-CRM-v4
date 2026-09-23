package app

import (
	"context"
	"testing"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

type searchStaff struct{ testStaff }

func (searchStaff) ListEligibleStaff(context.Context) ([]groupopsport.OperationMember, error) {
	return []groupopsport.OperationMember{
		{StaffID: 1, SenderUserID: "QianLan", DisplayName: "浅蓝"},
		{StaffID: 2, SenderUserID: "HuangYouCan", DisplayName: "黄有璨"},
	}, nil
}

func TestOperationMemberSearchBeforePageLimit(t *testing.T) {
	s := &RuntimeService{uow: testUOW{}, staff: searchStaff{}}
	for _, query := range []string{"huang", " HUANG ", "黄有"} {
		page, err := s.ListOperationMembers(context.Background(), 1, query)
		if err != nil || len(page.Items) != 1 || page.Items[0].StaffID != 2 {
			t.Fatalf("query %q: page=%+v err=%v", query, page, err)
		}
	}
	page, err := s.ListOperationMembers(context.Background(), 100, "absent")
	if err != nil || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("unmatched query: page=%+v err=%v", page, err)
	}
}
