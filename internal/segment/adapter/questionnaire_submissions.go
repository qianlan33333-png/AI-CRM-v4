package adapter

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

func (s LegacyTemplateSource) submissions(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.Submissions == nil {
		return nil, ErrCustomerReadUnavailable
	}
	ids, err := listParam(p, "questionnaire_ids")
	if err != nil || len(ids) == 0 {
		return nil, ErrCustomerReadUnavailable
	}
	require, err := boolParam(p, "require_wecom_identity")
	if err != nil {
		return nil, err
	}
	recognized := map[int64]bool{}
	if require {
		if s.RecognizedContacts == nil || s.PrimaryOwnerCorpScope == "" {
			return nil, ErrCustomerReadUnavailable
		}
		contacts, e := s.RecognizedContacts.AudienceRecognizedContacts(ctx, s.PrimaryOwnerCorpScope, at)
		if e != nil {
			return nil, e
		}
		for _, id := range contacts {
			recognized[int64(id)] = true
		}
	}
	facts, err := s.Submissions.AudienceSubmissions(ctx, at)
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, f := range facts {
		if f.CustomerID <= 0 || f.SubmittedAt.IsZero() || f.SubmittedAt.After(at) || !contains(ids, strconv.FormatInt(int64(f.QuestionnaireID), 10)) || !owner(p, f.StaffID) {
			continue
		}
		if require && !recognized[int64(f.CustomerID)] {
			continue
		}
		out[int64(f.CustomerID)] = true
	}
	return idsFrom(out), nil
}
