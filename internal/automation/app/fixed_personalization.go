package app

import (
	"context"
	"strings"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

// SetCustomerNames binds the existing Customer read Port at composition time.
// Reads require the caller's transaction and never resolve or create identity.
func (s *RuntimeService) SetCustomerNames(reader customerport.DirectoryDisplayNameReader) error {
	if s == nil || reader == nil {
		return ErrRuntimeNotReady
	}
	s.customerNames = reader
	return nil
}

func hasCustomerNameVariable(text string) bool {
	return strings.Contains(text, "{{用户名}}") || strings.Contains(text, "{{客户名}}")
}

// fixedContentWithin renders only text, individually for the chosen canonical
// customers. Its caller persists those bodies in the same acceptance UoW.
func (s *RuntimeService) fixedContentWithin(ctx context.Context, content automationport.FixedContentPackage, ids []customerdomain.CustomerID) ([]automationport.FixedContentPackage, error) {
	result := make([]automationport.FixedContentPackage, len(ids))
	if !hasCustomerNameVariable(content.ContentText) {
		for i := range result {
			result[i] = content
		}
		return result, nil
	}
	if s.customerNames == nil {
		return nil, ErrRuntimeNotReady
	}
	names := make(map[customerdomain.CustomerID]string, len(ids))
	// The Customer Port's existing DisplayNames read accepts at most 200 IDs.
	// Chunking does not restrict Automation's existing recipient ceiling.
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		values, err := s.customerNames.DisplayNames(ctx, ids[start:end])
		if err != nil {
			return nil, err
		}
		for _, id := range ids[start:end] {
			names[id] = values[id]
		}
	}
	for i, id := range ids {
		personalized := content
		personalized.ContentText = automationport.RenderCustomerNamePrompt(content.ContentText, strings.TrimSpace(names[id]))
		// Honor the same published-content limit without silently truncating a
		// customer's name or changing unrelated material references.
		if !validText(personalized.ContentText, maxContentText) {
			return nil, ErrRuntimeInvalid
		}
		result[i] = personalized
	}
	return result, nil
}
