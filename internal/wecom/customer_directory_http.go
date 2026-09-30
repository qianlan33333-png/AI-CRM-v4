package wecom

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type DirectoryStaffNames interface {
	UserByWeComUserID(context.Context, string, bool) (accessdomain.User, error)
}

// These GETs only consume a published directory. They never call the Provider
// or schedule a refresh. Canonical customer resolution belongs to OneID.
func (h CustomerSyncHTTPHandler) directoryRead(w http.ResponseWriter, r *http.Request) {
	principal, err := h.Auth.Authenticate(r.Context(), r)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	if principal.Kind != accessdomain.KindAdmin {
		writeSyncError(w, accessdomain.ErrPermissionDenied)
		return
	}
	raw := r.PathValue("customer_id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != raw {
		writeSyncError(w, errors.New("invalid_customer_id"))
		return
	}
	reader, ok := h.Service.Store.(interface {
		wecomport.DirectoryCustomerProfileReader
		wecomport.CustomerTagHistoryReader
	})
	if !ok || h.UOW == nil || h.Canonical == nil {
		writeSyncError(w, ErrSyncNotReady)
		return
	}
	q := wecomport.TagHistoryQuery{CustomerID: customerdomain.CustomerID(id)}
	values := r.URL.Query()
	for key, vs := range values {
		if len(vs) != 1 {
			writeSyncError(w, errors.New("invalid_query"))
			return
		}
		switch key {
		case "employee_id":
			q.EmployeeID = vs[0]
		case "tag_id":
			q.ProviderTagID = vs[0]
		case "from_date":
			q.FromDate = vs[0]
		case "to_date":
			q.ToDate = vs[0]
		case "before_id":
			q.BeforeID, err = strconv.ParseInt(vs[0], 10, 64)
		case "limit":
			q.Limit, err = strconv.Atoi(vs[0])
			if q.Limit < 1 {
				err = errors.New("invalid_limit")
			}
		default:
			err = errors.New("invalid_query")
		}
		if err != nil {
			writeSyncError(w, errors.New("invalid_query"))
			return
		}
	}
	if !validTagHistoryQuery(q) {
		writeSyncError(w, errors.New("invalid_query"))
		return
	}
	var payload any
	err = h.UOW.Within(r.Context(), func(ctx context.Context) error {
		canonical, err := h.Canonical.ResolveCanonicalCustomer(ctx, q.CustomerID)
		if err != nil {
			return err
		}
		q.CustomerID = canonical.CustomerID
		switch {
		case strings.HasSuffix(r.URL.Path, "/tag-history/statistics"):
			items, err := reader.CustomerTagHistoryStatistics(ctx, q)
			payload = map[string]any{"items": items}
			return err
		case strings.HasSuffix(r.URL.Path, "/tag-history"):
			page, err := reader.CustomerTagHistory(ctx, q)
			if err != nil {
				return err
			}
			for i := range page.Items {
				page.Items[i].EmployeeName = h.directoryStaffName(ctx, page.Items[i].EmployeeID)
			}
			payload = page
			return nil
		default:
			if len(values) > 0 {
				return errors.New("invalid_query")
			}
			p, err := reader.DirectoryCustomerProfile(ctx, q.CustomerID)
			if err != nil {
				return err
			}
			for i := range p.FollowUsers {
				p.FollowUsers[i].EmployeeName = h.directoryStaffName(ctx, p.FollowUsers[i].EmployeeID)
			}
			channels := []customerport.TimelineItem{}
			channelStatus := "unavailable"
			if h.CustomerChannels != nil {
				page, readErr := h.CustomerChannels.CustomerChannelActivities(ctx, q.CustomerID, customerport.PageQuery{Limit: 50, Watermark: time.Now().UTC()})
				if readErr == nil {
					channels = page.Items
					channelStatus = "available"
				}
			}
			payload = struct {
				wecomport.DirectoryCustomerProfile
				ChannelHistory       []customerport.TimelineItem `json:"channel_history"`
				ChannelHistoryStatus string                      `json:"channel_history_status"`
			}{p, channels, channelStatus}
			return nil
		}
	})
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeSyncJSON(w, http.StatusOK, payload)
}
func (h CustomerSyncHTTPHandler) directoryStaffName(ctx context.Context, id string) string {
	if h.StaffNames == nil {
		return ""
	}
	user, err := h.StaffNames.UserByWeComUserID(ctx, id, false)
	if err != nil {
		return ""
	}
	return user.DisplayName
}
