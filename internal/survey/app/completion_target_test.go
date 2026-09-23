package app

import (
	"encoding/json"
	"testing"
)

func TestParseSurveyCompletionTarget(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "same origin path", raw: `{"enabled":true,"target_type":"h5","h5_url":"/finish"}`, ok: true},
		{name: "public https", raw: `{"enabled":true,"target_type":"h5","h5_url":"https://example.com/finish"}`, ok: true},
		{name: "private h5", raw: `{"enabled":true,"target_type":"h5","h5_url":"https://127.0.0.1/finish"}`},
		{name: "decimal loopback h5", raw: `{"enabled":true,"target_type":"h5","h5_url":"https://2130706433/finish"}`},
		{name: "short loopback h5", raw: `{"enabled":true,"target_type":"h5","h5_url":"https://127.1/finish"}`},
		{name: "octal private h5", raw: `{"enabled":true,"target_type":"h5","h5_url":"https://0300.0250.0001.0001/finish"}`},
		{name: "hex loopback h5", raw: `{"enabled":true,"target_type":"h5","h5_url":"https://0x7f000001/finish"}`},
		{name: "local domain h5", raw: `{"enabled":true,"target_type":"h5","h5_url":"https://gateway.local/finish"}`},
		{name: "unsafe scheme", raw: `{"enabled":true,"target_type":"h5","h5_url":"javascript:alert(1)"}`},
		{name: "local URL link", raw: `{"enabled":true,"target_type":"url_link","source_url":"https://localhost/link","response_url_key":"url_link"}`},
		{name: "legacy IPv4 URL link", raw: `{"enabled":true,"target_type":"url_link","source_url":"https://0177.0.0.1/link","response_url_key":"url_link"}`},
		{name: "invalid response key", raw: `{"enabled":true,"target_type":"url_link","source_url":"https://example.com/link","response_url_key":"data[0]"}`},
		{name: "nested response key", raw: `{"enabled":true,"target_type":"url_link","source_url":"https://example.com/link","response_url_key":"data.url_link"}`, ok: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseSurveyCompletionTarget(json.RawMessage(test.raw))
			if test.ok && err != nil {
				t.Fatalf("parse target: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
