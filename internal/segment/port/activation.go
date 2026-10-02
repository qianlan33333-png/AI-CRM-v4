package port

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"time"
)

// ActivationConditions extends existing templates without introducing an
// external member importer or another customer identifier.
type ActivationConditions struct {
	ElapsedHoursMin     *int64              `json:"elapsed_hours_min,omitempty"`
	ElapsedHoursMax     *int64              `json:"elapsed_hours_max,omitempty"`
	RegistrationStatus  string              `json:"registration_status,omitempty"`
	QuestionnaireStatus string              `json:"questionnaire_status,omitempty"`
	QuestionnaireIDs    []string            `json:"questionnaire_ids,omitempty"`
	TokenUsageStatus    string              `json:"token_usage_status,omitempty"`
	EnrollmentOverride  *EnrollmentOverride `json:"enrollment_override,omitempty"`
}
type EnrollmentOverride struct {
	At       time.Time `json:"at"`
	OrderIDs []int64   `json:"order_ids"`
}

var ErrInvalidActivation = errors.New("invalid activation conditions")

func ParseActivation(raw json.RawMessage, paid bool) (ActivationConditions, error) {
	var v ActivationConditions
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || d.Decode(&v) != nil {
		return v, ErrInvalidActivation
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return v, ErrInvalidActivation
	}
	for _, n := range []*int64{v.ElapsedHoursMin, v.ElapsedHoursMax} {
		if n != nil && (*n < 0 || *n > math.MaxInt64/int64(time.Hour)) {
			return v, ErrInvalidActivation
		}
	}
	min := int64(0)
	if v.ElapsedHoursMin != nil {
		min = *v.ElapsedHoursMin
	}
	if v.ElapsedHoursMax != nil && *v.ElapsedHoursMax <= min {
		return v, ErrInvalidActivation
	}
	if v.RegistrationStatus == "" {
		v.RegistrationStatus = "any"
	}
	if v.QuestionnaireStatus == "" {
		v.QuestionnaireStatus = "any"
	}
	if v.TokenUsageStatus == "" {
		v.TokenUsageStatus = "any"
	}
	if v.RegistrationStatus != "any" && v.RegistrationStatus != "registered" && v.RegistrationStatus != "unregistered" {
		return v, ErrInvalidActivation
	}
	if v.QuestionnaireStatus != "any" && v.QuestionnaireStatus != "submitted" && v.QuestionnaireStatus != "unsubmitted" {
		return v, ErrInvalidActivation
	}
	if v.TokenUsageStatus != "any" && v.TokenUsageStatus != "unused_7d" {
		return v, ErrInvalidActivation
	}
	seen := map[string]bool{}
	for _, id := range v.QuestionnaireIDs {
		n, e := strconv.ParseInt(id, 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != id || seen[id] {
			return v, ErrInvalidActivation
		}
		seen[id] = true
	}
	if paid {
		if v.TokenUsageStatus != "any" || (v.QuestionnaireStatus != "any" && len(v.QuestionnaireIDs) == 0) {
			return v, ErrInvalidActivation
		}
	} else if v.RegistrationStatus != "any" || v.QuestionnaireStatus != "any" || len(v.QuestionnaireIDs) > 0 || v.EnrollmentOverride != nil {
		return v, ErrInvalidActivation
	}
	if v.EnrollmentOverride != nil {
		if v.EnrollmentOverride.At.IsZero() || len(v.EnrollmentOverride.OrderIDs) == 0 {
			return v, ErrInvalidActivation
		}
		ids := map[int64]bool{}
		for _, id := range v.EnrollmentOverride.OrderIDs {
			if id <= 0 || ids[id] {
				return v, ErrInvalidActivation
			}
			ids[id] = true
		}
	}
	return v, nil
}
func (v ActivationConditions) MatchesElapsed(anchor, reference time.Time) bool {
	if v.ElapsedHoursMin == nil && v.ElapsedHoursMax == nil {
		return true
	}
	if anchor.IsZero() || anchor.After(reference) {
		return false
	}
	elapsed := reference.Sub(anchor)
	return (v.ElapsedHoursMin == nil || elapsed >= time.Duration(*v.ElapsedHoursMin)*time.Hour) && (v.ElapsedHoursMax == nil || elapsed < time.Duration(*v.ElapsedHoursMax)*time.Hour)
}
