package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type sourceStub struct {
	ids []customerdomain.CustomerID
	err error
}

func (s sourceStub) Evaluate(_ context.Context, _ segmentport.Definition, at time.Time) (segmentport.Evaluation, error) {
	return segmentport.Evaluation{CustomerIDs: s.ids, ReferenceAt: at}, s.err
}

type canonicalStub struct {
	ids []customerdomain.CustomerID
	err error
}

func (s canonicalStub) CanonicalCustomers(context.Context, []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return s.ids, s.err
}

func TestEvaluatorReturnsSortedDeduplicatedCanonicalOneIDs(t *testing.T) {
	evaluator, err := NewEvaluator(segmentcompiler.Compiler{}, sourceStub{ids: []customerdomain.CustomerID{9, 4, 4}}, canonicalStub{ids: []customerdomain.CustomerID{7, 3, 7}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`), time.Now().UTC())
	if err != nil || len(result.CustomerIDs) != 2 || result.CustomerIDs[0] != 3 || result.CustomerIDs[1] != 7 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func TestEvaluatorFailsClosedOnResolverConflict(t *testing.T) {
	evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, sourceStub{}, canonicalStub{err: errors.New("identity conflict")})
	_, err := evaluator.Evaluate(context.Background(), json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`), time.Now().UTC())
	if !errors.Is(err, ErrEvaluationUnavailable) {
		t.Fatalf("err=%v", err)
	}
}
