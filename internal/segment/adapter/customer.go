// Package adapter connects the Segment domain to stable Customer ports.
package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

var ErrCustomerReadUnavailable = errors.New("audience customer reader is unavailable")

type CustomerSource struct {
	UoW       platformport.UnitOfWork
	Customers customerport.AudienceReader
	Legacy    segmentport.DefinitionSource
}

func (a CustomerSource) Evaluate(ctx context.Context, definition segmentport.Definition, reference time.Time) (segmentport.Evaluation, error) {
	if a.UoW == nil || reference.IsZero() {
		return segmentport.Evaluation{}, ErrCustomerReadUnavailable
	}
	var ast segmentdsl.AST
	if json.Unmarshal(definition.Expression, &ast) != nil || ast.SchemaVersion != 1 {
		return segmentport.Evaluation{}, ErrCustomerReadUnavailable
	}
	if ast.Template != segmentdsl.ActiveContacts {
		if a.Legacy == nil {
			return segmentport.Evaluation{}, ErrCustomerReadUnavailable
		}
		// Owner audience ports deliberately require the caller's UoW. This keeps
		// one evaluation on a single PostgreSQL snapshot and never holds a
		// transaction across a Provider operation (there are none on this path).
		var result segmentport.Evaluation
		err := a.UoW.Within(ctx, func(tx context.Context) error {
			var evaluateErr error
			result, evaluateErr = a.Legacy.Evaluate(tx, definition, reference)
			return evaluateErr
		})
		return result, err
	}
	if a.Customers == nil || len(ast.Predicate.Values) != 1 {
		return segmentport.Evaluation{}, ErrCustomerReadUnavailable
	}
	days, err := strconv.Atoi(ast.Predicate.Values[0])
	if err != nil {
		return segmentport.Evaluation{}, ErrCustomerReadUnavailable
	}
	var result segmentport.Evaluation
	err = a.UoW.Within(ctx, func(tx context.Context) error {
		ids, asOf, readErr := a.Customers.ActiveWithin(tx, reference, days)
		if readErr != nil {
			return readErr
		}
		safe := sha256.Sum256([]byte(asOf.UTC().Format(time.RFC3339Nano)))
		result = segmentport.Evaluation{CustomerIDs: ids, ReferenceAt: reference.UTC(), Watermarks: []segmentport.SourceWatermark{{Source: "customer.directory.v1", AsOf: asOf.UTC(), Fresh: !asOf.IsZero(), SafeDigest: safe}}}
		return nil
	})
	return result, err
}

// CanonicalCustomers follows Customer-owned roots in one explicit UoW. It has
// no identity provisioning, binding, merge, or external-identifier access.
type CanonicalCustomers struct {
	UoW      platformport.UnitOfWork
	Resolver customerport.CanonicalCustomerResolver
}

type canonicalCustomerBatchResolver interface {
	ResolveCanonicalCustomers(context.Context, []customerdomain.CustomerID) ([]customerport.CanonicalCustomer, error)
}

func (a CanonicalCustomers) CanonicalCustomers(ctx context.Context, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	if a.UoW == nil || a.Resolver == nil {
		return nil, ErrCustomerReadUnavailable
	}
	result := make([]customerdomain.CustomerID, 0, len(ids))
	read := func(tx context.Context) error {
		if batch, ok := a.Resolver.(canonicalCustomerBatchResolver); ok {
			canonical, resolveErr := batch.ResolveCanonicalCustomers(tx, ids)
			if resolveErr != nil || len(canonical) != len(ids) {
				return ErrCustomerReadUnavailable
			}
			for index, item := range canonical {
				if item.RequestedCustomerID != ids[index] || item.CustomerID < 1 {
					return ErrCustomerReadUnavailable
				}
				result = append(result, item.CustomerID)
			}
			return nil
		}
		for _, id := range ids {
			canonical, resolveErr := a.Resolver.ResolveCanonicalCustomer(tx, id)
			if resolveErr != nil || canonical.CustomerID < 1 {
				return ErrCustomerReadUnavailable
			}
			result = append(result, canonical.CustomerID)
		}
		return nil
	}
	var err error
	if _, transactionErr := platformpostgres.RequireTransaction(ctx); transactionErr == nil {
		err = read(ctx)
	} else {
		err = a.UoW.Within(ctx, read)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

var _ segmentport.DefinitionSource = CustomerSource{}
var _ segmentport.CanonicalCustomerResolver = CanonicalCustomers{}
