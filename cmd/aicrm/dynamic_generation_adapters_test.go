package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type dynamicGenerationSurveyStub struct {
	query surveyport.CustomerHistoryQuery
}

func (s *dynamicGenerationSurveyStub) CustomerHistoryWindow(_ context.Context, query surveyport.CustomerHistoryQuery) (surveyport.CustomerHistoryWindow, error) {
	s.query = query
	return surveyport.CustomerHistoryWindow{Items: []surveyport.Submission{{QuestionnaireTitle: "入营问卷", SubmittedAt: time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC), Answers: []surveyport.AnswerSnapshot{{QuestionTitle: "目标", TextValueMasked: "增长"}}}}}, nil
}

type dynamicGenerationMessagesStub struct{ query archiveport.CustomerQuery }

func (s *dynamicGenerationMessagesStub) CustomerMessages(_ context.Context, query archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
	s.query = query
	return archiveport.CustomerPage{Items: []archiveport.MessageItem{{OccurredAt: time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC), Direction: "inbound", ContentText: "想了解课程"}}}, nil
}
func (dynamicGenerationMessagesStub) CustomerStaff(context.Context, customerdomain.CustomerID) ([]archiveport.StaffOption, error) {
	return nil, nil
}

type dynamicGenerationTagsStub struct{}

func (dynamicGenerationTagsStub) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (dynamicGenerationTagsStub) CustomerTags(context.Context, customerdomain.CustomerID) (customerport.TagPage, error) {
	return customerport.TagPage{Items: []customerport.TagItem{{GroupName: "阶段", Name: "活跃", Status: "active"}}}, nil
}

type dynamicGenerationProfilesStub struct{}

func (dynamicGenerationProfilesStub) ReadSidebarProfile(context.Context, customerdomain.CustomerID) (customerport.SidebarProfile, error) {
	return customerport.SidebarProfile{ActivationState: "activated", Status: "active", UpdatedAt: time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)}, nil
}
func (dynamicGenerationProfilesStub) UpdateSidebarProfile(context.Context, customerport.SidebarProfileUpdate) (customerport.SidebarProfile, error) {
	return customerport.SidebarProfile{}, nil
}
func (dynamicGenerationProfilesStub) BindSidebarPhone(context.Context, customerport.SidebarPhoneBind) (customerport.SidebarPhoneResult, error) {
	return customerport.SidebarPhoneResult{}, nil
}

func TestDynamicGenerationContextFreezesOnlyApprovedReadPorts(t *testing.T) {
	surveys := &dynamicGenerationSurveyStub{}
	messages := &dynamicGenerationMessagesStub{}
	watermark := time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)
	reader := dynamicGenerationContextAdapter{questionnaires: surveys, messages: messages, tags: dynamicGenerationTagsStub{}, profiles: dynamicGenerationProfilesStub{}, now: func() time.Time { return watermark }}
	value, err := reader.FreezeGenerationContext(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if surveys.query.CustomerID != 42 || surveys.query.Limit != 20 || !surveys.query.Watermark.Equal(watermark) || messages.query.CustomerID != 42 || messages.query.Limit != 20 || !messages.query.Watermark.Equal(watermark) || messages.query.Watermark.Location() != time.UTC || !value.Valid() || !strings.Contains(value.Questionnaire, "入营问卷") || !strings.Contains(value.RecentChats, "想了解课程") || !strings.Contains(value.Tags, "阶段/活跃") || !strings.Contains(value.Activation, "activated") {
		t.Fatalf("queries survey=%+v messages=%+v value=%+v", surveys.query, messages.query, value)
	}
}

func TestBoundedGenerationTextPreservesUTF8AndByteLimit(t *testing.T) {
	value := boundedGenerationText(strings.Repeat("你", 9000))
	if len(value) > 16000 || !strings.HasSuffix(value, "...") || !utf8.ValidString(value) {
		t.Fatalf("value length=%d valid=%t", len(value), utf8.ValidString(value))
	}
}

func TestGenerationQuestionnaireUsesLatestSubmissionOnly(t *testing.T) {
	old := surveyport.Submission{ID: 1, QuestionnaireTitle: "旧问卷", SubmittedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Answers: []surveyport.AnswerSnapshot{{QuestionTitle: "方向", TextValueMasked: "旧方向"}}}
	latest := surveyport.Submission{ID: 3, QuestionnaireTitle: "最新问卷", SubmittedAt: old.SubmittedAt.Add(time.Hour), Answers: []surveyport.AnswerSnapshot{{QuestionTitle: "方向", TextValueMasked: "新的副业方向"}}}
	other := latest
	other.ID, other.QuestionnaireTitle = 2, "同秒较早提交"
	value := generationQuestionnaireContext([]surveyport.Submission{old, other, latest})
	if !strings.Contains(value, "最新问卷") || !strings.Contains(value, "新的副业方向") || strings.Contains(value, "旧方向") || strings.Contains(value, "同秒较早提交") {
		t.Fatal("mixed stale questionnaire context:", value)
	}
	if generationQuestionnaireContext(nil) != "" {
		t.Fatal("missing questionnaire must remain unknown")
	}
}

func TestGeneratedOutboundTemplateRetainsFrozenSources(t *testing.T) {
	freezer := automationOutboundContentFreezer{}
	raw := json.RawMessage(`{"schema_version":1,"sources":[{"kind":"miniprogram","id":1070,"source_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
	digest := sha256.Sum256(raw)
	completed, actual, err := freezer.CompleteGeneratedContent(raw, digest, "看到你正在探索副业方向，先从一个小验证开始吧。")
	if err != nil || sha256.Sum256(completed) != actual {
		t.Fatal("completion failed", err)
	}
	var snapshot frozenAutomationContent
	if json.Unmarshal(completed, &snapshot) != nil || len(snapshot.Sources) != 1 || snapshot.Sources[0].ID != 1070 || !strings.Contains(snapshot.ContentText, "副业方向") {
		t.Fatal("frozen material lost or output missing")
	}
	if _, _, err = freezer.CompleteGeneratedContent(append(raw, ' '), digest, "text"); err == nil {
		t.Fatal("accepted changed template")
	}
	if _, _, err = freezer.CompleteGeneratedContent(raw, digest, "  "); err == nil {
		t.Fatal("accepted empty generation")
	}
}
