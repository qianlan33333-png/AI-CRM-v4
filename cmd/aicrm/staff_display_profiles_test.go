package main

import (
	"context"
	"errors"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

type displayProfilesStub struct {
	rows []groupopsport.OperationMember
	err  error
}

func (s displayProfilesStub) ListOperationMemberDirectory(context.Context) ([]groupopsport.OperationMember, error) {
	return s.rows, s.err
}

func TestStaffDisplayProfilesPreserveAccessEligibilityAndBinding(t *testing.T) {
	users := []accessdomain.User{
		{ID: 1, WeComUserID: "huang", DisplayName: "企微客服 huang", Active: true},
		{ID: 2, WeComUserID: "inactive", DisplayName: "旧名字", Active: false},
		{ID: 3, WeComUserID: "rebound", DisplayName: "企微客服 rebound", Active: true},
		{ID: 4, WeComUserID: "local", DisplayName: "真实本地昵称", Active: true},
	}
	rows := []groupopsport.OperationMember{
		{StaffID: 1, SenderUserID: "huang", DisplayName: "黄有璨", NameSource: "wecom_profile", Active: false},
		{StaffID: 2, SenderUserID: "inactive", DisplayName: "已停用成员的真实名字", NameSource: "wecom_profile", Active: true},
		{StaffID: 3, SenderUserID: "old-binding", DisplayName: "不能错配", NameSource: "wecom_profile"},
		{StaffID: 4, SenderUserID: "local", DisplayName: "企微客服 local", NameSource: "local_fallback"},
		{StaffID: 99, SenderUserID: "not-local", DisplayName: "不能扩展名单", NameSource: "wecom_profile", Active: true},
	}
	got := staffWithDisplayProfiles(context.Background(), users, displayProfilesStub{rows: rows})
	if len(got) != len(users) {
		t.Fatal("profile expanded Access candidates")
	}
	want := []string{"黄有璨", "已停用成员的真实名字", "姓名待同步", "真实本地昵称"}
	for i, user := range got {
		if user.DisplayName != want[i] || user.Active != users[i].Active || user.ID != users[i].ID || user.WeComUserID != users[i].WeComUserID {
			t.Fatalf("profile changed binding or eligibility at %d", i)
		}
	}
	if users[0].DisplayName != "企微客服 huang" {
		t.Fatal("mutated Access source slice")
	}
}

func TestStaffDisplayProfilesFailuresAndAmbiguityDoNotInventNames(t *testing.T) {
	users := []accessdomain.User{{ID: 1, WeComUserID: "huang", DisplayName: "企微客服 huang", Active: true}}
	for _, reader := range []staffDisplayProfileReader{
		nil, displayProfilesStub{err: errors.New("projection unavailable")},
		displayProfilesStub{rows: []groupopsport.OperationMember{
			{StaffID: 1, SenderUserID: "huang", DisplayName: "姓名甲", NameSource: "wecom_profile"},
			{StaffID: 1, SenderUserID: "huang", DisplayName: "姓名乙", NameSource: "wecom_profile"},
		}},
	} {
		got := staffWithDisplayProfiles(context.Background(), users, reader)
		if len(got) != 1 || !got[0].Active || got[0].DisplayName != "姓名待同步" {
			t.Fatal("fallback lost eligible staff or invented nickname")
		}
	}
}

type displayUsersStub struct{ users []accessdomain.User }

func (s displayUsersStub) ListUsers(context.Context) ([]accessdomain.User, error) {
	return s.users, nil
}
func (s displayUsersStub) UserByID(_ context.Context, id int64, _ bool) (accessdomain.User, error) {
	for _, u := range s.users {
		if u.ID == id {
			return u, nil
		}
	}
	return accessdomain.User{}, errors.New("not found")
}
func TestChannelStaffPresentationUsesSharedProfilesWithoutChangingEligibility(t *testing.T) {
	adapter := channelStaffReferenceAdapter{
		users:    displayUsersStub{users: []accessdomain.User{{ID: 12, WeComUserID: "wecom-alice", DisplayName: "企微客服 wecom-alice", Active: false}}},
		profiles: displayProfilesStub{rows: []groupopsport.OperationMember{{StaffID: 12, SenderUserID: "wecom-alice", DisplayName: "真实昵称", NameSource: "wecom_profile", Active: true}}},
	}
	candidates, err := adapter.ListAcquisitionStaff(context.Background())
	if err != nil || len(candidates) != 1 || candidates[0].DisplayName != "真实昵称" || candidates[0].Active || candidates[0].ID != 12 || candidates[0].WeComUserID != "wecom-alice" {
		t.Fatal("candidate presentation changed binding/activation or omitted profile")
	}
	snapshots, err := adapter.ReadChannelStaff(context.Background(), []int64{12})
	if err != nil || len(snapshots) != 1 || snapshots[0].Name != "真实昵称" || snapshots[0].Active {
		t.Fatal("saved staff snapshot did not share presentation")
	}
}
