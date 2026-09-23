package domain

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidChannel    = errors.New("invalid channel")
	ErrInvalidAssignment = errors.New("invalid channel assignment")
	ErrImmutableCode     = errors.New("channel code is immutable")
	ErrInvalidTransition = errors.New("invalid channel status transition")
	ErrVersionConflict   = errors.New("channel version conflict")
)

type ChannelType string
type CarrierType string
type Status string
type AssignmentMode string
type AssignmentStrategy string

const (
	ChannelQRCode      ChannelType = "qrcode"
	ChannelAcquisition ChannelType = "wecom_customer_acquisition"

	CarrierQRCode CarrierType = "qrcode"
	CarrierLink   CarrierType = "link"

	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
	StatusArchived Status = "archived"

	AssignmentSingle AssignmentMode = "single_owner"
	AssignmentMulti  AssignmentMode = "multi_staff"

	StrategyRatio     AssignmentStrategy = "ratio"
	StrategyCapSwitch AssignmentStrategy = "cap_switch"
)

const (
	MaximumAssignees  = 5
	MaximumMediaItems = 12
)

var opaqueCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)

type MediaReferences struct {
	Images       []int64
	MiniPrograms []int64
	Attachments  []int64
	GroupInvites []int64
}

type Assignee struct {
	StaffID     int64
	Priority    int
	Ratio       int
	MaxScans24h int
}

type Assignment struct {
	Mode           AssignmentMode
	Strategy       AssignmentStrategy
	OverflowPolicy string
	Assignees      []Assignee
}

type Config struct {
	Type              ChannelType
	Carrier           CarrierType
	Name              string
	SceneValue        string
	QRCodeURL         string
	CustomerChannel   string
	LinkURL           string
	FinalURL          string
	WelcomeMessage    string
	Media             MediaReferences
	AutoAcceptFriend  bool
	EntryTagID        int64
	EntryTagName      string
	EntryTagGroupName string
	Assignment        Assignment
}

type Channel struct {
	ID            int64
	Code          string
	Status        Status
	Config        Config
	ConfigVersion int64
	Version       int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type CreateChannel struct {
	Code   string
	Status Status
	Config Config
}

type UpdateChannel struct {
	ExpectedVersion int64
	Code            string
	Status          Status
	Config          Config
}

func NewChannel(command CreateChannel, now time.Time) (Channel, error) {
	channel := Channel{
		Code: command.Code, Status: command.Status, Config: command.Config,
		ConfigVersion: 1, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if err := ValidateChannel(channel); err != nil {
		return Channel{}, err
	}
	return channel, nil
}

func (channel Channel) Update(command UpdateChannel, now time.Time) (Channel, error) {
	if channel.Version < 1 || command.ExpectedVersion != channel.Version {
		return Channel{}, ErrVersionConflict
	}
	if command.Code != channel.Code {
		return Channel{}, ErrImmutableCode
	}
	// Archive controls availability, not editability. Configuration changes
	// preserve the requested status and create a new immutable version.
	updated := channel
	updated.Status = command.Status
	updated.Config = command.Config
	updated.ConfigVersion++
	updated.Version++
	updated.UpdatedAt = now.UTC()
	if err := ValidateChannel(updated); err != nil {
		return Channel{}, err
	}
	return updated, nil
}

func (channel Channel) CanPublish() bool {
	return channel.Status == StatusActive && ValidateChannel(channel) == nil
}

func ValidateChannel(channel Channel) error {
	if !opaqueCode.MatchString(channel.Code) || !validStatus(channel.Status) || channel.ConfigVersion < 1 || channel.Version < 1 ||
		channel.CreatedAt.IsZero() || channel.UpdatedAt.IsZero() || channel.UpdatedAt.Before(channel.CreatedAt) {
		return ErrInvalidChannel
	}
	return ValidateConfig(channel.Config)
}

func ValidateConfig(config Config) error {
	if !validText(config.Name, 200) || !validOptionalText(config.SceneValue, 10000) || !validOptionalText(config.CustomerChannel, 10000) ||
		!validOptionalText(config.WelcomeMessage, 10000) || !validOptionalHTTPS(config.QRCodeURL) || !validOptionalHTTPS(config.LinkURL) || !validOptionalHTTPS(config.FinalURL) ||
		!validTypeCarrier(config.Type, config.Carrier) || !validMedia(config.Media) || !validEntryTag(config) || ValidateAssignment(config.Assignment) != nil {
		return ErrInvalidChannel
	}
	if err := ValidateWelcomeMessageTemplate(config.WelcomeMessage); err != nil {
		return errors.Join(ErrInvalidChannel, err)
	}
	return nil
}

func ValidateAssignment(assignment Assignment) error {
	if assignment.Mode != AssignmentSingle && assignment.Mode != AssignmentMulti || assignment.Strategy != StrategyRatio && assignment.Strategy != StrategyCapSwitch ||
		!validOptionalOpaque(assignment.OverflowPolicy, 128) || len(assignment.Assignees) < 1 || len(assignment.Assignees) > MaximumAssignees ||
		assignment.Mode == AssignmentSingle && len(assignment.Assignees) != 1 {
		return ErrInvalidAssignment
	}
	seen := make(map[int64]struct{}, len(assignment.Assignees))
	ratio := 0
	for index, assignee := range assignment.Assignees {
		if assignee.StaffID < 1 || assignee.Priority != index+1 {
			return ErrInvalidAssignment
		}
		if _, exists := seen[assignee.StaffID]; exists {
			return ErrInvalidAssignment
		}
		seen[assignee.StaffID] = struct{}{}
		switch assignment.Strategy {
		case StrategyRatio:
			if assignee.Ratio < 1 || assignee.Ratio > 100 || assignee.MaxScans24h != 0 {
				return ErrInvalidAssignment
			}
			ratio += assignee.Ratio
		case StrategyCapSwitch:
			if assignee.MaxScans24h < 1 || assignee.Ratio != 0 {
				return ErrInvalidAssignment
			}
		}
	}
	if assignment.Strategy == StrategyRatio && ratio != 100 {
		return ErrInvalidAssignment
	}
	return nil
}

func validTypeCarrier(channelType ChannelType, carrier CarrierType) bool {
	return channelType == ChannelQRCode && carrier == CarrierQRCode || channelType == ChannelAcquisition && carrier == CarrierLink
}

func validStatus(status Status) bool {
	return status == StatusActive || status == StatusInactive || status == StatusArchived
}

func validText(value string, maximum int) bool {
	return value != "" && validOptionalText(value, maximum)
}

func validOptionalText(value string, maximum int) bool {
	return utf8.ValidString(value) && value == strings.TrimSpace(value) && utf8.RuneCountInString(value) <= maximum
}

func validOptionalOpaque(value string, maximum int) bool {
	return value == "" || validText(value, maximum) && !strings.Contains(value, "://")
}

func validOptionalHTTPS(value string) bool {
	if value == "" {
		return true
	}
	if !validText(value, 10000) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func validMedia(media MediaReferences) bool {
	return validIDs(media.Images) && validIDs(media.MiniPrograms) && validIDs(media.Attachments) && validIDs(media.GroupInvites)
}

func validIDs(values []int64) bool {
	if len(values) > MaximumMediaItems {
		return false
	}
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value < 1 {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validEntryTag(config Config) bool {
	if config.EntryTagID == 0 {
		return config.EntryTagName == "" && config.EntryTagGroupName == ""
	}
	return config.EntryTagID > 0 && validText(config.EntryTagName, 200) && validText(config.EntryTagGroupName, 200)
}
