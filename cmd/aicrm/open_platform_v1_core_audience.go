package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"strconv"
	"strings"
	"time"
)

func coreAudienceError(err error) error {
	if err == nil {
		return nil
	}
	code := openplatformport.ErrorDependencyUnavailable
	switch err.Error() {
	case "invalid audience configuration request":
		code = openplatformport.ErrorValidation
	case "audience configuration conflict":
		code = openplatformport.ErrorConflict
	case "audience configuration not found":
		code = openplatformport.ErrorNotFound
	}
	return openplatformport.NewError(code, "audience operation unavailable")
}
func audiencePackageScope(p accessdomain.MachinePrincipal, id int64) bool {
	allowed, limited := p.OwnerScope["package_id"]
	if !limited {
		return true
	}
	for _, s := range allowed {
		if s == strconv.FormatInt(id, 10) {
			return true
		}
	}
	return false
}
func audienceCustomerPrincipal(p accessdomain.MachinePrincipal) accessdomain.MachinePrincipal {
	scope := accessdomain.OwnerScope{}
	for k, v := range p.OwnerScope {
		if k != "package_id" {
			scope[k] = v
		}
	}
	p.OwnerScope = scope
	return p
}
func (e *openPlatformExecutor) v1CoreAudience(ctx context.Context, in openplatformport.Invocation) (openplatformport.Result, error) {
	invalid := func() (openplatformport.Result, error) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid audience input")
	}
	denied := func() (openplatformport.Result, error) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "audience resource denied")
	}
	for key, values := range in.Principal.OwnerScope {
		switch key {
		case "package_id", "customer_id", "owner_userid":
		case "corp_id":
			if !(accessdomain.OwnerScope{"corp_id": values}).Allows(map[string]string{"corp_id": in.Principal.CorpID}) {
				return denied()
			}
		default:
			return denied()
		}
	}
	if in.Operation == openplatformport.OperationCorePushRecord {
		var input struct {
			PushID        string                        `json:"push_id"`
			CustomerID    int64                         `json:"customer_id"`
			PackageID     int64                         `json:"package_id"`
			Materials     []segmentport.CoreMaterialRef `json:"materials"`
			OccurredAt    time.Time                     `json:"occurred_at"`
			Status        string                        `json:"status"`
			StatusVersion int64                         `json:"status_version"`
		}
		if decodeV1JSON(in.Input, &input) != nil {
			return invalid()
		}
		push := segmentport.CorePush{PushID: input.PushID, CustomerID: input.CustomerID, PackageID: input.PackageID, Materials: input.Materials, OccurredAt: input.OccurredAt, Status: input.Status, StatusVersion: input.StatusVersion}
		if push.CustomerID < 1 || push.PackageID < 1 || strings.TrimSpace(push.PushID) == "" || push.StatusVersion < 1 || len(in.IdempotencyKey) > 128 || strings.TrimSpace(in.IdempotencyKey) != in.IdempotencyKey {
			return invalid()
		}
		if !audiencePackageScope(in.Principal, push.PackageID) {
			return denied()
		}
		if err := e.ensureCustomerScope(ctx, audienceCustomerPrincipal(in.Principal), customerdomain.CustomerID(push.CustomerID), nil); err != nil {
			return openplatformport.Result{}, v1CustomerScopeError(err)
		}
		// Preserve the published optional/short header contract without losing
		// Segment's durable receipt. Missing keys bind to one business version;
		// explicit short keys bind to that exact caller key. Never use randomness.
		key := in.IdempotencyKey
		if key == "" {
			key = fmt.Sprintf("audience-version-%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d", push.PushID, push.CustomerID, push.PackageID, push.StatusVersion))))
		} else if len(key) < 16 {
			key = fmt.Sprintf("audience-header-%x", sha256.Sum256([]byte(key)))
		}
		result, err := e.coreAudience.RecordSupervisedPush(ctx, in.Principal.ClientID, key, push)
		return openplatformport.Result{Data: result}, coreAudienceError(err)
	}
	var input struct {
		PackageID  int64  `json:"package_id"`
		CustomerID int64  `json:"customer_id"`
		Cursor     string `json:"cursor"`
		Limit      int    `json:"limit"`
	}
	if decodeV1JSON(in.Input, &input) != nil {
		return invalid()
	}
	if in.Operation == openplatformport.OperationCoreProducts {
		if input.PackageID != 0 || input.CustomerID != 0 || input.Cursor != "" || input.Limit != 0 {
			return invalid()
		}
		products, err := e.coreAudience.Products(ctx)
		if err != nil {
			return openplatformport.Result{}, coreAudienceError(err)
		}
		items := []segmentport.CoreProduct{}
		for _, p := range products {
			if audiencePackageScope(in.Principal, p.PackageID) {
				items = append(items, p)
			}
		}
		return openplatformport.Result{Data: map[string]any{"items": items}}, nil
	}
	if input.PackageID < 1 || input.CustomerID < 0 || input.Limit < 0 || input.Limit > 100 || len(input.Cursor) > 4096 {
		return invalid()
	}
	if input.Limit == 0 {
		input.Limit = 50
	}
	if !audiencePackageScope(in.Principal, input.PackageID) {
		return denied()
	}
	principal := audienceCustomerPrincipal(in.Principal)
	if in.Operation == openplatformport.OperationCoreMembers {
		if input.CustomerID != 0 {
			return invalid()
		}
		page, err := e.coreAudience.CoreMembers(ctx, input.PackageID, input.Cursor, input.Limit)
		if err != nil {
			return openplatformport.Result{}, coreAudienceError(err)
		}
		items := []segmentport.Member{}
		for _, item := range page.Items {
			if err = e.ensureCustomerScope(ctx, principal, item.CustomerID, nil); err != nil {
				if err == errOpenPlatformResourceOutOfScope {
					continue
				}
				return openplatformport.Result{}, v1CustomerScopeError(err)
			}
			items = append(items, item)
		}
		page.Items = items
		return openplatformport.Result{Data: page}, nil
	}
	if input.CustomerID < 1 {
		return invalid()
	}
	if err := e.ensureCustomerScope(ctx, principal, customerdomain.CustomerID(input.CustomerID), nil); err != nil {
		return openplatformport.Result{}, v1CustomerScopeError(err)
	}
	var result any
	var err error
	if in.Operation == openplatformport.OperationCoreMemberHistory {
		result, err = e.coreAudience.MemberHistory(ctx, input.PackageID, input.CustomerID, input.Cursor, input.Limit)
	} else {
		result, err = e.coreAudience.MemberDetail(ctx, input.PackageID, input.CustomerID, input.Cursor, input.Limit)
	}
	return openplatformport.Result{Data: result}, coreAudienceError(err)
}
