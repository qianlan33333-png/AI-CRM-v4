package store

import (
	"context"
	"encoding/json"
	"fmt"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

func (r *Repository) ContactHistoryWithin(ctx context.Context, after int64, allow []surveyport.LegacyContactMapping) ([]customerport.ContactHistory, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	if after == 0 {
		for _, m := range allow {
			var valid bool
			err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_definition_questions q JOIN survey_definition_versions v ON v.id=q.definition_version_id WHERE q.id=$1 AND q.definition_version_id=$2 AND v.is_immutable AND (($3='contact_phone' AND q.question_type='mobile') OR ($3='shipping_contact' AND q.question_type='shipping_address')) AND (q.sidebar_profile_field='' OR q.sidebar_profile_field=$3) AND NOT EXISTS(SELECT 1 FROM survey_definition_questions other WHERE other.definition_version_id=q.definition_version_id AND other.id<>q.id AND other.sidebar_profile_field IN ('contact_phone','shipping_contact')))`, m.QuestionID, m.DefinitionVersionID, m.Target).Scan(&valid)
			if err != nil {
				return nil, mapError(err)
			}
			if !valid {
				return nil, surveyport.ErrInvalid
			}
		}
	}
	rows, err := t.Query(ctx, `SELECT a.id,a.submission_id,s.customer_id,s.submitted_at,a.question_type,a.text_value_ciphertext,s.definition_version_id,COALESCE(a.definition_question_id,0),COALESCE(q.sidebar_profile_field,'') FROM survey_submission_answers a JOIN survey_submissions s ON s.id=a.submission_id LEFT JOIN survey_definition_questions q ON q.id=a.definition_question_id AND q.definition_version_id=s.definition_version_id WHERE a.id>$1 AND a.question_type IN ('mobile','shipping_address') ORDER BY a.id LIMIT 100`, after)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []customerport.ContactHistory{}
	for rows.Next() {
		var fact customerport.ContactHistory
		var customer *int64
		var submission, version, question int64
		var kind, target string
		var encrypted []byte
		if err = rows.Scan(&fact.Cursor, &submission, &customer, &fact.Update.SourceAt, &kind, &encrypted, &version, &question, &target); err != nil {
			return nil, mapError(err)
		}
		for _, m := range allow {
			if int64(m.DefinitionVersionID) == version && int64(m.QuestionID) == question && target == "" {
				target = m.Target
			}
		}
		if target != "contact_phone" && target != "shipping_contact" {
			fact.SkipReason = "unmapped"
		} else if customer == nil || *customer < 1 {
			fact.SkipReason = "missing_customer"
		} else if (target == "contact_phone" && kind != "mobile") || (target == "shipping_contact" && kind != "shipping_address") {
			fact.SkipReason = "mapping_type_mismatch"
		} else {
			fact.Update.CustomerID = customerdomain.CustomerID(*customer)
			fact.Update.Source = fmt.Sprintf("survey:%d:answer:%d", submission, fact.Cursor)
			fact.Update.OnlyIfEmpty = true
			raw, e := r.cipher.Decrypt(encrypted)
			if e != nil {
				fact.SkipReason = "unreadable"
			} else if kind == "mobile" {
				p, ok := customerport.NormalizeContactPhone(raw)
				if !ok {
					fact.SkipReason = "invalid_phone"
				} else {
					fact.Update.MobileE164 = p
				}
			} else {
				var v customerport.ShippingContact
				if json.Unmarshal([]byte(raw), &v) != nil || !customerport.ValidShippingContact(v) {
					fact.SkipReason = "invalid_shipping"
				} else {
					fact.Update.Shipping = &v
					fact.Update.MobileE164, _ = customerport.NormalizeContactPhone(v.MobileE164)
				}
			}
		}
		out = append(out, fact)
	}
	return out, mapError(rows.Err())
}
