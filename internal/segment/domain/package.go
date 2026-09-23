// Package domain contains Segment-owned audience configuration invariants.
package domain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalid    = errors.New("invalid audience package")
	ErrConflict   = errors.New("audience package version conflict")
	ErrActiveEdit = errors.New("active audience package must be paused before editing")
	ErrArchived   = errors.New("archived audience package is immutable")
)

type Lifecycle string

const (
	Paused   Lifecycle = "paused"
	Active   Lifecycle = "active"
	Archived Lifecycle = "archived"
)

type Group struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	SortOrder        int       `json:"sort_order"`
	Version          int64     `json:"version"`
	CreatedBy        int64     `json:"created_by"`
	CreatedActorKind string    `json:"created_actor_kind"`
	CreatedActorRef  string    `json:"created_actor_ref"`
	UpdatedBy        int64     `json:"updated_by"`
	UpdatedActorKind string    `json:"updated_actor_kind"`
	UpdatedActorRef  string    `json:"updated_actor_ref"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Package struct {
	ID                            int64      `json:"id"`
	GroupID                       *int64     `json:"group_id,omitempty"`
	Code                          string     `json:"code"`
	Name                          string     `json:"name"`
	Lifecycle                     Lifecycle  `json:"lifecycle"`
	Version                       int64      `json:"version"`
	CurrentConfigurationVersionID *int64     `json:"current_configuration_version_id,omitempty"`
	PublishedSnapshotID           *int64     `json:"published_snapshot_id,omitempty"`
	CurrentAutomationBindingID    *int64     `json:"current_automation_binding_id,omitempty"`
	CurrentSenderSetID            *int64     `json:"current_sender_set_id,omitempty"`
	CreatedBy                     int64      `json:"created_by"`
	CreatedActorKind              string     `json:"created_actor_kind"`
	CreatedActorRef               string     `json:"created_actor_ref"`
	UpdatedBy                     int64      `json:"updated_by"`
	UpdatedActorKind              string     `json:"updated_actor_kind"`
	UpdatedActorRef               string     `json:"updated_actor_ref"`
	CreatedAt                     time.Time  `json:"created_at"`
	UpdatedAt                     time.Time  `json:"updated_at"`
	ArchivedAt                    *time.Time `json:"archived_at,omitempty"`
}

type ConfigurationVersion struct {
	ID               int64           `json:"id"`
	PackageID        int64           `json:"package_id"`
	Version          int64           `json:"version"`
	SchemaVersion    int             `json:"schema_version"`
	Definition       json.RawMessage `json:"definition"`
	RefreshCronUTC   string          `json:"refresh_cron_utc,omitempty"`
	RefreshMode      string          `json:"refresh_mode"`
	Digest           [32]byte        `json:"digest"`
	CreatedBy        int64           `json:"created_by"`
	CreatedActorKind string          `json:"created_actor_kind"`
	CreatedActorRef  string          `json:"created_actor_ref"`
	CreatedAt        time.Time       `json:"created_at"`
}

var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,119}$`)

func NewGroup(name string, sortOrder int, actor int64, now time.Time) (Group, error) {
	kind, reference := adminActor(actor)
	return NewGroupWithActor(name, sortOrder, actor, kind, reference, now)
}

func NewGroupWithActor(name string, sortOrder int, actorID int64, actorKind, actorReference string, now time.Time) (Group, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 || sortOrder < 0 || !validActor(actorID, actorKind, actorReference) || now.IsZero() {
		return Group{}, ErrInvalid
	}
	return Group{Name: name, SortOrder: sortOrder, Version: 1, CreatedBy: actorID, CreatedActorKind: actorKind, CreatedActorRef: actorReference, UpdatedBy: actorID, UpdatedActorKind: actorKind, UpdatedActorRef: actorReference, CreatedAt: now, UpdatedAt: now}, nil
}

func NewPackage(code, name string, groupID *int64, actor int64, now time.Time) (Package, error) {
	kind, reference := adminActor(actor)
	return NewPackageWithActor(code, name, groupID, actor, kind, reference, now)
}

func NewPackageWithActor(code, name string, groupID *int64, actorID int64, actorKind, actorReference string, now time.Time) (Package, error) {
	code, name = strings.TrimSpace(strings.ToLower(code)), strings.TrimSpace(name)
	if !codePattern.MatchString(code) || name == "" || len([]rune(name)) > 200 || !validActor(actorID, actorKind, actorReference) || now.IsZero() || invalidOptionalID(groupID) {
		return Package{}, ErrInvalid
	}
	return Package{GroupID: cloneID(groupID), Code: code, Name: name, Lifecycle: Paused, Version: 1, CreatedBy: actorID, CreatedActorKind: actorKind, CreatedActorRef: actorReference, UpdatedBy: actorID, UpdatedActorKind: actorKind, UpdatedActorRef: actorReference, CreatedAt: now, UpdatedAt: now}, nil
}

func (p Package) Copy(code, name string, actor int64, now time.Time) (Package, error) {
	kind, reference := adminActor(actor)
	return p.CopyWithActor(code, name, actor, kind, reference, now)
}

func (p Package) CopyWithActor(code, name string, actorID int64, actorKind, actorReference string, now time.Time) (Package, error) {
	if p.Lifecycle == Archived {
		return Package{}, ErrArchived
	}
	return NewPackageWithActor(code, name, p.GroupID, actorID, actorKind, actorReference, now)
}

func (p *Package) UpdateDetails(name string, groupID *int64, expectedVersion, actor int64, now time.Time) error {
	kind, reference := adminActor(actor)
	return p.UpdateDetailsWithActor(name, groupID, expectedVersion, actor, kind, reference, now)
}

func (p *Package) UpdateDetailsWithActor(name string, groupID *int64, expectedVersion, actorID int64, actorKind, actorReference string, now time.Time) error {
	if p == nil || expectedVersion != p.Version {
		return ErrConflict
	}
	if p.Lifecycle == Archived {
		return ErrArchived
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 200 || invalidOptionalID(groupID) || !validActor(actorID, actorKind, actorReference) || now.IsZero() {
		return ErrInvalid
	}
	p.Name, p.GroupID, p.UpdatedBy, p.UpdatedActorKind, p.UpdatedActorRef, p.UpdatedAt = name, cloneID(groupID), actorID, actorKind, actorReference, now
	p.Version++
	return nil
}

func (p *Package) Transition(target Lifecycle, expectedVersion, actor int64, now time.Time) error {
	kind, reference := adminActor(actor)
	return p.TransitionWithActor(target, expectedVersion, actor, kind, reference, now)
}

func (p *Package) TransitionWithActor(target Lifecycle, expectedVersion, actorID int64, actorKind, actorReference string, now time.Time) error {
	if p == nil || expectedVersion != p.Version {
		return ErrConflict
	}
	if p.Lifecycle == Archived {
		return ErrArchived
	}
	if !validActor(actorID, actorKind, actorReference) || now.IsZero() || (target != Paused && target != Active && target != Archived) {
		return ErrInvalid
	}
	if p.Lifecycle == target {
		return nil
	}
	p.Lifecycle, p.UpdatedBy, p.UpdatedActorKind, p.UpdatedActorRef, p.UpdatedAt = target, actorID, actorKind, actorReference, now
	p.Version++
	if target == Archived {
		archivedAt := now
		p.ArchivedAt = &archivedAt
	}
	return nil
}

func NewConfigurationVersion(packageID, version int64, definition json.RawMessage, refreshCronUTC, refreshMode string, actor int64, now time.Time) (ConfigurationVersion, error) {
	kind, reference := adminActor(actor)
	return NewConfigurationVersionWithActor(packageID, version, definition, refreshCronUTC, refreshMode, actor, kind, reference, now)
}

func NewConfigurationVersionWithActor(packageID, version int64, definition json.RawMessage, refreshCronUTC, refreshMode string, actorID int64, actorKind, actorReference string, now time.Time) (ConfigurationVersion, error) {
	definition = append(json.RawMessage(nil), definition...)
	refreshCronUTC = strings.TrimSpace(refreshCronUTC)
	var object map[string]json.RawMessage
	if packageID < 1 || version < 1 || !validActor(actorID, actorKind, actorReference) || now.IsZero() || json.Unmarshal(definition, &object) != nil || object == nil || len(refreshCronUTC) > 100 || !validRefreshMode(refreshMode) {
		return ConfigurationVersion{}, ErrInvalid
	}
	return ConfigurationVersion{PackageID: packageID, Version: version, SchemaVersion: 1, Definition: definition, RefreshCronUTC: refreshCronUTC, RefreshMode: refreshMode, Digest: sha256.Sum256(definition), CreatedBy: actorID, CreatedActorKind: actorKind, CreatedActorRef: actorReference, CreatedAt: now}, nil
}

func adminActor(id int64) (string, string) {
	if id < 1 {
		return "", ""
	}
	return "admin", "admin:" + strconv.FormatInt(id, 10)
}

func validActor(id int64, kind, reference string) bool {
	switch kind {
	case "admin":
		return id > 0 && reference == "admin:"+strconv.FormatInt(id, 10)
	case "machine":
		if id != 0 || !strings.HasPrefix(reference, "machine:") {
			return false
		}
		client := strings.TrimPrefix(reference, "machine:")
		if client == "" || len(client) > 160 || strings.TrimSpace(client) != client {
			return false
		}
		for _, r := range client {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validRefreshMode(mode string) bool {
	return mode == "manual" || mode == "every_3m" || mode == "daily_0200" || mode == "every_3m_plus_daily_0200" || mode == "legacy_custom"
}

func invalidOptionalID(id *int64) bool { return id != nil && *id < 1 }
func cloneID(id *int64) *int64 {
	if id == nil {
		return nil
	}
	copy := *id
	return &copy
}
