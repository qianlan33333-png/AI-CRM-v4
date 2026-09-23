// Package radar owns content-radar definitions and local lifecycle rules.
package radar

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxNameRunes        = 120
	MaxTitleRunes       = 200
	MaxDescriptionRunes = 2_000
	MaxDestinationBytes = 2_048
)

var (
	ErrInvalidArgument   = errors.New("radar: invalid argument")
	ErrInvalidStatus     = errors.New("radar: invalid status")
	ErrInvalidTransition = errors.New("radar: invalid status transition")
	ErrVersionConflict   = errors.New("radar: version conflict")
	// New codes keep the rd_ prefix and at least 96 bits of entropy. The
	// alphanumeric alternative is the immutable 1-8 character code generated
	// by the production v1 Radar and is accepted only so existing share URLs
	// survive the one-time migration; new writes never generate this shape.
	publicCodePattern = regexp.MustCompile(`^(?:rd_[A-Za-z0-9_-]{16,64}|[A-Za-z0-9]{1,8})$`)
)

type RadarID int64
type MediaID int64
type PublicCode string
type LinkVersion int64

func (id RadarID) Valid() bool          { return id > 0 }
func (id MediaID) Valid() bool          { return id > 0 }
func (version LinkVersion) Valid() bool { return version > 0 }
func (code PublicCode) Valid() bool     { return publicCodePattern.MatchString(string(code)) }

type ContentType string

const (
	ContentTypeLink  ContentType = "link"
	ContentTypeImage ContentType = "image"
	ContentTypePDF   ContentType = "pdf"
)

func (kind ContentType) Valid() bool {
	return kind == ContentTypeLink || kind == ContentTypeImage || kind == ContentTypePDF
}

type AuthPolicy string

const (
	AuthPolicyAnonymous       AuthPolicy = "anonymous"
	AuthPolicyUnionIDRequired AuthPolicy = "unionid_required"
)

func (policy AuthPolicy) Valid() bool {
	return policy == AuthPolicyAnonymous || policy == AuthPolicyUnionIDRequired
}

type Status string

const (
	StatusDraft    Status = "draft"
	StatusEnabled  Status = "enabled"
	StatusDisabled Status = "disabled"
)

func (status Status) Valid() bool {
	return status == StatusDraft || status == StatusEnabled || status == StatusDisabled
}

type Content struct {
	Type           ContentType `json:"type"`
	DestinationURL string      `json:"destination_url,omitempty"`
	MediaID        MediaID     `json:"media_id,omitempty"`
}

func (content Content) Validate() error {
	if !content.Type.Valid() {
		return ErrInvalidArgument
	}
	switch content.Type {
	case ContentTypeLink:
		if content.MediaID != 0 || validatePublicHTTPSURL(content.DestinationURL) != nil {
			return ErrInvalidArgument
		}
	case ContentTypeImage, ContentTypePDF:
		if !content.MediaID.Valid() || content.DestinationURL != "" {
			return ErrInvalidArgument
		}
	}
	return nil
}

type Link struct {
	ID          RadarID     `json:"id"`
	PublicCode  PublicCode  `json:"public_code"`
	Name        string      `json:"name"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Content     Content     `json:"content"`
	AuthPolicy  AuthPolicy  `json:"auth_policy"`
	Status      Status      `json:"status"`
	Version     LinkVersion `json:"version"`
	CreatedBy   int64       `json:"created_by"`
	UpdatedBy   int64       `json:"updated_by"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

func NewDraft(id RadarID, code PublicCode, name, title, description string, content Content, authPolicy AuthPolicy, now time.Time) (Link, error) {
	link := Link{
		ID:          id,
		PublicCode:  code,
		Name:        name,
		Title:       title,
		Description: description,
		Content:     content,
		AuthPolicy:  authPolicy,
		Status:      StatusDraft,
		Version:     1,
		CreatedAt:   now.UTC(),
		UpdatedAt:   now.UTC(),
	}
	if err := link.Validate(); err != nil {
		return Link{}, err
	}
	return link, nil
}

func (link Link) Validate() error {
	if !link.ID.Valid() || !link.PublicCode.Valid() || !link.AuthPolicy.Valid() || !link.Status.Valid() || !link.Version.Valid() || link.CreatedBy < 0 || link.UpdatedBy < 0 {
		return ErrInvalidArgument
	}
	if !validText(link.Name, MaxNameRunes, false) || !validText(link.Title, MaxTitleRunes, false) || !validText(link.Description, MaxDescriptionRunes, true) {
		return ErrInvalidArgument
	}
	if link.CreatedAt.IsZero() || link.UpdatedAt.IsZero() || link.UpdatedAt.Before(link.CreatedAt) {
		return ErrInvalidArgument
	}
	return link.Content.Validate()
}

func (link Link) Transition(expected LinkVersion, target Status, now time.Time) (Link, bool, error) {
	if err := link.Validate(); err != nil {
		return Link{}, false, err
	}
	if expected != link.Version {
		return Link{}, false, ErrVersionConflict
	}
	if !target.Valid() {
		return Link{}, false, ErrInvalidStatus
	}
	if target == link.Status {
		return link, false, nil
	}
	if !allowedTransition(link.Status, target) {
		return Link{}, false, ErrInvalidTransition
	}
	if now.IsZero() || now.Before(link.UpdatedAt) {
		return Link{}, false, ErrInvalidArgument
	}
	link.Status = target
	link.Version++
	link.UpdatedAt = now.UTC()
	return link, true, nil
}

type Revision struct {
	Name        string
	Title       string
	Description string
	Content     Content
	AuthPolicy  AuthPolicy
}

// Revise changes mutable content fields while deliberately exposing no way to
// replace the public code.
func (link Link) Revise(expected LinkVersion, revision Revision, now time.Time) (Link, error) {
	if err := link.Validate(); err != nil {
		return Link{}, err
	}
	if expected != link.Version {
		return Link{}, ErrVersionConflict
	}
	if now.IsZero() || now.Before(link.UpdatedAt) {
		return Link{}, ErrInvalidArgument
	}
	link.Name = revision.Name
	link.Title = revision.Title
	link.Description = revision.Description
	link.Content = revision.Content
	link.AuthPolicy = revision.AuthPolicy
	link.Version++
	link.UpdatedAt = now.UTC()
	if err := link.Validate(); err != nil {
		return Link{}, err
	}
	return link, nil
}

func allowedTransition(current, target Status) bool {
	switch target {
	case StatusEnabled:
		return current == StatusDraft || current == StatusDisabled
	case StatusDisabled:
		return current == StatusEnabled
	default:
		return false
	}
}

func validText(value string, maximum int, optional bool) bool {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) || utf8.RuneCountInString(value) > maximum {
		return false
	}
	if !optional && value == "" {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validatePublicHTTPSURL(value string) error {
	if value == "" || len(value) > MaxDestinationBytes || value != strings.TrimSpace(value) || !utf8.ValidString(value) || strings.Contains(value, `\`) {
		return ErrInvalidArgument
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.Host == "" || parsed.User != nil {
		return ErrInvalidArgument
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".test") || strings.HasSuffix(host, ".invalid") || strings.HasSuffix(host, ".") || net.ParseIP(host) != nil {
		return ErrInvalidArgument
	}
	if strings.ContainsAny(parsed.Host, "%@") || strings.HasSuffix(parsed.Host, ":") {
		return ErrInvalidArgument
	}
	if port := parsed.Port(); port != "" {
		portNumber, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || portNumber == 0 {
			return ErrInvalidArgument
		}
	}
	if !strings.Contains(host, ".") {
		return ErrInvalidArgument
	}
	return nil
}
