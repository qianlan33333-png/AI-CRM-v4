package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

func assessmentQuestionnaire(t *testing.T) surveyport.Questionnaire {
	t.Helper()
	weight := 1.0
	config := surveyport.AssessmentConfig{
		TotalScoreTitle: "商业力测评", StrengthCount: 1, WeaknessCount: 1,
		OverallLevels: []surveyport.AssessmentLevel{{MinimumScore: 0, MaximumScore: 4, Title: "起步", Enabled: true, SortOrder: 1}, {MinimumScore: 5, MaximumScore: 10, Title: "成长", Enabled: true, SortOrder: 2, TagCodes: []string{"overall-growth"}}},
		Dimensions: []surveyport.AssessmentDimension{
			{Key: "traffic", Name: "获客", Weight: &weight, ScoringMethod: "sum", CategoryMethod: "most_selected", Enabled: true, ParticipatesInTotalScore: true, ShowInResult: true, SortOrder: 1, TypePriority: []string{"organic", "paid"}, Types: []surveyport.AssessmentType{{Key: "organic", Name: "自然流量", Title: "内容型", Enabled: true, ShowInResult: true, SortOrder: 1, TagCodes: []string{"organic"}}, {Key: "paid", Name: "付费流量", Title: "投放型", Enabled: true, ShowInResult: true, SortOrder: 2}}, Levels: []surveyport.AssessmentLevel{{MinimumScore: 0, MaximumScore: 2, Title: "待提升", Enabled: true, SortOrder: 1}, {MinimumScore: 3, MaximumScore: 5, Title: "优势", Enabled: true, SortOrder: 2}}},
			{Key: "conversion", Name: "成交", Weight: &weight, ScoringMethod: "sum", CategoryMethod: "most_selected", Enabled: true, ParticipatesInTotalScore: true, ShowInResult: true, SortOrder: 2, TypePriority: []string{"consult", "self"}, Types: []surveyport.AssessmentType{{Key: "consult", Name: "咨询", Title: "咨询型", Enabled: true, ShowInResult: true, SortOrder: 1}, {Key: "self", Name: "自助", Title: "自助型", Enabled: true, ShowInResult: true, SortOrder: 2}}, Levels: []surveyport.AssessmentLevel{{MinimumScore: 0, MaximumScore: 2, Title: "待提升", Enabled: true, SortOrder: 1}, {MinimumScore: 3, MaximumScore: 5, Title: "优势", Enabled: true, SortOrder: 2}}},
		},
		FinalRecommendation: surveyport.FinalRecommendation{Enabled: true, Title: "下一步建议", CourseURL: "https://example.com/course"},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return surveyport.Questionnaire{Name: "assessment", Title: "商业力测评", Mode: surveyport.ModeAssessment, AnswerDisplayMode: surveyport.DisplayOneByOne, AssessmentConfig: raw, Slug: "business-assessment", Status: surveyport.StatusDraft, Questions: []surveyport.Question{
		{ID: 1, Type: surveyport.QuestionSingleChoice, Title: "你的获客方式", AssessmentDimensionKey: "traffic", Required: true, SortOrder: 0, Validation: surveyport.Validation{MinimumSelections: intPointer(1), MaximumSelections: intPointer(1)}, Options: []surveyport.Option{{ID: 11, Text: "内容", Score: 4, AssessmentTypeKey: "organic", TagCodes: []string{"content"}, SortOrder: 0}, {ID: 12, Text: "投放", Score: 2, AssessmentTypeKey: "paid", SortOrder: 1}}},
		{ID: 2, Type: surveyport.QuestionSingleChoice, Title: "你的成交方式", AssessmentDimensionKey: "conversion", Required: true, SortOrder: 1, Validation: surveyport.Validation{MinimumSelections: intPointer(1), MaximumSelections: intPointer(1)}, Options: []surveyport.Option{{ID: 21, Text: "咨询", Score: 1, AssessmentTypeKey: "consult", SortOrder: 0}, {ID: 22, Text: "自助", Score: 3, AssessmentTypeKey: "self", SortOrder: 1}}},
	}, ScoreRules: []surveyport.ScoreRule{}}
}

func TestAssessmentScoresDimensionsTypesAndOverallLevel(t *testing.T) {
	questionnaire := assessmentQuestionnaire(t)
	if err := ValidateQuestionnaire(questionnaire); err != nil {
		t.Fatal(err)
	}
	result, err := EvaluateAssessment(questionnaire, []surveyport.SubmissionAnswer{{QuestionID: 1, OptionIDs: []surveyport.ID{11}}, {QuestionID: 2, OptionIDs: []surveyport.ID{21}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalScore != 5 || result.OverallLevel == nil || result.OverallLevel.Title != "成长" {
		t.Fatalf("result=%+v", result)
	}
	if len(result.Dimensions) != 2 || result.Dimensions[0].DominantType == nil || result.Dimensions[0].DominantType.Key != "organic" {
		t.Fatalf("dimensions=%+v", result.Dimensions)
	}
	if len(result.StrengthDimensionKeys) != 1 || result.StrengthDimensionKeys[0] != "traffic" || len(result.WeaknessDimensionKeys) != 1 || result.WeaknessDimensionKeys[0] != "conversion" {
		t.Fatalf("ranking=%+v/%+v", result.StrengthDimensionKeys, result.WeaknessDimensionKeys)
	}
}

func TestAssessmentRejectsOverlapsAndUnknownReferences(t *testing.T) {
	questionnaire := assessmentQuestionnaire(t)
	var config surveyport.AssessmentConfig
	if err := json.Unmarshal(questionnaire.AssessmentConfig, &config); err != nil {
		t.Fatal(err)
	}
	config.OverallLevels[1].MinimumScore = 4
	questionnaire.AssessmentConfig, _ = json.Marshal(config)
	if err := ValidateQuestionnaire(questionnaire); !errors.Is(err, ErrInvalidQuestionnaire) {
		t.Fatalf("overlap error=%v", err)
	}

	questionnaire = assessmentQuestionnaire(t)
	questionnaire.Questions[0].Options[0].AssessmentTypeKey = "missing"
	if err := ValidateQuestionnaire(questionnaire); !errors.Is(err, ErrInvalidQuestionnaire) {
		t.Fatalf("unknown type error=%v", err)
	}
}

func TestAssessmentBusinessKeysPreserveLegacyUnicodeReferences(t *testing.T) {
	questionnaire := assessmentQuestionnaire(t)
	var config surveyport.AssessmentConfig
	if err := json.Unmarshal(questionnaire.AssessmentConfig, &config); err != nil {
		t.Fatal(err)
	}
	const dimensionKey = "维度 1/增长"
	const typeKey = "暖男/女型"
	config.Dimensions[0].Key = dimensionKey
	config.Dimensions[0].Types[0].Key = typeKey
	config.Dimensions[0].TypePriority = []string{typeKey, "paid"}
	questionnaire.Questions[0].AssessmentDimensionKey = dimensionKey
	questionnaire.Questions[0].Options[0].AssessmentTypeKey = typeKey
	questionnaire.AssessmentConfig, _ = json.Marshal(config)

	if err := ValidateQuestionnaire(questionnaire); err != nil {
		t.Fatalf("legacy Chinese/slash assessment keys rejected: %v", err)
	}
	result, err := EvaluateAssessment(questionnaire, []surveyport.SubmissionAnswer{{QuestionID: 1, OptionIDs: []surveyport.ID{11}}, {QuestionID: 2, OptionIDs: []surveyport.ID{21}}})
	if err != nil || len(result.Dimensions) != 2 || result.Dimensions[0].Key != dimensionKey || result.Dimensions[0].DominantType == nil || result.Dimensions[0].DominantType.Key != typeKey {
		t.Fatalf("legacy key result=%+v err=%v", result, err)
	}
}

func TestAssessmentBusinessKeysRejectMalformedValues(t *testing.T) {
	for _, key := range []string{"", " key", "key ", "key\nline", strings.Repeat("字", 129)} {
		if validAssessmentBusinessKey(key) {
			t.Fatalf("accepted malformed assessment key %q", key)
		}
	}
}

func TestAssessmentStillRejectsDuplicateAndBrokenKeyReferences(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*surveyport.Questionnaire, *surveyport.AssessmentConfig)
	}{
		{
			name: "duplicate dimension key",
			mutate: func(questionnaire *surveyport.Questionnaire, config *surveyport.AssessmentConfig) {
				config.Dimensions[1].Key = config.Dimensions[0].Key
			},
		},
		{
			name: "type priority missing type",
			mutate: func(questionnaire *surveyport.Questionnaire, config *surveyport.AssessmentConfig) {
				config.Dimensions[0].TypePriority = []string{"organic", "missing"}
			},
		},
		{
			name: "question missing dimension",
			mutate: func(questionnaire *surveyport.Questionnaire, config *surveyport.AssessmentConfig) {
				questionnaire.Questions[0].AssessmentDimensionKey = "missing"
			},
		},
		{
			name: "option missing type",
			mutate: func(questionnaire *surveyport.Questionnaire, config *surveyport.AssessmentConfig) {
				questionnaire.Questions[0].Options[0].AssessmentTypeKey = "missing"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			questionnaire := assessmentQuestionnaire(t)
			var config surveyport.AssessmentConfig
			if err := json.Unmarshal(questionnaire.AssessmentConfig, &config); err != nil {
				t.Fatal(err)
			}
			test.mutate(&questionnaire, &config)
			questionnaire.AssessmentConfig, _ = json.Marshal(config)
			if err := ValidateQuestionnaire(questionnaire); !errors.Is(err, ErrInvalidQuestionnaire) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDisabledFinalRecommendationCannotHideInvalidURL(t *testing.T) {
	questionnaire := assessmentQuestionnaire(t)
	var config surveyport.AssessmentConfig
	if err := json.Unmarshal(questionnaire.AssessmentConfig, &config); err != nil {
		t.Fatal(err)
	}
	config.FinalRecommendation.Enabled = false
	config.FinalRecommendation.CourseURL = "javascript:alert(1)"
	questionnaire.AssessmentConfig, _ = json.Marshal(config)
	if err := ValidateQuestionnaire(questionnaire); !errors.Is(err, ErrInvalidQuestionnaire) {
		t.Fatalf("unsafe URL error=%v", err)
	}
}
