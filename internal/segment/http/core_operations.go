package http

import (
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"net/http"
	"strings"
)

func (h *Handler) BindCoreOperations(core *segmentapp.CoreOperations) { h.core = core }
func (h *Handler) coreOperations(w http.ResponseWriter, r *http.Request, tail string) {
	if h.core == nil {
		resultError(w, segmentapp.ErrNotReady)
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		var value any
		var e error
		switch tail {
		case "core/product-options":
			if h.productOptions == nil {
				resultError(w, segmentapp.ErrNotReady)
				return
			}
			limit, offset := queryInt(r, "limit", 50), queryInt(r, "offset", 0)
			if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
				fail(w, 400, "invalid_request")
				return
			}
			value, e = h.productOptions.ListProductOptions(r.Context(), productport.ProductOptionQuery{Q: r.URL.Query().Get("q"), ProductType: productport.ProductOptionAll, Limit: int32(limit), Offset: int32(offset)})
		case "core/products":
			value, e = h.core.Products(r.Context())
		case "core/prompt":
			value, e = h.core.Prompt(r.Context())
		case "core/prompt/history":
			value, e = h.core.PromptHistory(r.Context())
		default:
			if strings.HasPrefix(tail, "core/recommendations/") {
				value, e = h.core.Recommendation(r.Context(), id(strings.TrimPrefix(tail, "core/recommendations/")))
				break
			}
			parts := strings.Split(tail, "/")
			if len(parts) == 5 && parts[0] == "packages" && parts[2] == "members" && (parts[4] == "operations" || parts[4] == "history") {
				if parts[4] == "history" {
					value, e = h.core.MemberHistory(r.Context(), id(parts[1]), id(parts[3]), r.URL.Query().Get("cursor"), queryInt(r, "limit", 50))
				} else {
					value, e = h.core.MemberDetail(r.Context(), id(parts[1]), id(parts[3]), r.URL.Query().Get("cursor"), queryInt(r, "limit", 50))
				}
			} else {
				fail(w, 404, "not_found")
				return
			}
		}
		if e != nil {
			resultError(w, e)
			return
		}
		respond(w, 200, map[string]any{"data": value})
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		method(w, "GET, POST, PUT")
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	var value any
	var e error
	switch tail {
	case "core/recommendations":
		var in segmentapp.CoreRecommendCommand
		if !decode(w, r, &in) {
			return
		}
		in.Actor = p.InternalID
		in.IdempotencyKey = key
		value, e = h.core.Recommend(r.Context(), in)
	case "core/products":
		var in segmentapp.CoreProductCommand
		if !decode(w, r, &in) {
			return
		}
		in.Actor = p.InternalID
		in.IdempotencyKey = key
		value, e = h.core.PutProduct(r.Context(), in)
	case "core/prompt":
		var in segmentapp.CorePromptCommand
		if !decode(w, r, &in) {
			return
		}
		in.Actor = p.InternalID
		in.IdempotencyKey = key
		value, e = h.core.SavePrompt(r.Context(), in)
	case "core/assignments":
		var in segmentapp.CoreAssignmentCommand
		if !decode(w, r, &in) {
			return
		}
		in.Actor = p.InternalID
		in.IdempotencyKey = key
		value, e = h.core.ChangeAssignment(r.Context(), in)
	case "core/pushes":
		var in segmentapp.CorePushCommand
		if !decode(w, r, &in) {
			return
		}
		in.Actor, _ = segmentport.AdminMutationActor(p.InternalID)
		in.IdempotencyKey = key
		in.Push.Source = in.Actor.Reference
		value, e = h.core.RecordPush(r.Context(), in)
	default:
		fail(w, 404, "not_found")
		return
	}
	if e != nil {
		resultError(w, e)
		return
	}
	respond(w, 200, map[string]any{"data": value})
}
