package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type testUOW struct{}

func (testUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type testPasswords struct{}

func (testPasswords) Hash(value string) (string, error) {
	if len(value) < 3 {
		return "", domain.ErrInvalidInput
	}
	return "hash:" + value, nil
}
func (testPasswords) Verify(value, encoded string) bool { return encoded == "hash:"+value }

type memoryRepository struct {
	mu       sync.Mutex
	users    map[int64]domain.User
	sessions map[[32]byte]domain.Session
	limits   map[[32]byte]domain.LoginRateLimit
	receipts map[string][32]byte
	audits   []domain.LoginAudit
	nextID   int64
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{users: map[int64]domain.User{}, sessions: map[[32]byte]domain.Session{}, limits: map[[32]byte]domain.LoginRateLimit{}, receipts: map[string][32]byte{}, nextID: 1}
}

func (repo *memoryRepository) CountUsers(context.Context) (int64, error) {
	return int64(len(repo.users)), nil
}
func (repo *memoryRepository) UserByID(_ context.Context, id int64, _ bool) (domain.User, error) {
	user, ok := repo.users[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return user, nil
}
func (repo *memoryRepository) UserByUsername(_ context.Context, username string, _ bool) (domain.User, error) {
	for _, user := range repo.users {
		if user.Username == username {
			return user, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}
func (repo *memoryRepository) UserByWeComUserID(_ context.Context, wecomUserID string, _ bool) (domain.User, error) {
	for _, user := range repo.users {
		if user.WeComUserID == wecomUserID {
			return user, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}
func (repo *memoryRepository) UsersByWeComUserIDs(_ context.Context, values []string) ([]domain.User, error) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	result := make([]domain.User, 0, len(values))
	for _, user := range repo.users {
		if _, ok := seen[user.WeComUserID]; ok {
			result = append(result, user)
		}
	}
	return result, nil
}
func (repo *memoryRepository) ListUsers(context.Context) ([]domain.User, error) {
	result := make([]domain.User, 0, len(repo.users))
	for _, user := range repo.users {
		result = append(result, user)
	}
	return result, nil
}
func (repo *memoryRepository) CreateUser(_ context.Context, user domain.User) (domain.User, error) {
	user.ID = repo.nextID
	repo.nextID++
	user.SessionVersion = 1
	if user.AccessGrantedAt == nil {
		granted := testNow
		user.AccessGrantedAt = &granted
	}
	user.LoginEnabled = user.Active
	repo.users[user.ID] = user
	return user, nil
}
func (repo *memoryRepository) CreateStaffProjection(_ context.Context, user domain.User) (domain.User, error) {
	user.ID = repo.nextID
	repo.nextID++
	user.SessionVersion, user.Active, user.LoginEnabled, user.AccessGrantedAt, user.Roles = 1, true, false, nil, []domain.Role{domain.RoleViewer}
	repo.users[user.ID] = user
	return user, nil
}
func (repo *memoryRepository) BootstrapUser(ctx context.Context, user domain.User) (domain.User, bool, error) {
	if len(repo.users) > 0 {
		existing, err := repo.UserByUsername(ctx, user.Username, false)
		return existing, false, err
	}
	created, err := repo.CreateUser(ctx, user)
	return created, true, err
}
func (repo *memoryRepository) SetLoginEnabled(_ context.Context, id int64, enabled bool, _ time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	if user.AccessGrantedAt == nil {
		return domain.ErrNotFound
	}
	if user.LoginEnabled == enabled {
		return nil
	}
	if enabled && !user.Active && user.LegacyLoginReactivationPending {
		user.Active = true
	}
	user.LoginEnabled, user.LegacyLoginReactivationPending, user.SessionVersion = enabled, false, user.SessionVersion+1
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) GrantAccess(_ context.Context, id int64, role domain.Role, displayName string, now time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	if !user.Active || user.AccessGrantedAt != nil {
		return domain.ErrConflict
	}
	user.Roles, user.DisplayName, user.LoginEnabled, user.AccessGrantedAt, user.SessionVersion = []domain.Role{role}, strings.TrimSpace(displayName), true, &now, user.SessionVersion+1
	repo.users[id] = user
	return nil
}

func (repo *memoryRepository) ReserveLoginAccessRequest(_ context.Context, actorID int64, key string, digest [32]byte, _ time.Time) (bool, error) {
	receiptKey := fmt.Sprintf("%d:%s", actorID, key)
	if stored, exists := repo.receipts[receiptKey]; exists {
		if stored != digest {
			return false, domain.ErrConflict
		}
		return false, nil
	}
	repo.receipts[receiptKey] = digest
	return true, nil
}

func TestSetLoginAccessIsAtomicAtTheAccessBoundaryAndFencesDisabledSessions(t *testing.T) {
	repository := newMemoryRepository()
	ownerGrant, operatorGrant := testNow, testNow
	repository.users[1] = domain.User{ID: 1, Username: "owner", DisplayName: "Owner", Active: true, LoginEnabled: true, AccessGrantedAt: &ownerGrant, SessionVersion: 1, Roles: []domain.Role{domain.RoleSuperAdmin}}
	repository.users[2] = domain.User{ID: 2, Username: "operator", DisplayName: "Operator", Active: true, LoginEnabled: true, AccessGrantedAt: &operatorGrant, SessionVersion: 7, Roles: []domain.Role{domain.RoleAdmin}}
	service, err := NewManagement(repository, testUOW{}, testPasswords{}, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	actor := domain.Principal{Kind: domain.KindAdmin, InternalID: 1, Roles: []domain.Role{domain.RoleSuperAdmin}, SessionVersion: 1}
	users, err := service.SetLoginAccess(context.Background(), actor, "access-1", []LoginAccessChange{{AdminUserID: 2, LoginEnabled: false}})
	if err != nil || len(users) != 2 || !repository.users[2].Active || repository.users[2].LoginEnabled || repository.users[2].SessionVersion != 8 {
		t.Fatalf("users=%+v stored=%+v err=%v", users, repository.users[2], err)
	}
	_, err = service.SetLoginAccess(context.Background(), actor, "access-1", []LoginAccessChange{{AdminUserID: 2, LoginEnabled: false}})
	if err != nil || repository.users[2].SessionVersion != 8 {
		t.Fatalf("idempotent desired state session=%d err=%v", repository.users[2].SessionVersion, err)
	}
	if _, err = service.SetLoginAccess(context.Background(), actor, "access-2", []LoginAccessChange{{AdminUserID: 1, LoginEnabled: false}}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("self disable error=%v", err)
	}
	if _, err = service.SetLoginAccess(context.Background(), actor, "access-1", []LoginAccessChange{{AdminUserID: 2, LoginEnabled: true}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("payload drift error=%v", err)
	}
	if !repository.users[2].Active || repository.users[2].LoginEnabled || repository.users[2].SessionVersion != 8 {
		t.Fatalf("payload drift mutated user=%+v", repository.users[2])
	}
}
func (repo *memoryRepository) SetWeComUserID(_ context.Context, id int64, value string, _ time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	user.WeComUserID, user.SessionVersion = value, user.SessionVersion+1
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) ReplaceRoles(_ context.Context, id int64, roles []domain.Role, _ time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	user.Roles, user.SessionVersion = roles, user.SessionVersion+1
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) SetPasswordHash(_ context.Context, id int64, value string, _ time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	user.PasswordHash, user.SessionVersion = value, user.SessionVersion+1
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) SetLastLogin(_ context.Context, id int64, now time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	user.LastLoginAt = &now
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) CreateSession(_ context.Context, session domain.Session) (domain.Session, error) {
	session.ID = int64(len(repo.sessions) + 1)
	repo.sessions[session.TokenDigest] = session
	return session, nil
}
func (repo *memoryRepository) SessionByTokenDigest(_ context.Context, digest [32]byte, _ bool) (domain.Session, error) {
	session, ok := repo.sessions[digest]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	session.User = repo.users[session.AdminUserID]
	return session, nil
}
func (repo *memoryRepository) TouchSession(context.Context, int64, time.Time) error { return nil }
func (repo *memoryRepository) RevokeSession(_ context.Context, digest [32]byte, reason string, now time.Time) (bool, error) {
	session, ok := repo.sessions[digest]
	if !ok {
		return false, nil
	}
	session.RevokedAt, session.RevokedReason = &now, reason
	repo.sessions[digest] = session
	return true, nil
}
func (repo *memoryRepository) LoginRateLimit(_ context.Context, digest [32]byte, _ bool) (domain.LoginRateLimit, error) {
	limit, ok := repo.limits[digest]
	if !ok {
		limit = domain.LoginRateLimit{KeyDigest: digest, WindowStartedAt: testNow, UpdatedAt: testNow}
		repo.limits[digest] = limit
	}
	return limit, nil
}
func (repo *memoryRepository) SaveLoginRateLimit(_ context.Context, limit domain.LoginRateLimit) error {
	repo.limits[limit.KeyDigest] = limit
	return nil
}
func (repo *memoryRepository) AppendLoginAudit(_ context.Context, audit domain.LoginAudit) error {
	repo.audits = append(repo.audits, audit)
	return nil
}
func (*memoryRepository) AppendAccessAudit(context.Context, domain.AccessAudit) error { return nil }

var testNow = time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)

func authenticationFixture(t *testing.T, maxFailures int) (*Authentication, *memoryRepository, *time.Time) {
	t.Helper()
	repository := newMemoryRepository()
	grant := testNow
	repository.users[1] = domain.User{ID: 1, Username: "operator", PasswordHash: "hash:correct-password", Active: true, LoginEnabled: true, AccessGrantedAt: &grant,
		SessionVersion: 1, Roles: []domain.Role{domain.RoleSuperAdmin}}
	now := testNow
	service, err := NewAuthentication(repository, testUOW{}, testPasswords{}, AuthenticationConfig{
		SessionTTL: time.Hour, Window: time.Minute, MaxFailures: maxFailures, BlockFor: time.Minute,
		Now: func() time.Time { return now }, DummyPHCHash: "hash:dummy-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, &now
}

func TestSessionReplayAfterLogoutAndCSRFValidation(t *testing.T) {
	service, _, _ := authenticationFixture(t, 5)
	issued, err := service.Login(context.Background(), LoginCommand{Username: "operator", Password: "correct-password", Remote: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthorizeCSRF(context.Background(), issued.SessionToken, issued.CSRFToken, "replayed-wrong-token"); !errors.Is(err, domain.ErrCSRFRequired) {
		t.Fatalf("wrong CSRF error = %v", err)
	}
	if err = service.Logout(context.Background(), issued.SessionToken, issued.CSRFToken, issued.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(context.Background(), issued.SessionToken); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatalf("replayed session error = %v", err)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	service, _, now := authenticationFixture(t, 5)
	issued, err := service.Login(context.Background(), LoginCommand{Username: "operator", Password: "correct-password", Remote: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	if _, err = service.Authenticate(context.Background(), issued.SessionToken); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatalf("expired session error = %v", err)
	}
}

func TestPersistentLoginRateLimit(t *testing.T) {
	service, repository, _ := authenticationFixture(t, 2)
	command := LoginCommand{Username: "operator", Password: "wrong-password", Remote: "127.0.0.1"}
	for index := 0; index < 2; index++ {
		if _, err := service.Login(context.Background(), command); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v", index+1, err)
		}
	}
	if _, err := service.Login(context.Background(), command); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("rate limited error = %v", err)
	}
	if len(repository.audits) != 3 || repository.audits[2].Outcome != "rate_limited" {
		t.Fatalf("audit outcomes = %#v", repository.audits)
	}
}

func TestSessionVersionInvalidatesExistingSession(t *testing.T) {
	service, repository, _ := authenticationFixture(t, 5)
	issued, err := service.Login(context.Background(), LoginCommand{Username: "operator", Password: "correct-password", Remote: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	user := repository.users[1]
	user.SessionVersion++
	repository.users[1] = user
	if _, err = service.Authenticate(context.Background(), issued.SessionToken); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatalf("version mismatch error = %v", err)
	}
}

func TestAdminCannotEscalatePrivileges(t *testing.T) {
	service, err := NewManagement(newMemoryRepository(), testUOW{}, testPasswords{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	actor := domain.Principal{Kind: domain.KindAdmin, InternalID: 2, Roles: []domain.Role{domain.RoleAdmin}, SessionVersion: 1}
	_, err = service.AddUser(context.Background(), actor, AddUserInput{
		Username: "new-user", Password: "a-valid-password", DisplayName: "New", Roles: []domain.Role{domain.RoleSuperAdmin},
	})
	if !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("privilege escalation error = %v", err)
	}
}

func TestBootstrapIsIdempotentAndDoesNotRotateExistingPassword(t *testing.T) {
	repository := newMemoryRepository()
	service, err := NewManagement(repository, testUOW{}, testPasswords{}, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	input := BootstrapInput{Username: "first-admin", Password: "initial-password", DisplayName: "First Admin"}
	first, created, err := service.Bootstrap(context.Background(), input)
	if err != nil || !created {
		t.Fatalf("first bootstrap created=%v err=%v", created, err)
	}
	again, created, err := service.Bootstrap(context.Background(), BootstrapInput{
		Username: "first-admin", Password: "different-password", DisplayName: "Changed",
	})
	if err != nil || created || again.ID != first.ID || again.PasswordHash != "hash:initial-password" {
		t.Fatalf("second bootstrap=%#v created=%v err=%v", again, created, err)
	}
}

type recordingPasswords struct{ verifies int }

func (*recordingPasswords) Hash(value string) (string, error) { return "hash:" + value, nil }
func (passwords *recordingPasswords) Verify(value, encoded string) bool {
	passwords.verifies++
	return encoded == "hash:"+value
}

func TestMalformedUsernameUsesDummyHashRateLimitAndRedactedAudit(t *testing.T) {
	repository := newMemoryRepository()
	passwords := &recordingPasswords{}
	service, err := NewAuthentication(repository, testUOW{}, passwords, AuthenticationConfig{
		SessionTTL: time.Hour, Window: time.Minute, MaxFailures: 5, BlockFor: time.Minute,
		Now: func() time.Time { return testNow }, DummyPHCHash: "hash:dummy-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Login(context.Background(), LoginCommand{
		Username: "bad username", Password: "dummy-password", Remote: "203.0.113.9",
	}); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("malformed login error = %v", err)
	}
	if passwords.verifies != 1 || len(repository.limits) != 1 || len(repository.audits) != 1 {
		t.Fatalf("verifies=%d limits=%d audits=%d", passwords.verifies, len(repository.limits), len(repository.audits))
	}
	auditJSON, err := json.Marshal(repository.audits[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(auditJSON), "bad username") || strings.Contains(string(auditJSON), "203.0.113.9") {
		t.Fatalf("audit contains raw identifier: %s", auditJSON)
	}
}

func TestLoginWithWeComUserIDUnknownDisabledSuccessAndSessionVersion(t *testing.T) {
	service, repository, _ := authenticationFixture(t, 5)
	active := repository.users[1]
	active.WeComUserID = "Alice_01"
	active.SessionVersion = 7
	repository.users[1] = active
	disabledGrant := testNow
	repository.users[2] = domain.User{ID: 2, Username: "disabled", WeComUserID: "Disabled_02", Active: false, LoginEnabled: false, AccessGrantedAt: &disabledGrant,
		SessionVersion: 4, Roles: []domain.Role{domain.RoleAdmin}}

	if _, err := service.LoginWithWeComUserID(context.Background(), WeComLoginCommand{WeComUserID: "Unknown_03"}); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("unknown WeCom user error = %v", err)
	}
	if _, err := service.LoginWithWeComUserID(context.Background(), WeComLoginCommand{WeComUserID: "Disabled_02"}); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("disabled WeCom user error = %v", err)
	}
	issued, err := service.LoginWithWeComUserID(context.Background(), WeComLoginCommand{WeComUserID: " Alice_01 ", Remote: "127.0.0.1"})
	if err != nil || issued.SessionToken == "" || issued.CSRFToken == "" {
		t.Fatalf("issued=%#v err=%v", issued, err)
	}
	if len(repository.sessions) != 1 {
		t.Fatalf("sessions = %#v", repository.sessions)
	}
	for _, session := range repository.sessions {
		if session.SessionVersion != 7 {
			t.Fatalf("session version = %d", session.SessionVersion)
		}
	}
	if got := repository.audits[len(repository.audits)-1].Reason; got != "wecom_oauth" {
		t.Fatalf("success audit reason = %q", got)
	}
}

func TestListUsersAllowsActiveAdminAndReturnsOnlyPublicShape(t *testing.T) {
	repository := newMemoryRepository()
	operatorGrant, adminGrant := testNow, testNow
	repository.users[1] = domain.User{ID: 1, Username: "operator", PasswordHash: "never-return-this",
		DisplayName: "Operator", Active: true, LoginEnabled: true, AccessGrantedAt: &operatorGrant, SessionVersion: 2, Roles: []domain.Role{domain.RoleSuperAdmin}}
	repository.users[2] = domain.User{ID: 2, Username: "admin-user", PasswordHash: "never-return-this",
		DisplayName: "Admin", Active: true, LoginEnabled: true, AccessGrantedAt: &adminGrant, SessionVersion: 1, Roles: []domain.Role{domain.RoleAdmin}}
	service, err := NewManagement(repository, testUOW{}, testPasswords{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if users, listErr := service.ListUsers(context.Background(), domain.Principal{Kind: domain.KindAdmin, InternalID: 2, Roles: []domain.Role{domain.RoleAdmin}, SessionVersion: 1}); listErr != nil || len(users) != 2 {
		t.Fatalf("admin list users=%#v err=%v", users, listErr)
	}
	users, err := service.ListUsers(context.Background(), domain.Principal{Kind: domain.KindAdmin, InternalID: 1, Roles: []domain.Role{domain.RoleSuperAdmin}, SessionVersion: 2})
	if err != nil || len(users) != 2 {
		t.Fatalf("users=%#v err=%v", users, err)
	}
	payload, _ := json.Marshal(users)
	if strings.Contains(string(payload), "never-return-this") || strings.Contains(string(payload), "password") || strings.Contains(string(payload), "digest") {
		t.Fatalf("public users leaked secret fields: %s", payload)
	}
}

func TestAddUserMapsPasswordPolicyFailureToInvalidInput(t *testing.T) {
	service, err := NewManagement(newMemoryRepository(), testUOW{}, credential.PasswordHasher{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AddUser(context.Background(), domain.Principal{
		Kind: domain.KindAdmin, InternalID: 1, Roles: []domain.Role{domain.RoleSuperAdmin},
	}, AddUserInput{Username: "employee", Password: "short", DisplayName: "Employee", Roles: []domain.Role{domain.RoleViewer}})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("password policy error = %v", err)
	}
}

func (repo *memoryRepository) SuperAdminControl(_ context.Context, _ bool) (domain.SuperAdminControl, error) {
	for _, user := range repo.users {
		if user.Active && len(user.Roles) == 1 && user.Roles[0] == domain.RoleSuperAdmin {
			return domain.SuperAdminControl{AdminUserID: user.ID, Version: 1, UpdatedAt: testNow}, nil
		}
	}
	return domain.SuperAdminControl{}, domain.ErrNotFound
}
func (*memoryRepository) InitializeSuperAdminControl(context.Context, int64, time.Time) error {
	return nil
}
func (*memoryRepository) SetSuperAdminControl(context.Context, int64, time.Time) error { return nil }
func (repo *memoryRepository) ReserveGovernanceMutation(_ context.Context, actorID int64, key, action string, targetID int64, digest [32]byte, _ time.Time) (bool, error) {
	return repo.ReserveLoginAccessRequest(context.Background(), actorID, "governance:"+action+":"+key+":"+fmt.Sprint(targetID), digest, testNow)
}
