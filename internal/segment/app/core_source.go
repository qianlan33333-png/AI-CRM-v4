package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type CoreAssignmentReader interface {
	CoreCustomerIDs(context.Context, int64) ([]customerdomain.CustomerID, error)
}

// CoreSource extends the existing closed evaluator; it never executes model output as code.
type CoreSource struct {
	UOW      platformport.UnitOfWork
	Reader   CoreAssignmentReader
	Fallback segmentport.DefinitionSource
}

func (s CoreSource) Evaluate(ctx context.Context, def segmentport.Definition, at time.Time) (segmentport.Evaluation, error) {
	var ast segmentdsl.AST
	if json.Unmarshal(def.Expression, &ast) != nil {
		return segmentport.Evaluation{}, ErrInvalid
	}
	if ast.Template != segmentdsl.CoreAIProduct {
		return s.Fallback.Evaluate(ctx, def, at)
	}
	var id int64
	if len(ast.Parameters) != 1 || json.Unmarshal(ast.Parameters["core_product_id"], &id) != nil || id < 1 || id > 5 {
		return segmentport.Evaluation{}, ErrInvalid
	}
	var ids []customerdomain.CustomerID
	e := s.UOW.Within(ctx, func(tx context.Context) error { var e error; ids, e = s.Reader.CoreCustomerIDs(tx, id); return e })
	return segmentport.Evaluation{CustomerIDs: ids, ReferenceAt: at, Watermarks: []segmentport.SourceWatermark{{Source: "segment.core_assignments.v1", AsOf: at, Fresh: true, SafeDigest: sha256.Sum256([]byte(at.Format(time.RFC3339Nano)))}}}, e
}
