package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

var ErrInvalidAssessment = errors.New("invalid survey assessment")

const maximumAssessmentConfigBytes = 256 << 10

func ParseAssessmentConfig(raw json.RawMessage, questions []surveyport.Question) (surveyport.AssessmentConfig, error) {
	var config surveyport.AssessmentConfig
	if len(raw) == 0 || len(raw) > maximumAssessmentConfigBytes || !json.Valid(raw) {
		return config, ErrInvalidAssessment
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return surveyport.AssessmentConfig{}, ErrInvalidAssessment
	}
	if !validText(config.TotalScoreTitle, 500) || config.StrengthCount < 0 || config.StrengthCount > 20 || config.WeaknessCount < 0 || config.WeaknessCount > 20 ||
		!validOptionalOpaque(config.TemplateID) || !validOptionalText(config.TemplateName, 500) || !validOptionalOpaque(config.AssetKind) ||
		len(config.Dimensions) == 0 || len(config.Dimensions) > 100 || len(config.OverallLevels) == 0 || len(config.OverallLevels) > 100 || len(config.Recommendations) > 100 {
		return surveyport.AssessmentConfig{}, ErrInvalidAssessment
	}
	if config.SourceQuestionnaireID != nil && *config.SourceQuestionnaireID < 1 || !validRecommendation(config.FinalRecommendation) || validateLevels(config.OverallLevels) != nil {
		return surveyport.AssessmentConfig{}, ErrInvalidAssessment
	}
	dimensions := make(map[string]surveyport.AssessmentDimension, len(config.Dimensions))
	for index, dimension := range config.Dimensions {
		if dimension.SortOrder != index+1 || !validAssessmentBusinessKey(dimension.Key) || !validText(dimension.Name, 500) || !validOptionalText(dimension.Summary, 10000) ||
			dimension.ScoringMethod != "sum" || dimension.CategoryMethod != "most_selected" || len(dimension.Types) == 0 || len(dimension.Types) > 100 || len(dimension.Levels) == 0 || len(dimension.Levels) > 100 {
			return surveyport.AssessmentConfig{}, ErrInvalidAssessment
		}
		if dimension.Weight != nil && (math.IsNaN(*dimension.Weight) || math.IsInf(*dimension.Weight, 0) || *dimension.Weight < 0 || *dimension.Weight > 1000) {
			return surveyport.AssessmentConfig{}, ErrInvalidAssessment
		}
		if _, exists := dimensions[dimension.Key]; exists || validateLevels(dimension.Levels) != nil {
			return surveyport.AssessmentConfig{}, ErrInvalidAssessment
		}
		types := make(map[string]struct{}, len(dimension.Types))
		for typeIndex, assessmentType := range dimension.Types {
			if assessmentType.SortOrder != typeIndex+1 || !validAssessmentBusinessKey(assessmentType.Key) || !validText(assessmentType.Name, 500) || !validText(assessmentType.Title, 500) ||
				!validOptionalText(assessmentType.Greeting, 10000) || !validOptionalText(assessmentType.Summary, 10000) || !validOptionalText(assessmentType.Diagnosis, 10000) ||
				!validOptionalText(assessmentType.ProblemHint, 10000) || !validOptionalText(assessmentType.RecommendedAction, 10000) || !validOptionalText(assessmentType.CourseName, 500) ||
				!validCourseURL(assessmentType.CourseURL) || !validOptionalText(assessmentType.CTAText, 500) || !validTags(assessmentType.TagCodes) {
				return surveyport.AssessmentConfig{}, ErrInvalidAssessment
			}
			if _, exists := types[assessmentType.Key]; exists {
				return surveyport.AssessmentConfig{}, ErrInvalidAssessment
			}
			types[assessmentType.Key] = struct{}{}
		}
		if len(dimension.TypePriority) != len(types) {
			return surveyport.AssessmentConfig{}, ErrInvalidAssessment
		}
		prioritySeen := map[string]struct{}{}
		for _, key := range dimension.TypePriority {
			if _, exists := types[key]; !exists {
				return surveyport.AssessmentConfig{}, ErrInvalidAssessment
			}
			if _, duplicate := prioritySeen[key]; duplicate {
				return surveyport.AssessmentConfig{}, ErrInvalidAssessment
			}
			prioritySeen[key] = struct{}{}
		}
		dimensions[dimension.Key] = dimension
	}
	for _, question := range questions {
		dimension, exists := dimensions[question.AssessmentDimensionKey]
		if !exists {
			return surveyport.AssessmentConfig{}, ErrInvalidAssessment
		}
		types := map[string]struct{}{}
		for _, assessmentType := range dimension.Types {
			types[assessmentType.Key] = struct{}{}
		}
		for _, option := range question.Options {
			if _, exists := types[option.AssessmentTypeKey]; !exists {
				return surveyport.AssessmentConfig{}, ErrInvalidAssessment
			}
		}
	}
	return config, nil
}

func EvaluateAssessment(questionnaire surveyport.Questionnaire, answers []surveyport.SubmissionAnswer) (surveyport.AssessmentResult, error) {
	if questionnaire.Mode != surveyport.ModeAssessment || ValidateQuestionnaire(questionnaire) != nil || ValidateAnswers(questionnaire.Questions, answers) != nil {
		return surveyport.AssessmentResult{}, ErrInvalidAssessment
	}
	config, err := ParseAssessmentConfig(questionnaire.AssessmentConfig, questionnaire.Questions)
	if err != nil {
		return surveyport.AssessmentResult{}, err
	}
	answerByQuestion := make(map[surveyport.ID]surveyport.SubmissionAnswer, len(answers))
	for _, answer := range answers {
		answerByQuestion[answer.QuestionID] = answer
	}
	type accumulator struct {
		dimension  surveyport.AssessmentDimension
		score      float64
		typeCounts map[string]int
		tags       map[string]struct{}
	}
	accumulators := make(map[string]*accumulator, len(config.Dimensions))
	for _, dimension := range config.Dimensions {
		accumulators[dimension.Key] = &accumulator{dimension: dimension, typeCounts: map[string]int{}, tags: map[string]struct{}{}}
	}
	for _, question := range questionnaire.Questions {
		answer, exists := answerByQuestion[question.ID]
		if !exists {
			continue
		}
		selected := make(map[surveyport.ID]struct{}, len(answer.OptionIDs))
		for _, id := range answer.OptionIDs {
			selected[id] = struct{}{}
		}
		accumulator := accumulators[question.AssessmentDimensionKey]
		for _, option := range question.Options {
			if _, exists := selected[option.ID]; !exists {
				continue
			}
			accumulator.score += option.Score
			accumulator.typeCounts[option.AssessmentTypeKey]++
			for _, tag := range option.TagCodes {
				accumulator.tags[tag] = struct{}{}
			}
		}
	}
	result := surveyport.AssessmentResult{Dimensions: make([]surveyport.AssessmentDimensionResult, 0, len(config.Dimensions)), TagCodes: []string{}}
	allTags := map[string]struct{}{}
	for _, dimension := range config.Dimensions {
		accumulator := accumulators[dimension.Key]
		dimensionResult := surveyport.AssessmentDimensionResult{Key: dimension.Key, Name: dimension.Name, Score: accumulator.score, TagCodes: sortedTags(accumulator.tags)}
		if level := matchingLevel(dimension.Levels, accumulator.score); level != nil {
			dimensionResult.Level = level
			for _, tag := range level.TagCodes {
				accumulator.tags[tag] = struct{}{}
				allTags[tag] = struct{}{}
			}
		}
		if dominant := dominantType(dimension, accumulator.typeCounts); dominant != nil {
			dimensionResult.DominantType = dominant
			for _, tag := range dominant.TagCodes {
				accumulator.tags[tag] = struct{}{}
				allTags[tag] = struct{}{}
			}
		}
		dimensionResult.TagCodes = sortedTags(accumulator.tags)
		for tag := range accumulator.tags {
			allTags[tag] = struct{}{}
		}
		result.Dimensions = append(result.Dimensions, dimensionResult)
		if dimension.Enabled && dimension.ParticipatesInTotalScore {
			weight := 1.0
			if dimension.Weight != nil {
				weight = *dimension.Weight
			}
			result.TotalScore += accumulator.score * weight
		}
	}
	result.OverallLevel = matchingLevel(config.OverallLevels, result.TotalScore)
	if result.OverallLevel != nil {
		for _, tag := range result.OverallLevel.TagCodes {
			allTags[tag] = struct{}{}
		}
	}
	result.StrengthDimensionKeys, result.WeaknessDimensionKeys = rankedDimensions(result.Dimensions, config.StrengthCount, config.WeaknessCount)
	result.TagCodes = sortedTags(allTags)
	return result, nil
}

func EvaluateScoreRules(rules []surveyport.ScoreRule, score float64) []string {
	for _, rule := range rules {
		if rule.MinimumScore != nil && score < *rule.MinimumScore || rule.MaximumScore != nil && score > *rule.MaximumScore {
			continue
		}
		return append([]string(nil), rule.TagCodes...)
	}
	return []string{}
}

func validateLevels(levels []surveyport.AssessmentLevel) error {
	enabled := make([]surveyport.AssessmentLevel, 0, len(levels))
	for index, level := range levels {
		if level.SortOrder != index+1 || math.IsNaN(level.MinimumScore) || math.IsInf(level.MinimumScore, 0) || math.IsNaN(level.MaximumScore) || math.IsInf(level.MaximumScore, 0) || level.MinimumScore > level.MaximumScore ||
			!validText(level.Title, 500) || !validOptionalText(level.Greeting, 10000) || !validOptionalText(level.Summary, 10000) || !validOptionalText(level.RecommendedAction, 10000) ||
			!validOptionalText(level.CourseName, 500) || !validCourseURL(level.CourseURL) || !validOptionalText(level.CTAText, 500) || !validTags(level.TagCodes) {
			return ErrInvalidAssessment
		}
		if level.Enabled {
			enabled = append(enabled, level)
		}
	}
	sort.Slice(enabled, func(i, j int) bool { return enabled[i].MinimumScore < enabled[j].MinimumScore })
	for index := 1; index < len(enabled); index++ {
		if enabled[index].MinimumScore <= enabled[index-1].MaximumScore {
			return ErrInvalidAssessment
		}
	}
	return nil
}

func validRecommendation(value surveyport.FinalRecommendation) bool {
	if !validOptionalText(value.Title, 500) || !validOptionalText(value.Description, 10000) || !validOptionalText(value.CourseName, 500) || !validCourseURL(value.CourseURL) || !validOptionalText(value.CTAText, 500) {
		return false
	}
	return !value.Enabled || value.Title != ""
}

func matchingLevel(levels []surveyport.AssessmentLevel, score float64) *surveyport.AssessmentLevel {
	for _, level := range levels {
		if level.Enabled && score >= level.MinimumScore && score <= level.MaximumScore {
			copyLevel := level
			return &copyLevel
		}
	}
	return nil
}

func dominantType(dimension surveyport.AssessmentDimension, counts map[string]int) *surveyport.AssessmentType {
	bestKey, bestCount := "", 0
	for _, key := range dimension.TypePriority {
		if counts[key] > bestCount {
			bestKey, bestCount = key, counts[key]
		}
	}
	if bestKey == "" {
		return nil
	}
	for _, assessmentType := range dimension.Types {
		if assessmentType.Enabled && assessmentType.Key == bestKey {
			copyType := assessmentType
			return &copyType
		}
	}
	return nil
}

func rankedDimensions(values []surveyport.AssessmentDimensionResult, strengths, weaknesses int) ([]string, []string) {
	ordered := append([]surveyport.AssessmentDimensionResult(nil), values...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score == ordered[j].Score {
			return strings.Compare(ordered[i].Key, ordered[j].Key) < 0
		}
		return ordered[i].Score > ordered[j].Score
	})
	if strengths > len(ordered) {
		strengths = len(ordered)
	}
	if weaknesses > len(ordered) {
		weaknesses = len(ordered)
	}
	high := make([]string, strengths)
	low := make([]string, weaknesses)
	for index := 0; index < strengths; index++ {
		high[index] = ordered[index].Key
	}
	for index := 0; index < weaknesses; index++ {
		low[index] = ordered[len(ordered)-1-index].Key
	}
	return high, low
}

func sortedTags(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
