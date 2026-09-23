package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type governanceDirectoryStub struct {
	employees []wecomport.EnterpriseEmployee
	exact     map[string]wecomport.EnterpriseEmployee
	listErr   error
	readErr   error
	listCalls int
	readCalls int
}

func (directory *governanceDirectoryStub) EnterpriseDirectoryReady() bool { return true }
func (directory *governanceDirectoryStub) ListEnterpriseEmployees(context.Context) ([]wecomport.EnterpriseEmployee, error) {
	directory.listCalls++
	if directory.listErr != nil {
		return nil, directory.listErr
	}
	return append([]wecomport.EnterpriseEmployee(nil), directory.employees...), nil
}
func (directory *governanceDirectoryStub) ReadEnterpriseEmployee(_ context.Context, userID string) (wecomport.EnterpriseEmployee, error) {
	directory.readCalls++
	if directory.readErr != nil {
		return wecomport.EnterpriseEmployee{}, directory.readErr
	}
	value, ok := directory.exact[userID]
	if !ok {
		return wecomport.EnterpriseEmployee{}, wecomport.ErrEnterpriseEmployeeNotFound
	}
	return value, nil
}

func governanceFixture(t *testing.T) (*Management, *memoryRepository, *governanceDirectoryStub, domain.Principal) {
	t.Helper()
	repository := newMemoryRepository()
	superGrant, viewerGrant, adminGrant := testNow, testNow, testNow
	repository.users[1] = domain.User{ID: 1, Username: "super", DisplayName: "Super", Active: true, LoginEnabled: true, AccessGrantedAt: &superGrant, SessionVersion: 3, Roles: []domain.Role{domain.RoleSuperAdmin}}
	repository.users[2] = domain.User{ID: 2, Username: "viewer", DisplayName: "Viewer", Active: true, LoginEnabled: true, AccessGrantedAt: &viewerGrant, SessionVersion: 4, Roles: []domain.Role{domain.RoleViewer}}
	repository.users[3] = domain.User{ID: 3, Username: "admin", DisplayName: "Admin", Active: true, LoginEnabled: true, AccessGrantedAt: &adminGrant, SessionVersion: 5, Roles: []domain.Role{domain.RoleAdmin}}
	repository.nextID = 4
	service, err := NewManagement(repository, testUOW{}, testPasswords{}, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	if err = service.SetGovernanceSigningKey(key); err != nil {
		t.Fatal(err)
	}
	// Intentionally reverse the Port's sequence. The app must stabilize it
	// before applying its signed offset cursor.
	directory := &governanceDirectoryStub{employees: []wecomport.EnterpriseEmployee{{UserID: "Bob", DisplayName: "Bob"}, {UserID: "Alice", DisplayName: "Alice"}}, exact: map[string]wecomport.EnterpriseEmployee{}}
	if err = service.SetEnterpriseEmployeeDirectory(directory, key, "fixture-corp"); err != nil {
		t.Fatal(err)
	}
	return service, repository, directory, domain.Principal{Kind: domain.KindAdmin, InternalID: 1, Roles: []domain.Role{domain.RoleSuperAdmin}, SessionVersion: 3}
}

func TestGovernancePasswordResetUsesStableKeyedIdempotencyFingerprint(t *testing.T) {
	service, repository, _, super := governanceFixture(t)
	command := ResetPasswordInput{TargetID: 2, Password: "valid-reset-password", IdempotencyKey: "reset-key"}
	if err := service.ResetGovernancePassword(context.Background(), super, command); err != nil {
		t.Fatal(err)
	}
	first := repository.users[2]
	if first.SessionVersion != 5 || first.PasswordHash != "hash:valid-reset-password" {
		t.Fatalf("first reset=%+v", first)
	}
	if err := service.ResetGovernancePassword(context.Background(), super, command); err != nil {
		t.Fatalf("same command retry=%v", err)
	}
	if got := repository.users[2].SessionVersion; got != first.SessionVersion {
		t.Fatalf("idempotent retry fenced session again: %d", got)
	}
	command.Password = "different-reset-password"
	if err := service.ResetGovernancePassword(context.Background(), super, command); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("payload drift error=%v", err)
	}
}

func TestEnterpriseDirectoryIdentifierMissFallsBackOnlyForDefinitiveMiss(t *testing.T) {
	service, _, directory, super := governanceFixture(t)
	listing, err := service.ListEnterpriseEmployees(context.Background(), super, "", "Alice", 50)
	if err != nil || len(listing.Items) != 1 || listing.Items[0].WeComUserID != "Alice" || directory.readCalls != 1 || directory.listCalls != 1 {
		t.Fatalf("listing=%+v read=%d list=%d err=%v", listing, directory.readCalls, directory.listCalls, err)
	}
	directory.readErr = errors.New("provider timeout")
	_, err = service.ListEnterpriseEmployees(context.Background(), super, "", "Alice", 50)
	if !errors.Is(err, ErrEnterpriseDirectoryUnavailable) || directory.listCalls != 1 {
		t.Fatalf("failure err=%v listCalls=%d", err, directory.listCalls)
	}
}

func TestEnterpriseDirectoryCursorBindsActorSessionCorpAndQuery(t *testing.T) {
	service, repository, _, super := governanceFixture(t)
	first, err := service.ListEnterpriseEmployees(context.Background(), super, "", "", 1)
	if err != nil || !first.HasMore || first.NextCursor == "" || len(first.Items) != 1 || first.Items[0].WeComUserID != "Alice" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := service.ListEnterpriseEmployees(context.Background(), super, first.NextCursor, "", 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].WeComUserID != "Bob" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if _, err = service.ListEnterpriseEmployees(context.Background(), super, first.NextCursor, "Alice", 1); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("query substituted cursor error=%v", err)
	}
	admin := domain.Principal{Kind: domain.KindAdmin, InternalID: 3, Roles: []domain.Role{domain.RoleAdmin}, SessionVersion: repository.users[3].SessionVersion}
	if _, err = service.ListEnterpriseEmployees(context.Background(), admin, first.NextCursor, "", 1); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("actor substituted cursor error=%v", err)
	}
}

func (repo *memoryRepository) SetStaffDisplayName(_ context.Context, id int64, providerID, name string, now time.Time) error {
	user, ok := repo.users[id]
	if !ok || user.WeComUserID != providerID {
		return domain.ErrConflict
	}
	user.DisplayName = name
	user.UpdatedAt = now
	repo.users[id] = user
	return nil
}

func TestRefreshStaffNamesPreservesPermissionsAndProviderFailure(t *testing.T) {
	service, repo, directory, actor := governanceFixture(t)
	user := repo.users[2]
	user.WeComUserID = "staff-two"
	user.DisplayName = "企微客服 staff-two"
	repo.users[2] = user
	directory.employees = []wecomport.EnterpriseEmployee{{UserID: "staff-two", DisplayName: "员工昵称"}, {UserID: "not-provisioned", DisplayName: "其他员工"}}
	if err := service.RefreshStaffNames(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	updated := repo.users[2]
	if updated.DisplayName != "员工昵称" || updated.LoginEnabled != user.LoginEnabled || updated.SessionVersion != user.SessionVersion || len(repo.users) != 3 || updated.Roles[0] != user.Roles[0] {
		t.Fatalf("refresh changed access: %+v", updated)
	}
	directory.listErr = errors.New("provider unavailable")
	if err := service.RefreshStaffNames(context.Background(), actor); !errors.Is(err, ErrEnterpriseDirectoryUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if repo.users[2].DisplayName != "员工昵称" {
		t.Fatal("provider failure erased trusted nickname")
	}
}
