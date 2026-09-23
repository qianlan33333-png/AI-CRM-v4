package app

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrProjectionUnavailable = errors.New("adminops projection is unavailable")

// ProjectionService is the composed AdminOps-owned read/observation adapter.
// It is intentionally narrower than port.Repository: the PR09 Config page
// may consume only these redacted facts, while the broader AdminOps control
// plane remains unmounted until its own owner tables and HTTP contract exist.
type ProjectionService struct {
	uow   platformport.UnitOfWork
	store adminopsport.ProjectionStore
	now   func() time.Time
}

func NewProjectionService(uow platformport.UnitOfWork, store adminopsport.ProjectionStore) (*ProjectionService, error) {
	if uow == nil || store == nil {
		return nil, ErrProjectionUnavailable
	}
	return &ProjectionService{uow: uow, store: store, now: time.Now}, nil
}

// ListReleaseProjections implements the Config port. Invalid persisted data
// fails closed instead of being silently skipped or converted to an empty
// placeholder response.
func (service *ProjectionService) ListReleaseProjections(ctx context.Context) ([]configport.ReleaseProjection, error) {
	if service == nil || service.uow == nil || service.store == nil {
		return nil, ErrProjectionUnavailable
	}
	var stored []adminopsport.ReleaseProjection
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		stored, err = service.store.ListReleaseProjections(tx)
		return err
	}); err != nil {
		return nil, err
	}
	result := make([]configport.ReleaseProjection, 0, len(stored))
	for _, item := range stored {
		if !validReleaseProjection(item) {
			return nil, ErrProjectionUnavailable
		}
		result = append(result, configport.ReleaseProjection{ID: item.ID, ReleaseSHA: item.ReleaseSHA, Status: item.Status, ObservedAt: item.ObservedAt.UTC()})
	}
	return result, nil
}

// ListDiagnosticSnapshots implements the Config port. Details are never read
// from PostgreSQL, so there is no path for secret/PII-bearing JSON to reach
// the HTTP response.
func (service *ProjectionService) ListDiagnosticSnapshots(ctx context.Context) ([]configport.DiagnosticProjection, error) {
	if service == nil || service.uow == nil || service.store == nil {
		return nil, ErrProjectionUnavailable
	}
	var stored []adminopsport.DiagnosticSnapshot
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		stored, err = service.store.ListDiagnosticSnapshots(tx)
		return err
	}); err != nil {
		return nil, err
	}
	result := make([]configport.DiagnosticProjection, 0, len(stored))
	for _, item := range stored {
		if !validDiagnosticSnapshot(item) {
			return nil, ErrProjectionUnavailable
		}
		result = append(result, configport.DiagnosticProjection{ID: item.ID, Key: item.Key, Status: item.Status, ObservedAt: item.ObservedAt.UTC()})
	}
	return result, nil
}

// RecordReleaseProjection stores one truthful local observation. The caller
// cannot provide details, actor data, or an external-effect result.
func (service *ProjectionService) RecordReleaseProjection(ctx context.Context, item adminopsport.ReleaseProjection) (adminopsport.ReleaseProjection, error) {
	if service == nil || service.uow == nil || service.store == nil || item.ID != 0 {
		return adminopsport.ReleaseProjection{}, ErrProjectionUnavailable
	}
	if item.ObservedAt.IsZero() {
		item.ObservedAt = time.Now().UTC()
		if service.now != nil {
			item.ObservedAt = service.now().UTC()
		}
	}
	if !validReleaseProjection(item) {
		return adminopsport.ReleaseProjection{}, ErrProjectionUnavailable
	}
	var result adminopsport.ReleaseProjection
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = service.store.RecordReleaseProjection(tx, item)
		return err
	})
	if err != nil || !validReleaseProjection(result) || result.ID < 1 {
		if err != nil {
			return adminopsport.ReleaseProjection{}, err
		}
		return adminopsport.ReleaseProjection{}, ErrProjectionUnavailable
	}
	return result, nil
}

// RecordDiagnosticSnapshot stores one truthful local runtime observation.
// Only a bounded key/status/timestamp enters the AdminOps table.
func (service *ProjectionService) RecordDiagnosticSnapshot(ctx context.Context, item adminopsport.DiagnosticSnapshot) (adminopsport.DiagnosticSnapshot, error) {
	if service == nil || service.uow == nil || service.store == nil || item.ID != 0 {
		return adminopsport.DiagnosticSnapshot{}, ErrProjectionUnavailable
	}
	if item.ObservedAt.IsZero() {
		item.ObservedAt = time.Now().UTC()
		if service.now != nil {
			item.ObservedAt = service.now().UTC()
		}
	}
	if !validDiagnosticSnapshot(item) {
		return adminopsport.DiagnosticSnapshot{}, ErrProjectionUnavailable
	}
	var result adminopsport.DiagnosticSnapshot
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = service.store.RecordDiagnosticSnapshot(tx, item)
		return err
	})
	if err != nil || !validDiagnosticSnapshot(result) || result.ID < 1 {
		if err != nil {
			return adminopsport.DiagnosticSnapshot{}, err
		}
		return adminopsport.DiagnosticSnapshot{}, ErrProjectionUnavailable
	}
	return result, nil
}

// DiagnosticsService is the explicit diagnostics composition seam. Keeping it
// separate from the Config reader makes it possible for readiness/runtime
// code to record observations without granting that code any raw details.
type DiagnosticsService struct {
	projections *ProjectionService
}

func NewDiagnosticsService(projections *ProjectionService) (*DiagnosticsService, error) {
	if projections == nil {
		return nil, ErrProjectionUnavailable
	}
	return &DiagnosticsService{projections: projections}, nil
}

func (service *DiagnosticsService) Record(ctx context.Context, item adminopsport.DiagnosticSnapshot) (adminopsport.DiagnosticSnapshot, error) {
	if service == nil || service.projections == nil {
		return adminopsport.DiagnosticSnapshot{}, ErrProjectionUnavailable
	}
	return service.projections.RecordDiagnosticSnapshot(ctx, item)
}

func validReleaseProjection(item adminopsport.ReleaseProjection) bool {
	return item.ID >= 0 && safeIdentifier(item.ReleaseSHA, 200) && oneOf(item.Status, "observed", "active", "superseded", "failed") && validObservedAt(item.ObservedAt)
}

func validDiagnosticSnapshot(item adminopsport.DiagnosticSnapshot) bool {
	return item.ID >= 0 && safeIdentifier(item.Key, 120) && oneOf(item.Status, "ok", "warning", "error", "unknown") && validObservedAt(item.ObservedAt)
}

func validObservedAt(value time.Time) bool {
	return !value.IsZero()
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func safeIdentifier(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, item := range value {
		if unicode.IsControl(item) || unicode.IsSpace(item) || !(item >= 'a' && item <= 'z') && !(item >= 'A' && item <= 'Z') && !(item >= '0' && item <= '9') && !strings.ContainsRune("._:/-", item) {
			return false
		}
	}
	normalized := strings.ToLower(strings.NewReplacer("_", "", ".", "", ":", "", "/", "", "-", "").Replace(value))
	for _, forbidden := range []string{"secret", "token", "password", "cookie", "privatekey", "openid", "externaluserid", "phone", "mobile", "email", "apikey", "authorization", "credential"} {
		if strings.Contains(normalized, forbidden) {
			return false
		}
	}
	return true
}

var _ configport.SafeProjectionReader = (*ProjectionService)(nil)
