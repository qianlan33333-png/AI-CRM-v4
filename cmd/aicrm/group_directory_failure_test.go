package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
)

type directoryFailureFixture struct{ code string }

func (e directoryFailureFixture) Error() string                   { return "provider-secret-raw-response" }
func (e directoryFailureFixture) DirectoryFailureCode() string    { return e.code }
func (e directoryFailureFixture) DirectoryFailureRetryable() bool { return false }

func TestGroupDirectoryFailureKeepsOnlySafeStageAndReason(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  string
	}{
		{directoryFailureFixture{"provider_permission_denied"}, "group_directory_detail_provider_permission_denied"},
		{directoryFailureFixture{"provider-secret-code"}, "group_directory_detail_provider_unavailable"},
		{context.DeadlineExceeded, "group_directory_detail_provider_timeout"},
	} {
		err := groupOpsDirectoryFailure("detail", tc.cause)
		var diagnostic *groupopsapp.GroupDirectoryReadError
		if !errors.As(err, &diagnostic) || err.Error() != tc.code || strings.Contains(err.Error(), "secret") {
			t.Fatalf("diagnostic=%v", err)
		}
	}
}
