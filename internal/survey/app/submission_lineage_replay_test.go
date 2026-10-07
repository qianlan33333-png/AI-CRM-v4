package app

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

type replaySubject struct {
	subject identityport.VerifiedIdentitySubject
}

func (s *replaySubject) VerifiedIdentitySubject(context.Context, int64) (identityport.VerifiedIdentitySubject, bool, error) {
	return s.subject, true, nil
}

func (s *replaySubject) WriteLockedVerifiedIdentitySubject(context.Context, int64) (identityport.VerifiedIdentitySubject, bool, error) {
	return s.subject, true, nil
}

func TestSubmissionReplayAfterSplitCannotReturnOtherRootReceipt(t *testing.T) {
	root := customerdomain.CustomerID(7)
	formerAlias := customerdomain.CustomerID(8)
	q := surveyport.Questionnaire{ID: 4, Slug: "growth", Status: surveyport.StatusPublished, DefinitionVersion: 1, Mode: surveyport.ModeSurvey, Questions: []surveyport.Question{{ID: 1, Type: surveyport.QuestionTextarea, Title: "需求", Required: true}}}
	store := &completionStore{questionnaire: q}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	service := NewSubmissionService(oauthUOW{}, store, cipher)
	service.collectedPhones = completionPhoneAttacher{}
	subject := &replaySubject{identityport.VerifiedIdentitySubject{CustomerID: root, Lineage: []customerdomain.CustomerID{root, formerAlias}}}
	if err = service.BindIdentitySubjects(subject); err != nil {
		t.Fatal(err)
	}
	command := surveyport.SubmitCommand{Slug: q.Slug, DefinitionVersion: 1, SubmissionKey: strings.Repeat("a", 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &formerAlias, ProviderIdentityID: 42, EvidenceDigest: strings.Repeat("b", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: 1, TextValue: "需要帮助"}}}
	first, err := service.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := service.Submit(context.Background(), command)
	if err != nil || unchanged != first {
		t.Fatalf("same subject replay=%+v first=%+v err=%v", unchanged, first, err)
	}
	// A historical submission owned by an alias is still available to its
	// current merged root; the ownership check must preserve this contract.
	store.stored.Identity = surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &formerAlias}
	merged, err := service.Submit(context.Background(), command)
	if err != nil || merged != first {
		t.Fatalf("merged lineage replay=%+v first=%+v err=%v", merged, first, err)
	}
	// Production persistence retains the canonical customer present at write.
	// A later split returns this exact provider subject to the former alias.
	store.stored.Identity = surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &root}
	subject.subject = identityport.VerifiedIdentitySubject{CustomerID: formerAlias, Lineage: []customerdomain.CustomerID{formerAlias}}
	replayed, err := service.Submit(context.Background(), command)
	if !errors.Is(err, surveyport.ErrConflict) || replayed.SubmissionID != 0 || replayed.ResultToken != "" {
		t.Fatalf("split replay returned former root's receipt: first=%+v replay=%+v err=%v", first, replayed, err)
	}
	if store.auditOutbox != 1 {
		t.Fatalf("split replay wrote audit/outbox: count=%d", store.auditOutbox)
	}
}
