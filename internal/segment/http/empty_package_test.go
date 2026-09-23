package http

import (
	"context"
	"encoding/json"
	"errors"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	"testing"
)

type modeApplication struct {
	fakeApplication
	configuration segmentdomain.ConfigurationVersion
	err           error
}

func (s modeApplication) CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error) {
	return s.configuration, s.err
}
func TestPackageMembershipModeUsesConfigurationNotCount(t *testing.T) {
	id := int64(9)
	for _, tc := range []struct {
		name, definition, mode string
		configID               *int64
	}{
		{"empty", "", "empty", nil},
		{"zero member rule", `{"template_key":"active_contacts"}`, "rule", &id},
		{"zero member core", `{"template_key":"core_ai_product","parameters":{"core_product_id":3}}`, "core_ai", &id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{service: modeApplication{configuration: segmentdomain.ConfigurationVersion{Definition: json.RawMessage(tc.definition)}}}
			got, err := h.packageReadDTO(context.Background(), segmentdomain.Package{ID: 1, CurrentConfigurationVersionID: tc.configID})
			if err != nil || got["membership_mode"] != tc.mode || got["member_count"] != 0 {
				t.Fatalf("projection=%v err=%v", got, err)
			}
			if tc.mode == "core_ai" && got["core_product_id"] != int64(3) {
				t.Fatal(got)
			}
		})
	}
	h := &Handler{service: modeApplication{err: errors.New("unavailable")}}
	if _, err := h.packageReadDTO(context.Background(), segmentdomain.Package{ID: 1, CurrentConfigurationVersionID: &id}); err == nil {
		t.Fatal("unavailable configuration reported as empty")
	}
}
