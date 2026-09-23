package main

import (
	"context"
	"errors"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type profileTestUOW struct{}

func (profileTestUOW) Within(ctx context.Context, run func(context.Context) error) error {
	return run(ctx)
}

type profileObservations struct {
	owners []wecomport.OwnerObservation
	tags   []wecomport.TagObservation
}

func (value profileObservations) CustomerOwnerObservations(context.Context, customerdomain.CustomerID) ([]wecomport.OwnerObservation, error) {
	return value.owners, nil
}
func (value profileObservations) CustomerTagObservations(context.Context, customerdomain.CustomerID) ([]wecomport.TagObservation, error) {
	return value.tags, nil
}

type profileUsers map[string]string

func (users profileUsers) UserByID(context.Context, int64, bool) (accessdomain.User, error) {
	return accessdomain.User{}, accessdomain.ErrNotFound
}

func (users profileUsers) UserByWeComUserID(_ context.Context, id string, _ bool) (accessdomain.User, error) {
	name, ok := users[id]
	if !ok {
		return accessdomain.User{}, accessdomain.ErrNotFound
	}
	return accessdomain.User{DisplayName: name}, nil
}

type profileTagNames map[string]tagport.ProviderTagName

func (names profileTagNames) ProviderTagNames(_ context.Context, ids []string) ([]tagport.ProviderTagName, error) {
	items := []tagport.ProviderTagName{}
	for _, id := range ids {
		if item, ok := names[id]; ok {
			items = append(items, item)
		}
	}
	return items, nil
}

type profileSurveyReader struct {
	item          surveyport.Submission
	total         int64
	historyLimit  int32
	historyOffset int32
	windowQuery   surveyport.CustomerHistoryQuery
}

func (reader *profileSurveyReader) CustomerHistory(_ context.Context, _ int64, limit, offset int32) (surveyport.SubmissionPage, error) {
	reader.historyLimit = limit
	reader.historyOffset = offset
	return surveyport.SubmissionPage{Items: []surveyport.Submission{reader.item}, Total: reader.total, Limit: limit, Offset: offset}, nil
}

func (reader *profileSurveyReader) CustomerHistoryWindow(_ context.Context, query surveyport.CustomerHistoryQuery) (surveyport.CustomerHistoryWindow, error) {
	reader.windowQuery = query
	return surveyport.CustomerHistoryWindow{Items: []surveyport.Submission{reader.item}}, nil
}

func TestCustomerOwnerAndTagAdaptersNeverExposeProviderIDs(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	observations := profileObservations{
		owners: []wecomport.OwnerObservation{{EmployeeID: "raw-userid-mapped", Status: "active", ObservedAt: now}, {EmployeeID: "raw-userid-unmatched", Status: "active", ObservedAt: now}},
		tags:   []wecomport.TagObservation{{ProviderTagID: "raw-provider-tag", ProviderType: 1, Status: "active", ObservedAt: now}},
	}
	owners, err := (customerOwnerAdapter{uow: profileTestUOW{}, observations: observations, users: profileUsers{"raw-userid-mapped": "小王"}}).CustomerOwners(context.Background(), 42)
	if err != nil || len(owners.Items) != 1 || owners.Items[0].DisplayName != "小王" || owners.UnmatchedCount != 1 || owners.Status.State != customerport.SectionDegraded {
		t.Fatalf("owners=%+v err=%v", owners, err)
	}
	tags, err := (customerTagAdapter{uow: profileTestUOW{}, observations: observations, names: profileTagNames{}}).CustomerTags(context.Background(), 42)
	if err != nil || len(tags.Items) != 1 || tags.Items[0].Name != "标签名称待同步" || tags.Items[0].Name == "raw-provider-tag" || tags.Status.State != customerport.SectionDegraded {
		t.Fatalf("tags=%+v err=%v", tags, err)
	}
}

func TestCustomerSurveyAdapterUsesOnlySurveyMaskedAnswers(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reader := &profileSurveyReader{total: 137, item: surveyport.Submission{ID: 8, QuestionnaireTitle: "安全问卷", SubmittedAt: now,
		Answers: []surveyport.AnswerSnapshot{{QuestionTitle: "手机号", TextValue: "13812345678", TextValueMasked: "138****5678"},
			{QuestionTitle: "选择", SelectedOptions: []surveyport.SelectedOptionSnapshot{{OptionText: "选项 A"}}}}}}
	adapter := customerSurveyAdapter{reader: reader}
	page, err := adapter.CustomerSurveys(context.Background(), 42, customerport.PageQuery{Limit: 21, Watermark: now})
	if err != nil || len(page.Items) != 1 || page.Total != 137 || page.Items[0].Answers[0].Answers[0] != "138****5678" || page.Items[0].Answers[1].Answers[0] != "选项 A" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if reader.historyLimit != 1 || reader.historyOffset != 0 || reader.windowQuery.Limit != 21 {
		t.Fatalf("summary limit=%d offset=%d window=%+v", reader.historyLimit, reader.historyOffset, reader.windowQuery)
	}
	if page.Items[0].Answers[0].Answers[0] == "13812345678" {
		t.Fatal("raw survey text leaked")
	}
}

func TestCustomerSectionAdapterClassifiesSourceFailure(t *testing.T) {
	failing := failingProfileObservations{}
	_, err := (customerOwnerAdapter{uow: profileTestUOW{}, observations: failing, users: profileUsers{}}).CustomerOwners(context.Background(), 42)
	if !errors.Is(err, customerport.ErrSectionUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

type failingProfileObservations struct{}

func (failingProfileObservations) CustomerOwnerObservations(context.Context, customerdomain.CustomerID) ([]wecomport.OwnerObservation, error) {
	return nil, errors.New("database unavailable")
}
func (failingProfileObservations) CustomerTagObservations(context.Context, customerdomain.CustomerID) ([]wecomport.TagObservation, error) {
	return nil, errors.New("database unavailable")
}

type localOwnerProfileStore struct {
	owner customerport.LocalOwner
	found bool
}

func (store localOwnerProfileStore) LocalOwner(context.Context, customerdomain.CustomerID, bool) (customerport.LocalOwner, bool, error) {
	return store.owner, store.found, nil
}

type localOwnerProfileUsers struct {
	byID    map[int64]accessdomain.User
	byWeCom map[string]accessdomain.User
}

func (users localOwnerProfileUsers) UserByID(_ context.Context, id int64, _ bool) (accessdomain.User, error) {
	user, found := users.byID[id]
	if !found {
		return accessdomain.User{}, accessdomain.ErrNotFound
	}
	return user, nil
}

func (users localOwnerProfileUsers) UserByWeComUserID(_ context.Context, id string, _ bool) (accessdomain.User, error) {
	user, found := users.byWeCom[id]
	if !found {
		return accessdomain.User{}, accessdomain.ErrNotFound
	}
	return user, nil
}

func TestCustomerOwnerAdapterShowsExplicitLocalOwnerBeforeWeComFacts(t *testing.T) {
	now := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	page, err := (customerOwnerAdapter{
		uow:          profileTestUOW{},
		observations: profileObservations{owners: []wecomport.OwnerObservation{{EmployeeID: "former", Status: "active", ObservedAt: now.Add(-time.Hour)}}},
		users:        localOwnerProfileUsers{byID: map[int64]accessdomain.User{9: {ID: 9, DisplayName: "新负责人"}}, byWeCom: map[string]accessdomain.User{"former": {DisplayName: "旧跟进员工"}}},
		owners:       localOwnerProfileStore{owner: customerport.LocalOwner{CustomerID: 42, StaffID: 9, Version: 2, Source: "owner_handoff_wecom_then_crm", UpdatedAt: now}, found: true},
	}).CustomerOwners(context.Background(), 42)
	if err != nil || len(page.Items) != 2 || page.Items[0].DisplayName != "新负责人" || page.Items[0].Status != "local_owner" || page.Items[0].Source != "owner_handoff_wecom_then_crm" || page.Items[1].Source != "wecom_follow" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}
