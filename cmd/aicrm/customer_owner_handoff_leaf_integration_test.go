package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
)

type ownerLeafReader struct {
	value customerport.OwnerHandoffExecution
}

func (r ownerLeafReader) ReadOwnerHandoffExecution(context.Context, string) (customerport.OwnerHandoffExecution, error) {
	return r.value, nil
}

type ownerLeafSink struct {
	completions []customerport.OwnerHandoffCompletion
}

func (s *ownerLeafSink) CompleteOwnerHandoffEffect(_ context.Context, v customerport.OwnerHandoffCompletion) error {
	s.completions = append(s.completions, v)
	return nil
}

// TestCustomerOwnerHandoffLeafProviderAndSinkPreservePartial101Fixture keeps
// the real WeCom leaf in Composition: two frozen sub-batches issue two calls;
// a missing row remains unknown while known success/refusal reach the sink.
func TestCustomerOwnerHandoffLeafProviderAndSinkPreservePartial101Fixture(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/transfer_customer":
			calls++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			ids, ok := body["external_userid"].([]any)
			if !ok || (len(ids) != 100 && len(ids) != 1) {
				t.Fatalf("ids=%v", body)
			}
			rows := make([]map[string]any, 0, len(ids))
			for i, raw := range ids {
				id, ok := raw.(string)
				if !ok {
					t.Fatal("nonstring id")
				}
				if len(ids) == 100 && i == 99 {
					continue
				}
				code := 0
				if len(ids) == 100 && i == 98 {
					code = 40003
				}
				rows = append(rows, map[string]any{"external_userid": id, "errcode": code})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "customer": rows})
		default:
			t.Fatalf("endpoint=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "corp", ContactSecret: "secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindCustomerOwnerHandoff, SourceRefDigest: effectport.Hash("source"), TargetRefDigest: effectport.Hash("target"), PayloadDigest: effectport.Hash("payload"), PolicyVersionHash: effectport.Hash("policy")}
	makeExecution := func(effectID string, first, count int) customerport.OwnerHandoffExecution {
		lines := make([]customerport.OwnerHandoffExecutionLine, 0, count)
		for i := first; i < first+count; i++ {
			lines = append(lines, customerport.OwnerHandoffExecutionLine{Line: int64(i + 1), CustomerID: 1, ExternalUserID: fmt.Sprintf("external-%03d", i)})
		}
		return customerport.OwnerHandoffExecution{EffectID: effectID, SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadRefDigest: string(envelope.PayloadDigest), PolicyRefDigest: string(envelope.PolicyVersionHash), SourceUserID: "source", TargetUserID: "target", Lines: lines}
	}
	sinkWriter := &ownerLeafSink{}
	sink, err := outbound.NewCustomerOwnerHandoffCompletionSink(sinkWriter)
	if err != nil {
		t.Fatal(err)
	}
	for _, execution := range []customerport.OwnerHandoffExecution{makeExecution("eer_100", 0, 100), makeExecution("eer_101", 100, 1)} {
		provider, err := outbound.NewCustomerOwnerHandoffProvider(ownerLeafReader{execution}, client)
		if err != nil {
			t.Fatal(err)
		}
		attempt := effectport.Attempt{EffectID: execution.EffectID, Number: 1, Generation: 1, Fence: 1}
		result, err := provider.Execute(context.Background(), envelope, attempt)
		if err != nil || !result.Artifact.Valid() {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if execution.EffectID == "eer_100" && result.Completion != effectport.StateUnknown {
			t.Fatalf("partial=%s", result.Completion)
		}
		if execution.EffectID == "eer_101" && result.Completion != effectport.StateExecuted {
			t.Fatalf("one=%s", result.Completion)
		}
		if err = sink.CompleteEffect(context.Background(), execution.EffectID, envelope, attempt, result); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || len(sinkWriter.completions) != 2 || sinkWriter.completions[0].State != string(effectport.StateUnknown) || len(sinkWriter.completions[0].Lines) != 100 || sinkWriter.completions[0].Lines[97].State != "provider_accepted" || sinkWriter.completions[0].Lines[98].State != "final_failed" || sinkWriter.completions[0].Lines[99].State != "outcome_unknown" || sinkWriter.completions[1].State != string(effectport.StateExecuted) || len(sinkWriter.completions[1].Lines) != 1 || sinkWriter.completions[1].Lines[0].State != "provider_accepted" {
		t.Fatalf("calls=%d completions=%+v", calls, sinkWriter.completions)
	}
}
