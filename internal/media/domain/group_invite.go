package domain

import (
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

const (
	MaxGroupInviteTitleBytes       = 128
	MaxGroupInviteDescriptionBytes = 512
	MaxGroupInviteURLBytes         = 2048
)

var ErrInvalidGroupInvite = errors.New("invalid group invite")

func NewGroupInvite(command mediaport.GroupInviteCreateCommand, now time.Time) (mediaport.GroupInvite, error) {
	enabled := true
	if command.Enabled != nil {
		enabled = *command.Enabled
	}
	item := mediaport.GroupInvite{
		Name: strings.TrimSpace(command.Name), Title: strings.TrimSpace(command.Title),
		Description: strings.TrimSpace(command.Description), JoinURL: strings.TrimSpace(command.JoinURL),
		CoverImageID: command.CoverImageID, Enabled: enabled, CreatedBy: command.Actor, UpdatedBy: command.Actor,
		Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if item.Name == "" {
		item.Name = item.Title
	}
	if !ValidGroupInvite(item, false) {
		return mediaport.GroupInvite{}, ErrInvalidGroupInvite
	}
	return item, nil
}

func ApplyGroupInvitePatch(current mediaport.GroupInvite, patch mediaport.GroupInvitePatch, actor int64, now time.Time) (mediaport.GroupInvite, error) {
	if EmptyGroupInvitePatch(patch) || !ValidGroupInvite(current, true) || current.ArchivedAt != nil || actor < 1 || now.IsZero() {
		return mediaport.GroupInvite{}, ErrInvalidGroupInvite
	}
	if patch.Name != nil {
		current.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Title != nil {
		current.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Description != nil {
		current.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.JoinURL != nil {
		current.JoinURL = strings.TrimSpace(*patch.JoinURL)
	}
	if patch.CoverImageID != nil {
		current.CoverImageID = *patch.CoverImageID
	}
	if patch.Enabled != nil {
		current.Enabled = *patch.Enabled
	}
	if current.Name == "" {
		current.Name = current.Title
	}
	current.UpdatedBy, current.UpdatedAt, current.Version = actor, now.UTC(), current.Version+1
	if !ValidGroupInvite(current, true) {
		return mediaport.GroupInvite{}, ErrInvalidGroupInvite
	}
	return current, nil
}

func ArchiveGroupInvite(current mediaport.GroupInvite, actor int64, now time.Time) (mediaport.GroupInvite, error) {
	if !ValidGroupInvite(current, true) || current.ArchivedAt != nil || actor < 1 || now.IsZero() {
		return mediaport.GroupInvite{}, ErrInvalidGroupInvite
	}
	archived := now.UTC()
	current.Enabled, current.UpdatedBy, current.UpdatedAt, current.ArchivedAt = false, actor, archived, &archived
	current.Version++
	return current, nil
}

func EmptyGroupInvitePatch(patch mediaport.GroupInvitePatch) bool {
	return patch.Name == nil && patch.Title == nil && patch.Description == nil && patch.JoinURL == nil && patch.CoverImageID == nil && patch.Enabled == nil
}

func ValidGroupInvite(item mediaport.GroupInvite, persisted bool) bool {
	if persisted && (item.ID < 1 || item.Version < 1 || item.CreatedAt.IsZero()) {
		return false
	}
	if item.Name == "" || item.Title == "" || item.CoverImageID < 0 || item.CreatedBy < 1 || item.UpdatedBy < 1 || item.UpdatedAt.IsZero() ||
		!utf8.ValidString(item.Name) || !utf8.ValidString(item.Title) || !utf8.ValidString(item.Description) ||
		len(item.Title) > MaxGroupInviteTitleBytes || len(item.Description) > MaxGroupInviteDescriptionBytes || !ValidGroupInviteJoinURL(item.JoinURL) {
		return false
	}
	return item.ArchivedAt == nil || !item.Enabled && !item.ArchivedAt.IsZero()
}

// ValidGroupInviteJoinURL is the single Media validator for a Group invite.
// A group invite may be either a native WeCom /gm link or an AI-CRM public
// /gi/ landing page. Both forms are HTTPS-only and reject credentials,
// queries, fragments, and encoded path ambiguity.
func ValidGroupInviteJoinURL(raw string) bool {
	if raw == "" || raw != strings.TrimSpace(raw) || len(raw) > MaxGroupInviteURLBytes || !utf8.ValidString(raw) {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" {
		return false
	}
	if parsed.Host == "work.weixin.qq.com" {
		return strings.HasPrefix(parsed.Path, "/gm/") && len(parsed.Path) > len("/gm/") && !strings.Contains(parsed.Path[len("/gm/"):], "/")
	}
	return parsed.Host == "www.youcangogogo.com" && strings.HasPrefix(parsed.Path, "/gi/") && len(parsed.Path) > len("/gi/") && !strings.Contains(parsed.Path[len("/gi/"):], "/")
}
