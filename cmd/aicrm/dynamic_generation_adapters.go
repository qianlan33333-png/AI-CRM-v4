package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

// dynamicGenerationContextAdapter is a Composition-owned read aggregation. It
// deliberately speaks only the four narrow, stable read ports that B06
// permits. It neither resolves identities nor performs a Provider write.
type dynamicGenerationContextAdapter struct {
	questionnaires surveyport.CustomerHistoryReader
	messages       archiveport.CustomerMessageReader
	tags           customerport.CustomerTagReader
	profiles       customerport.SidebarProfileService
	now            func() time.Time
}

func (a dynamicGenerationContextAdapter) FreezeGenerationContext(ctx context.Context, customerID customerdomain.CustomerID) (automationport.GenerationContext, error) {
	if customerID < 1 || a.questionnaires == nil || a.messages == nil || a.tags == nil || a.profiles == nil {
		return automationport.GenerationContext{}, fmt.Errorf("dynamic generation context reader is unavailable")
	}
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	watermark := now().UTC()
	questionnaires, err := a.questionnaires.CustomerHistoryWindow(ctx, surveyport.CustomerHistoryQuery{CustomerID: int64(customerID), Limit: 20, Watermark: watermark})
	if err != nil {
		return automationport.GenerationContext{}, fmt.Errorf("read questionnaire context: %w", err)
	}
	messages, err := a.messages.CustomerMessages(ctx, archiveport.CustomerQuery{CustomerID: customerID, Limit: 20, Watermark: watermark})
	if err != nil {
		return automationport.GenerationContext{}, fmt.Errorf("read recent chat context: %w", err)
	}
	tags, err := a.tags.CustomerTags(ctx, customerID)
	if err != nil {
		return automationport.GenerationContext{}, fmt.Errorf("read customer tag context: %w", err)
	}
	profile, err := a.profiles.ReadSidebarProfile(ctx, customerID)
	if err != nil {
		return automationport.GenerationContext{}, fmt.Errorf("read activation context: %w", err)
	}
	value := automationport.GenerationContext{
		Questionnaire: generationQuestionnaireContext(questionnaires.Items),
		RecentChats:   generationChatContext(messages.Items),
		Tags:          generationTagContext(tags.Items),
		Activation:    generationActivationContext(profile),
	}
	if !value.Valid() {
		return automationport.GenerationContext{}, fmt.Errorf("dynamic generation context exceeds limit")
	}
	return value, nil
}

func generationQuestionnaireContext(items []surveyport.Submission) string {
	var lines []string
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("[%s] %s", item.SubmittedAt.UTC().Format(time.RFC3339), item.QuestionnaireTitle))
		for _, answer := range item.Answers {
			values := make([]string, 0, len(answer.SelectedOptions)+1)
			for _, option := range answer.SelectedOptions {
				values = append(values, option.OptionText)
			}
			if answer.TextValueMasked != "" {
				values = append(values, answer.TextValueMasked)
			}
			if len(values) > 0 {
				lines = append(lines, answer.QuestionTitle+"："+strings.Join(values, "、"))
			}
		}
	}
	return boundedGenerationText(strings.Join(lines, "\n"))
}

func generationChatContext(items []archiveport.MessageItem) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		content := strings.TrimSpace(item.ContentText)
		if content == "" {
			content = "[" + item.MessageType + "]"
		}
		lines = append(lines, fmt.Sprintf("[%s][%s] %s", item.OccurredAt.UTC().Format(time.RFC3339), item.Direction, content))
	}
	return boundedGenerationText(strings.Join(lines, "\n"))
}

func generationTagContext(items []customerport.TagItem) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		label := item.Name
		if item.GroupName != "" {
			label = item.GroupName + "/" + label
		}
		if item.Status != "" {
			label += "（" + item.Status + "）"
		}
		lines = append(lines, label)
	}
	return boundedGenerationText(strings.Join(lines, "\n"))
}

func generationActivationContext(profile customerport.SidebarProfile) string {
	return boundedGenerationText(fmt.Sprintf("activation_status=%s\ncustomer_status=%s\nprofile_updated_at=%s", profile.ActivationState, profile.Status, profile.UpdatedAt.UTC().Format(time.RFC3339)))
}

// Context.Valid uses bytes, so truncate on UTF-8 boundaries rather than
// slicing bytes and risking an invalid prompt body.
func boundedGenerationText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 16000 {
		return value
	}
	var out strings.Builder
	out.Grow(16000)
	for _, runeValue := range value {
		encoded := string(runeValue)
		if out.Len()+len(encoded) > 15997 {
			break
		}
		out.WriteString(encoded)
	}
	return strings.TrimSpace(out.String()) + "..."
}

var _ automationport.GenerationContextReader = dynamicGenerationContextAdapter{}
