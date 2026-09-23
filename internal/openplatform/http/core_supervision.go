package http

import (
	"bytes"
	"encoding/json"
	"errors"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	"net/http"
	"strconv"
	"strings"
)

func (handler *Handler) coreSupervisedPush(w http.ResponseWriter, r *http.Request) {
	handler.invokeV1(w, r, openplatformport.OperationCorePushRecord, requestJSONInput)
}
func (handler *Handler) coreOperationsRead(w http.ResponseWriter, r *http.Request) {
	op := openplatformport.OperationCoreProducts
	if r.PathValue("package_id") != "" {
		op = openplatformport.OperationCoreMembers
	}
	if strings.HasSuffix(r.URL.Path, "/operations") {
		op = openplatformport.OperationCoreMemberOperations
	}
	if strings.HasSuffix(r.URL.Path, "/history") {
		op = openplatformport.OperationCoreMemberHistory
	}
	handler.invokeV1(w, r, op, coreAudienceJSONInput)
}
func coreAudienceJSONInput(r *http.Request) (json.RawMessage, error) {
	body, err := readBody(r)
	if err != nil || len(bytes.TrimSpace(body)) != 0 {
		return nil, errors.New("unexpected body")
	}
	values := map[string]any{}
	for _, key := range []string{"package_id", "customer_id"} {
		if raw := r.PathValue(key); raw != "" {
			v, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || v < 1 {
				return nil, errors.New("invalid identifier")
			}
			values[key] = v
		}
	}
	for key, entries := range r.URL.Query() {
		if len(entries) != 1 {
			return nil, errors.New("repeated query")
		}
		switch key {
		case "limit":
			v, e := strconv.Atoi(entries[0])
			if e != nil || v < 1 || v > 100 {
				return nil, errors.New("invalid limit")
			}
			values[key] = v
		case "cursor":
			if len(entries[0]) > 4096 {
				return nil, errors.New("invalid cursor")
			}
			values[key] = entries[0]
		default:
			return nil, errors.New("unknown query")
		}
	}
	return json.Marshal(values)
}
