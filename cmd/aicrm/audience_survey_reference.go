package main

import (
	"context"
	"errors"
	"strconv"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

// audienceSurveyReferenceAdapter keeps the questionnaire hierarchy inside the
// Survey owner. Each title is resolved only within its parent definition, so
// identically named questions or options outside that scope cannot match.
type audienceSurveyReferenceAdapter struct {
	surveys surveyport.DefinitionReader
}

func (a audienceSurveyReferenceAdapter) ResolveAudienceQuestionnaire(ctx context.Context, value string) (string, bool, error) {
	if a.surveys == nil {
		return "", false, errors.New("survey definition reader is required")
	}
	if id, ok := canonicalPositiveSurveyID(value); ok {
		questionnaire, err := a.surveys.Get(ctx, surveyport.ID(id))
		if errors.Is(err, surveyport.ErrNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return strconv.FormatInt(int64(questionnaire.ID), 10), true, nil
	}
	page, err := a.surveys.List(ctx, 100, 0, value, "")
	if err != nil {
		return "", false, err
	}
	if page.Total > int64(len(page.Items)) {
		return "", false, nil
	}
	resolved := ""
	for _, questionnaire := range page.Items {
		if questionnaire.Title != value {
			continue
		}
		id := strconv.FormatInt(int64(questionnaire.ID), 10)
		if resolved != "" && resolved != id {
			return "", false, nil
		}
		resolved = id
	}
	return resolved, resolved != "", nil
}

func (a audienceSurveyReferenceAdapter) ResolveAudienceQuestion(ctx context.Context, questionnaireID, value string) (string, bool, error) {
	questionnaire, found, err := a.questionnaire(ctx, questionnaireID)
	if err != nil || !found {
		return "", found, err
	}
	if id, ok := canonicalPositiveSurveyID(value); ok {
		for _, question := range questionnaire.Questions {
			if int64(question.ID) == id {
				return strconv.FormatInt(id, 10), true, nil
			}
		}
		return "", false, nil
	}
	resolved := ""
	for _, question := range questionnaire.Questions {
		if question.Title != value {
			continue
		}
		id := strconv.FormatInt(int64(question.ID), 10)
		if resolved != "" && resolved != id {
			return "", false, nil
		}
		resolved = id
	}
	return resolved, resolved != "", nil
}

func (a audienceSurveyReferenceAdapter) ResolveAudienceOption(ctx context.Context, questionnaireID, questionID, value string) (string, bool, error) {
	questionnaire, found, err := a.questionnaire(ctx, questionnaireID)
	if err != nil || !found {
		return "", found, err
	}
	questionIDValue, valid := canonicalPositiveSurveyID(questionID)
	if !valid {
		return "", false, nil
	}
	for _, question := range questionnaire.Questions {
		if int64(question.ID) != questionIDValue {
			continue
		}
		if id, ok := canonicalPositiveSurveyID(value); ok {
			for _, option := range question.Options {
				if int64(option.ID) == id {
					return strconv.FormatInt(id, 10), true, nil
				}
			}
			return "", false, nil
		}
		resolved := ""
		for _, option := range question.Options {
			if option.Text != value {
				continue
			}
			id := strconv.FormatInt(int64(option.ID), 10)
			if resolved != "" && resolved != id {
				return "", false, nil
			}
			resolved = id
		}
		return resolved, resolved != "", nil
	}
	return "", false, nil
}

func (a audienceSurveyReferenceAdapter) questionnaire(ctx context.Context, value string) (surveyport.Questionnaire, bool, error) {
	if a.surveys == nil {
		return surveyport.Questionnaire{}, false, errors.New("survey definition reader is required")
	}
	id, valid := canonicalPositiveSurveyID(value)
	if !valid {
		return surveyport.Questionnaire{}, false, nil
	}
	questionnaire, err := a.surveys.Get(ctx, surveyport.ID(id))
	if errors.Is(err, surveyport.ErrNotFound) {
		return surveyport.Questionnaire{}, false, nil
	}
	if err != nil {
		return surveyport.Questionnaire{}, false, err
	}
	return questionnaire, true, nil
}

func canonicalPositiveSurveyID(value string) (int64, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}
