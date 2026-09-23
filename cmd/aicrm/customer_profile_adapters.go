package main

import (
	"context"
	"errors"
	"sort"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type canonicalCustomerAdapter struct{ reader identityquery.Reader }

func (adapter canonicalCustomerAdapter) ResolveCanonicalCustomer(ctx context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	detail, err := adapter.reader.Customer(ctx, id)
	if err != nil {
		if errors.Is(err, identityquery.ErrNotFound) {
			return customerport.CanonicalCustomer{}, customerapp.ErrNotFound
		}
		return customerport.CanonicalCustomer{}, err
	}
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: detail.CanonicalCustomerID, Merged: detail.CanonicalCustomerID != id}, nil
}

func (adapter canonicalCustomerAdapter) ResolveCanonicalCustomers(ctx context.Context, ids []customerdomain.CustomerID) ([]customerport.CanonicalCustomer, error) {
	reader, ok := adapter.reader.(identityquery.CanonicalCustomerReader)
	if !ok {
		result := make([]customerport.CanonicalCustomer, 0, len(ids))
		for _, id := range ids {
			item, err := adapter.ResolveCanonicalCustomer(ctx, id)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
		return result, nil
	}
	resolved, err := reader.CanonicalCustomers(ctx, ids)
	if err != nil {
		if errors.Is(err, identityquery.ErrNotFound) {
			return nil, customerapp.ErrNotFound
		}
		return nil, err
	}
	result := make([]customerport.CanonicalCustomer, len(resolved))
	for index, item := range resolved {
		result[index] = customerport.CanonicalCustomer{RequestedCustomerID: item.RequestedCustomerID, CustomerID: item.CustomerID, Merged: item.RequestedCustomerID != item.CustomerID}
	}
	return result, nil
}

type customerOwnerAdapter struct {
	uow          platformport.UnitOfWork
	observations wecomport.CustomerProfileObservationReader
	users        interface {
		UserByID(context.Context, int64, bool) (accessdomain.User, error)
		UserByWeComUserID(context.Context, string, bool) (accessdomain.User, error)
	}
	owners interface {
		LocalOwner(context.Context, customerdomain.CustomerID, bool) (customerport.LocalOwner, bool, error)
	}
}

func (customerOwnerAdapter) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}

func (adapter customerOwnerAdapter) CustomerOwners(ctx context.Context, id customerdomain.CustomerID) (customerport.OwnerPage, error) {
	page := customerport.OwnerPage{Items: []customerport.OwnerItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		// The explicit CRM owner is authoritative after a local-only or
		// accepted WeCom handoff. Follow observations remain useful context but
		// cannot overwrite this Customer-owned projection.
		if adapter.owners != nil {
			local, found, localErr := adapter.owners.LocalOwner(tx, id, false)
			if localErr != nil {
				return localErr
			}
			if found {
				user, lookupErr := adapter.users.UserByID(tx, local.StaffID, false)
				if lookupErr != nil {
					page.Status.State, page.Status.ErrorCode = customerport.SectionDegraded, "local_owner_staff_unavailable"
					page.Items = append(page.Items, customerport.OwnerItem{DisplayName: "本地负责人待同步", Status: "local_owner", Source: local.Source, ObservedAt: local.UpdatedAt})
				} else {
					page.Items = append(page.Items, customerport.OwnerItem{DisplayName: user.DisplayName, Status: "local_owner", Source: local.Source, ObservedAt: local.UpdatedAt})
				}
				updated := local.UpdatedAt
				page.Status.AsOf = &updated
			}
		}
		observations, err := adapter.observations.CustomerOwnerObservations(tx, id)
		if err != nil {
			return err
		}
		for _, observation := range observations {
			if page.Status.AsOf == nil || observation.ObservedAt.After(*page.Status.AsOf) {
				value := observation.ObservedAt
				page.Status.AsOf = &value
			}
			user, lookupErr := adapter.users.UserByWeComUserID(tx, observation.EmployeeID, false)
			if errors.Is(lookupErr, accessdomain.ErrNotFound) {
				page.UnmatchedCount++
				continue
			}
			if lookupErr != nil {
				return lookupErr
			}
			page.Items = append(page.Items, customerport.OwnerItem{DisplayName: user.DisplayName, Status: observation.Status, Source: "wecom_follow", ObservedAt: observation.ObservedAt})
		}
		if page.UnmatchedCount > 0 {
			page.Status.State, page.Status.ErrorCode = customerport.SectionDegraded, "wecom_staff_name_unmatched"
		}
		return nil
	})
	if err != nil {
		return customerport.OwnerPage{}, customerport.ErrSectionUnavailable
	}
	return page, nil
}

type customerTagAdapter struct {
	uow          platformport.UnitOfWork
	observations wecomport.CustomerProfileObservationReader
	names        tagport.ProviderTagNameReader
}

func (customerTagAdapter) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}

func (adapter customerTagAdapter) CustomerTags(ctx context.Context, id customerdomain.CustomerID) (customerport.TagPage, error) {
	page := customerport.TagPage{Items: []customerport.TagItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		observations, err := adapter.observations.CustomerTagObservations(tx, id)
		if err != nil {
			return err
		}
		providerIDs := make([]string, 0, len(observations))
		for _, item := range observations {
			if item.ProviderType == 1 {
				providerIDs = append(providerIDs, item.ProviderTagID)
			}
		}
		resolved, err := adapter.names.ProviderTagNames(tx, providerIDs)
		if err != nil {
			return err
		}
		byID := map[string]tagport.ProviderTagName{}
		for _, item := range resolved {
			byID[item.ProviderTagID] = item
		}
		seen := map[string]struct{}{}
		for _, observation := range observations {
			name, group := "", ""
			if mapped, exists := byID[observation.ProviderTagID]; exists {
				name, group = mapped.Name, mapped.GroupName
			} else if observation.ProviderType == 2 && observation.ObservedName != "" {
				name = observation.ObservedName
			}
			if name == "" {
				name = "标签名称待同步"
				page.Status.State, page.Status.ErrorCode = customerport.SectionDegraded, "tag_catalog_name_missing"
			}
			key := name + "\x00" + group + "\x00" + observation.Status
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			page.Items = append(page.Items, customerport.TagItem{Name: name, GroupName: group, Status: observation.Status, ObservedAt: observation.ObservedAt})
			if page.Status.AsOf == nil || observation.ObservedAt.After(*page.Status.AsOf) {
				value := observation.ObservedAt
				page.Status.AsOf = &value
			}
		}
		sort.SliceStable(page.Items, func(i, j int) bool {
			if page.Items[i].Status != page.Items[j].Status {
				return page.Items[i].Status == "active"
			}
			return page.Items[i].Name < page.Items[j].Name
		})
		return nil
	})
	if err != nil {
		return customerport.TagPage{}, customerport.ErrSectionUnavailable
	}
	return page, nil
}

type customerSurveyAdapter struct {
	reader interface {
		surveyport.CustomerHistoryReader
		CustomerHistory(context.Context, int64, int32, int32) (surveyport.SubmissionPage, error)
	}
}

func (customerSurveyAdapter) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}

func (adapter customerSurveyAdapter) CustomerSurveys(ctx context.Context, id customerdomain.CustomerID, query customerport.PageQuery) (customerport.SurveyPage, error) {
	if query.Limit < 1 || query.Limit > 101 {
		return customerport.SurveyPage{}, customerport.ErrSectionUnavailable
	}
	// CustomerHistoryWindow permits at most 101 rows so profile callers can
	// detect a following page. Read the Owner's actual total separately with a
	// bounded one-row summary; never expose the page length as a total.
	summary, err := adapter.reader.CustomerHistory(ctx, int64(id), 1, 0)
	if err != nil {
		return customerport.SurveyPage{}, customerport.ErrSectionUnavailable
	}
	window, err := adapter.reader.CustomerHistoryWindow(ctx, surveyport.CustomerHistoryQuery{CustomerID: int64(id), Limit: int32(query.Limit), Watermark: query.Watermark, AfterAt: query.AfterAt, AfterID: surveyport.ID(query.AfterID)})
	if err != nil {
		return customerport.SurveyPage{}, customerport.ErrSectionUnavailable
	}
	page := customerport.SurveyPage{Items: make([]customerport.SurveyItem, 0, len(window.Items)), Total: summary.Total, Status: customerport.SectionStatus{State: customerport.SectionReady}}
	for _, submission := range window.Items {
		item := customerport.SurveyItem{ID: int64(submission.ID), Title: submission.QuestionnaireTitle, SubmittedAt: submission.SubmittedAt,
			Score: submission.TotalScore, Answers: []customerport.SurveyAnswer{}}
		if submission.Result.OverallLevel != nil {
			item.AssessmentLabel = submission.Result.OverallLevel.Title
		}
		for _, answer := range submission.Answers {
			values := make([]string, 0, len(answer.SelectedOptions)+1)
			for _, option := range answer.SelectedOptions {
				values = append(values, option.OptionText)
			}
			if len(values) == 0 && answer.TextValueMasked != "" {
				values = append(values, answer.TextValueMasked)
			}
			item.Answers = append(item.Answers, customerport.SurveyAnswer{Question: answer.QuestionTitle, Answers: values})
		}
		page.Items = append(page.Items, item)
	}
	asOf := query.Watermark.UTC()
	page.Status.AsOf = &asOf
	return page, nil
}

type customerTimelineAdapter struct {
	uow    platformport.UnitOfWork
	reader interface {
		CustomerTimeline(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.TimelinePage, error)
	}
}

func (customerTimelineAdapter) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}

func (adapter customerTimelineAdapter) CustomerTimeline(ctx context.Context, id customerdomain.CustomerID, query customerport.PageQuery) (customerport.TimelinePage, error) {
	var page customerport.TimelinePage
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = adapter.reader.CustomerTimeline(tx, id, query)
		return readErr
	})
	if err != nil {
		return customerport.TimelinePage{}, customerport.ErrSectionUnavailable
	}
	return page, nil
}

type disabledCustomerChatActivity struct{}

func (disabledCustomerChatActivity) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionNotReady, ErrorCode: "chat_activity_not_connected"}
}
func (disabledCustomerChatActivity) CustomerChatActivity(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.ChatActivityPage, error) {
	return customerport.ChatActivityPage{}, customerport.ErrCapabilityNotReady
}
