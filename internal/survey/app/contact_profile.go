package app

import (
	"context"
	"fmt"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

func (s *SubmissionService) BindContactProfiles(c interface {
	customerport.ContactReader
	customerport.ContactWriter
}) {
	s.contacts = c
}

func (s *SubmissionService) updateContactProfile(ctx context.Context, q surveyport.Questionnaire, c surveyport.SubmitCommand, stored surveyport.Submission) error {
	if s.contacts == nil {
		return nil
	}
	for _, question := range q.Questions {
		if question.SidebarProfileField != "contact_phone" && question.SidebarProfileField != "shipping_contact" {
			continue
		}
		for _, answer := range c.Answers {
			if answer.QuestionID != question.ID {
				continue
			}
			update := customerport.ContactUpdate{CustomerID: *c.Identity.CustomerID, Source: fmt.Sprintf("survey:%d:%d", stored.ID, question.ID)}
			if question.SidebarProfileField == "contact_phone" {
				update.MobileE164 = answer.TextValue
			}
			if question.SidebarProfileField == "shipping_contact" && answer.ShippingAddress != nil {
				update.Shipping = answer.ShippingAddress
				update.MobileE164 = answer.ShippingAddress.MobileE164
			}
			return s.contacts.UpdateContactWithin(ctx, update)
		}
	}
	return nil
}

// Only explicitly mapped questions receive defaults. Resolve the live subject
// again inside the read transaction; neither a cookie's old root nor an HTTP
// customer parameter determines whose contact is disclosed.
func (s *SubmissionService) ContactPrefill(ctx context.Context, q surveyport.Questionnaire, identity surveyport.SubmissionIdentity) (map[surveyport.ID]surveyport.SubmissionAnswer, error) {
	result := map[surveyport.ID]surveyport.SubmissionAnswer{}
	var target *surveyport.Question
	for i := range q.Questions {
		v := &q.Questions[i]
		if v.SidebarProfileField == "contact_phone" || v.SidebarProfileField == "shipping_contact" {
			target = v
			break
		}
	}
	if target == nil || s.contacts == nil {
		return result, nil
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		ids, e := s.trustedSubmissionLineage(tx, identity, false)
		if e != nil {
			return e
		}
		profile, e := s.contacts.ReadContactWithin(tx, ids[0], target.SidebarProfileField == "shipping_contact")
		if e != nil {
			return e
		}
		a := surveyport.SubmissionAnswer{QuestionID: target.ID}
		if target.SidebarProfileField == "contact_phone" && profile.MobileE164 != "" {
			a.TextValue = profile.MobileE164[3:]
			result[target.ID] = a
		}
		if target.SidebarProfileField == "shipping_contact" && (profile.Shipping != nil || profile.MobileE164 != "") {
			if profile.Shipping == nil {
				profile.Shipping = &customerport.ShippingContact{MobileE164: profile.MobileE164}
			}
			a.ShippingAddress = profile.Shipping
			result[target.ID] = a
		}
		return nil
	})
	return result, err
}

func (s *SubmissionService) ContactHistoryWithin(ctx context.Context, after int64, allow []surveyport.LegacyContactMapping) ([]customerport.ContactHistory, error) {
	source, ok := s.store.(interface {
		ContactHistoryWithin(context.Context, int64, []surveyport.LegacyContactMapping) ([]customerport.ContactHistory, error)
	})
	if !ok {
		return nil, surveyport.ErrUnavailable
	}
	return source.ContactHistoryWithin(ctx, after, allow)
}
