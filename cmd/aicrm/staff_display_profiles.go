package main

import (
	"context"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

// Staff activation and sender bindings remain Access-owned. This read-only
// seam reuses Group Ops' saved Provider profiles only for presentation; it
// neither resolves customer identity nor refreshes/writes Provider state.
type staffDisplayProfileReader interface {
	ListOperationMemberDirectory(context.Context) ([]groupopsport.OperationMember, error)
}

type staffDisplayBinding struct {
	staffID int64
	userID  string
}

func staffWithDisplayProfiles(ctx context.Context, users []accessdomain.User, reader staffDisplayProfileReader) []accessdomain.User {
	result := append([]accessdomain.User(nil), users...)
	names := make(map[staffDisplayBinding]string)
	ambiguous := make(map[staffDisplayBinding]bool)
	if reader != nil {
		// A presentation cache failure must not remove otherwise eligible staff.
		// Retain genuine Access names, and label synthetic names as awaiting sync.
		profiles, err := reader.ListOperationMemberDirectory(ctx)
		if err == nil {
			for _, profile := range profiles {
				if profile.NameSource != "wecom_profile" || profile.StaffID < 1 || strings.TrimSpace(profile.SenderUserID) == "" || !validStaffProfileDisplayName(profile.DisplayName) {
					continue
				}
				key := staffDisplayBinding{profile.StaffID, profile.SenderUserID}
				if previous, found := names[key]; found && previous != profile.DisplayName {
					ambiguous[key] = true
				}
				names[key] = profile.DisplayName
			}
		}
	}
	for index := range result {
		user := &result[index]
		key := staffDisplayBinding{user.ID, user.WeComUserID}
		if name, found := names[key]; found && !ambiguous[key] {
			user.DisplayName = name
		} else if name := strings.TrimSpace(user.DisplayName); name == "" || name == user.WeComUserID || name == "企微客服 "+user.WeComUserID {
			user.DisplayName = "姓名待同步"
		}
	}
	return result
}

func validStaffProfileDisplayName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && len([]rune(name)) <= 160 && !strings.ContainsAny(name, "\x00\r\n")
}
