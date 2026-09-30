package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

var ErrEvaluationUnavailable = errors.New("audience evaluation unavailable")

type DefinitionCompiler interface {
	Compile(json.RawMessage) (segmentport.Definition, error)
}

type Evaluator struct {
	compiler  DefinitionCompiler
	source    segmentport.DefinitionSource
	canonical segmentport.CanonicalCustomerResolver
}

func NewEvaluator(compiler DefinitionCompiler, source segmentport.DefinitionSource, canonical segmentport.CanonicalCustomerResolver) (*Evaluator, error) {
	if compiler == nil || source == nil || canonical == nil {
		return nil, ErrEvaluationUnavailable
	}
	return &Evaluator{compiler: compiler, source: source, canonical: canonical}, nil
}

func (e *Evaluator) Evaluate(ctx context.Context, raw json.RawMessage, reference time.Time) (segmentport.Evaluation, error) {
	if e == nil || reference.IsZero() {
		return segmentport.Evaluation{}, ErrEvaluationUnavailable
	}
	definition, err := e.compiler.Compile(raw)
	if err != nil {
		return segmentport.Evaluation{}, ErrUnsupportedDefinition
	}
	result, err := e.source.Evaluate(ctx, definition, reference.UTC())
	if err != nil || !result.ReferenceAt.Equal(reference.UTC()) || len(result.CustomerIDs) > segmentport.MaximumEvaluationMembers {
		return segmentport.Evaluation{}, ErrEvaluationUnavailable
	}
	canonical, err := e.canonical.CanonicalCustomers(ctx, result.CustomerIDs)
	if err != nil || len(canonical) != len(result.CustomerIDs) || len(canonical) > segmentport.MaximumEvaluationMembers {
		return segmentport.Evaluation{}, ErrEvaluationUnavailable
	}
	qualifiedPaidOrder := map[customerdomain.CustomerID]segmentport.PaidOrderFact(nil)
	if result.QualifiedPaidOrder != nil {
		if len(result.QualifiedPaidOrder) != len(result.CustomerIDs) {
			return segmentport.Evaluation{}, ErrEvaluationUnavailable
		}
		qualifiedPaidOrder = make(map[customerdomain.CustomerID]segmentport.PaidOrderFact, len(result.QualifiedPaidOrder))
		for index, id := range result.CustomerIDs {
			fact, found := result.QualifiedPaidOrder[id]
			if !found || fact.PaidOrderID < 1 || fact.PaidAt.IsZero() || fact.PaidAt.After(reference.UTC()) {
				return segmentport.Evaluation{}, ErrEvaluationUnavailable
			}
			fact.PaidAt = fact.PaidAt.UTC()
			root := canonical[index]
			if existing, present := qualifiedPaidOrder[root]; !present || fact.PaidAt.After(existing.PaidAt) || fact.PaidAt.Equal(existing.PaidAt) && fact.PaidOrderID > existing.PaidOrderID {
				qualifiedPaidOrder[root] = fact
			}
		}
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i] < canonical[j] })
	stable := make([]customerdomain.CustomerID, 0, len(canonical))
	for _, id := range canonical {
		if id < 1 {
			return segmentport.Evaluation{}, ErrEvaluationUnavailable
		}
		if len(stable) == 0 || stable[len(stable)-1] != id {
			stable = append(stable, id)
		}
	}
	result.CustomerIDs = stable
	result.QualifiedPaidOrder = qualifiedPaidOrder
	return result, nil
}
