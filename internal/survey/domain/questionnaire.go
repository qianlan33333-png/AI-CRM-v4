// Package domain contains pure Survey definition and submission invariants.
package domain

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

var (
	ErrInvalidQuestionnaire = errors.New("invalid survey questionnaire")
	ErrInvalidQuestion      = errors.New("invalid survey question")
	ErrInvalidOption        = errors.New("invalid survey option")
	ErrInvalidScoreRule     = errors.New("invalid survey score rule")
	ErrInvalidAnswer        = errors.New("invalid survey answer")
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)
var mobilePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
var opaquePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

const (
	MaximumQuestions       = 200
	MaximumOptions         = 100
	MaximumTextLength      = 10000
	MaximumOtherTextLength = 200
)

func ValidateQuestionnaire(value surveyport.Questionnaire) error {
	if !validText(value.Name, 200) || !validText(value.Title, 500) || !validOptionalText(value.Description, 10000) ||
		!slugPattern.MatchString(value.Slug) || strings.HasSuffix(value.Slug, "-") || !validDisplay(value.AnswerDisplayMode) || !validStatus(value.Status) ||
		(value.Mode != surveyport.ModeSurvey && value.Mode != surveyport.ModeAssessment) || len(value.Questions) == 0 || len(value.Questions) > MaximumQuestions {
		return ErrInvalidQuestionnaire
	}
	seenQuestionIDs := map[surveyport.ID]struct{}{}
	for index, question := range value.Questions {
		if question.SortOrder != index || ValidateQuestion(question, value.Mode) != nil {
			return ErrInvalidQuestionnaire
		}
		if question.ID > 0 {
			if _, exists := seenQuestionIDs[question.ID]; exists {
				return ErrInvalidQuestionnaire
			}
			seenQuestionIDs[question.ID] = struct{}{}
		}
	}
	for index, rule := range value.ScoreRules {
		if rule.SortOrder != index || ValidateScoreRule(rule) != nil {
			return ErrInvalidQuestionnaire
		}
	}
	if scoreRangesOverlap(value.ScoreRules) {
		return ErrInvalidQuestionnaire
	}
	if value.Mode == surveyport.ModeAssessment {
		if _, err := ParseAssessmentConfig(value.AssessmentConfig, value.Questions); err != nil {
			return ErrInvalidQuestionnaire
		}
	} else if len(value.AssessmentConfig) > 0 && string(value.AssessmentConfig) != "{}" && string(value.AssessmentConfig) != "null" {
		return ErrInvalidQuestionnaire
	}
	return nil
}

func ValidateQuestion(value surveyport.Question, mode surveyport.QuestionnaireMode) error {
	if !validText(value.Title, 1000) || value.SortOrder < 0 || !validOptionalText(value.Placeholder, 500) || !validOptionalOpaque(value.SidebarProfileField) {
		return ErrInvalidQuestion
	}
	switch value.Type {
	case surveyport.QuestionSingleChoice, surveyport.QuestionMultiChoice:
		if len(value.Options) < 2 || len(value.Options) > MaximumOptions || value.Validation.MinimumLength != nil || value.Validation.MaximumLength != nil {
			return ErrInvalidQuestion
		}
		minimum, maximum := 1, len(value.Options)
		if value.Validation.MinimumSelections != nil {
			minimum = *value.Validation.MinimumSelections
		}
		if value.Validation.MaximumSelections != nil {
			maximum = *value.Validation.MaximumSelections
		}
		if value.Type == surveyport.QuestionSingleChoice && (minimum != 1 || maximum != 1) || minimum < 0 || maximum < 1 || minimum > maximum || maximum > len(value.Options) {
			return ErrInvalidQuestion
		}
		seenIDs := map[surveyport.ID]struct{}{}
		otherCount := 0
		for index, option := range value.Options {
			if option.SortOrder != index || ValidateOption(option, mode) != nil {
				return ErrInvalidQuestion
			}
			if option.ID > 0 {
				if _, exists := seenIDs[option.ID]; exists {
					return ErrInvalidQuestion
				}
				seenIDs[option.ID] = struct{}{}
			}
			if option.IsOther {
				otherCount++
			}
		}
		if otherCount > 1 {
			return ErrInvalidQuestion
		}
	case surveyport.QuestionTextarea:
		if len(value.Options) != 0 || value.Validation.MinimumSelections != nil || value.Validation.MaximumSelections != nil || !validLengthRange(value.Validation.MinimumLength, value.Validation.MaximumLength, MaximumTextLength) {
			return ErrInvalidQuestion
		}
	case surveyport.QuestionMobile:
		if len(value.Options) != 0 || value.Validation.MinimumSelections != nil || value.Validation.MaximumSelections != nil || value.Validation.MinimumLength != nil || value.Validation.MaximumLength != nil {
			return ErrInvalidQuestion
		}
	default:
		return ErrInvalidQuestion
	}
	if mode == surveyport.ModeAssessment {
		if !validAssessmentBusinessKey(value.AssessmentDimensionKey) {
			return ErrInvalidQuestion
		}
	} else if value.AssessmentDimensionKey != "" {
		return ErrInvalidQuestion
	}
	return nil
}

func ValidateOption(value surveyport.Option, mode surveyport.QuestionnaireMode) error {
	if !validText(value.Text, 1000) || value.SortOrder < 0 || math.IsNaN(value.Score) || math.IsInf(value.Score, 0) || math.Abs(value.Score) > 1_000_000 || !validTags(value.TagCodes) {
		return ErrInvalidOption
	}
	if value.IsOther {
		if !validOptionalText(value.OtherPlaceholder, 500) || value.OtherMaximumLength < 1 || value.OtherMaximumLength > MaximumOtherTextLength {
			return ErrInvalidOption
		}
	} else if value.OtherPlaceholder != "" || value.OtherMaximumLength != 0 {
		return ErrInvalidOption
	}
	if mode == surveyport.ModeAssessment {
		if !validAssessmentBusinessKey(value.AssessmentTypeKey) {
			return ErrInvalidOption
		}
	} else if value.AssessmentTypeKey != "" {
		return ErrInvalidOption
	}
	return nil
}

func ValidateScoreRule(value surveyport.ScoreRule) error {
	if value.SortOrder < 0 || value.MinimumScore == nil && value.MaximumScore == nil || !validTags(value.TagCodes) {
		return ErrInvalidScoreRule
	}
	if value.MinimumScore != nil && (math.IsNaN(*value.MinimumScore) || math.IsInf(*value.MinimumScore, 0)) || value.MaximumScore != nil && (math.IsNaN(*value.MaximumScore) || math.IsInf(*value.MaximumScore, 0)) {
		return ErrInvalidScoreRule
	}
	if value.MinimumScore != nil && value.MaximumScore != nil && *value.MinimumScore > *value.MaximumScore {
		return ErrInvalidScoreRule
	}
	return nil
}

func ValidateAnswers(questions []surveyport.Question, answers []surveyport.SubmissionAnswer) error {
	byQuestion := make(map[surveyport.ID]surveyport.SubmissionAnswer, len(answers))
	for _, answer := range answers {
		if answer.QuestionID < 1 {
			return ErrInvalidAnswer
		}
		if _, exists := byQuestion[answer.QuestionID]; exists {
			return ErrInvalidAnswer
		}
		byQuestion[answer.QuestionID] = answer
	}
	for _, question := range questions {
		answer, supplied := byQuestion[question.ID]
		if question.ID < 1 || !supplied {
			if question.Required {
				return ErrInvalidAnswer
			}
			continue
		}
		delete(byQuestion, question.ID)
		if validateAnswer(question, answer) != nil {
			return ErrInvalidAnswer
		}
	}
	if len(byQuestion) != 0 {
		return ErrInvalidAnswer
	}
	return nil
}

func validateAnswer(question surveyport.Question, answer surveyport.SubmissionAnswer) error {
	switch question.Type {
	case surveyport.QuestionSingleChoice, surveyport.QuestionMultiChoice:
		minimum, maximum := 1, len(question.Options)
		if question.Validation.MinimumSelections != nil {
			minimum = *question.Validation.MinimumSelections
		}
		if question.Validation.MaximumSelections != nil {
			maximum = *question.Validation.MaximumSelections
		}
		if !question.Required && len(answer.OptionIDs) == 0 && answer.TextValue == "" {
			return nil
		}
		if len(answer.OptionIDs) < minimum || len(answer.OptionIDs) > maximum {
			return ErrInvalidAnswer
		}
		validOptions := make(map[surveyport.ID]surveyport.Option, len(question.Options))
		for _, option := range question.Options {
			validOptions[option.ID] = option
		}
		seen := map[surveyport.ID]struct{}{}
		otherSelected := false
		for _, optionID := range answer.OptionIDs {
			option, exists := validOptions[optionID]
			if !exists || optionID < 1 {
				return ErrInvalidAnswer
			}
			if _, duplicate := seen[optionID]; duplicate {
				return ErrInvalidAnswer
			}
			seen[optionID] = struct{}{}
			if option.IsOther {
				otherSelected = true
				if !validText(answer.TextValue, option.OtherMaximumLength) {
					return ErrInvalidAnswer
				}
			}
		}
		if !otherSelected && answer.TextValue != "" {
			return ErrInvalidAnswer
		}
	case surveyport.QuestionTextarea:
		if len(answer.OptionIDs) != 0 {
			return ErrInvalidAnswer
		}
		if answer.TextValue == "" && !question.Required {
			return nil
		}
		minimum, maximum := 0, MaximumTextLength
		if question.Validation.MinimumLength != nil {
			minimum = *question.Validation.MinimumLength
		}
		if question.Validation.MaximumLength != nil {
			maximum = *question.Validation.MaximumLength
		}
		length := utf8.RuneCountInString(answer.TextValue)
		if answer.TextValue != strings.TrimSpace(answer.TextValue) || !utf8.ValidString(answer.TextValue) || length < minimum || length > maximum {
			return ErrInvalidAnswer
		}
	case surveyport.QuestionMobile:
		if len(answer.OptionIDs) != 0 {
			return ErrInvalidAnswer
		}
		if answer.TextValue == "" && !question.Required {
			return nil
		}
		if !mobilePattern.MatchString(answer.TextValue) {
			return ErrInvalidAnswer
		}
	default:
		return ErrInvalidAnswer
	}
	return nil
}

func validDisplay(value surveyport.AnswerDisplayMode) bool {
	return value == surveyport.DisplayAllInOne || value == surveyport.DisplayOneByOne
}
func validStatus(value surveyport.QuestionnaireStatus) bool {
	return value == surveyport.StatusDraft || value == surveyport.StatusPublished || value == surveyport.StatusDisabled || value == surveyport.StatusArchived
}
func validText(value string, maximum int) bool {
	return value != "" && validOptionalText(value, maximum)
}
func validOptionalText(value string, maximum int) bool {
	return value == strings.TrimSpace(value) && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximum
}

// validAssessmentBusinessKey preserves the frozen editor's legacy local keys.
// They identify a dimension or type only within one assessment definition;
// they are not security-sensitive opaque references such as tags, profile
// fields, template IDs, or completion parameters.
func validAssessmentBusinessKey(value string) bool {
	if !validText(value, 128) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validOpaque(value string) bool {
	return opaquePattern.MatchString(value) && !strings.Contains(value, "://")
}
func validOptionalOpaque(value string) bool { return value == "" || validOpaque(value) }
func validLengthRange(minimum, maximum *int, ceiling int) bool {
	min, max := 0, ceiling
	if minimum != nil {
		min = *minimum
	}
	if maximum != nil {
		max = *maximum
	}
	return min >= 0 && max >= 1 && min <= max && max <= ceiling
}
func validTags(values []string) bool {
	if len(values) > 100 {
		return false
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if !validOpaque(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func scoreRangesOverlap(rules []surveyport.ScoreRule) bool {
	copyRules := append([]surveyport.ScoreRule(nil), rules...)
	sort.Slice(copyRules, func(i, j int) bool {
		if copyRules[i].MinimumScore == nil {
			return true
		}
		if copyRules[j].MinimumScore == nil {
			return false
		}
		return *copyRules[i].MinimumScore < *copyRules[j].MinimumScore
	})
	for index := 1; index < len(copyRules); index++ {
		previous, current := copyRules[index-1], copyRules[index]
		if previous.MaximumScore == nil || current.MinimumScore == nil || *current.MinimumScore <= *previous.MaximumScore {
			return true
		}
	}
	return false
}

func validCourseURL(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && len(value) <= 2048
}

func canonicalJSON(value any) (json.RawMessage, error) { return json.Marshal(value) }
