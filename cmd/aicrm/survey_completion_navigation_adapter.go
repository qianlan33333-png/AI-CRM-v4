package main

import (
	"context"
	"errors"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

// surveyCompletionNavigationResolver is Composition's public-only navigation
// boundary. Its values are parsed from a distinct deployment allowlist; it has
// no access to outbound Provider targets, credentials, request parameters, or
// Survey persistence.
type surveyCompletionNavigationResolver struct {
	targets map[string]string
}

func newSurveyCompletionNavigationResolver(targets map[string]string) (surveyCompletionNavigationResolver, error) {
	if targets == nil {
		return surveyCompletionNavigationResolver{targets: map[string]string{}}, nil
	}
	resolved := make(map[string]string, len(targets))
	for reference, target := range targets {
		if reference == "" || target == "" {
			return surveyCompletionNavigationResolver{}, errors.New("invalid survey completion navigation target")
		}
		resolved[reference] = target
	}
	return surveyCompletionNavigationResolver{targets: resolved}, nil
}

func (r surveyCompletionNavigationResolver) ResolvePublicCompletionTarget(ctx context.Context, reference string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	target, found := r.targets[reference]
	return target, found, nil
}

var _ surveyport.PublicCompletionTargetResolver = surveyCompletionNavigationResolver{}
