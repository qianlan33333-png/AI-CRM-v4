package main

import (
	"context"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestSurveyCompletionNavigationResolverUsesOnlyConfiguredOpaqueReference(t *testing.T) {
	targets, err := platformconfig.ParseSurveyCompletionNavigationTargets(`{"completion.done":"https://survey.example.test/finished"}`)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := newSurveyCompletionNavigationResolver(targets)
	if err != nil {
		t.Fatal(err)
	}
	if got, found, resolveErr := resolver.ResolvePublicCompletionTarget(context.Background(), "completion.done"); resolveErr != nil || !found || got != "https://survey.example.test/finished" {
		t.Fatalf("got=%q found=%v err=%v", got, found, resolveErr)
	}
	if got, found, resolveErr := resolver.ResolvePublicCompletionTarget(context.Background(), "https://receiver.example.test/provider"); resolveErr != nil || found || got != "" {
		t.Fatalf("unconfigured endpoint leaked: got=%q found=%v err=%v", got, found, resolveErr)
	}
}
