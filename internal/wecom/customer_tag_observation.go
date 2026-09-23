package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var ErrCustomerTagObservationUnavailable = errors.New("wecom customer tag observation unavailable")

// CustomerTagObservationStore is owned by WeCom. Its write is a readback
// observation only: it has no dependency on Customer command tables or EER.
type CustomerTagObservationStore interface {
	RecordCustomerTagRefresh(context.Context, string, customerdomain.CustomerID, string, []wecomport.ExternalContactTag, time.Time, string) error
}

// CustomerTagObservationService performs one existing WeCom directory read
// after an already-accepted mark_tag call. Network work is outside the UoW;
// only the resulting Provider observation is persisted in a fresh WeCom UoW.
type CustomerTagObservationService struct {
	Enabled  bool
	CorpID   string
	Provider wecomport.ExternalContactReader
	Store    CustomerTagObservationStore
	UOW      platformport.UnitOfWork
	Now      func() time.Time
}

func (service CustomerTagObservationService) RefreshCustomerTagObservation(ctx context.Context, effectRef string, customerID customerdomain.CustomerID, employeeID, externalUserID string) error {
	if !service.Enabled || service.CorpID == "" || service.Provider == nil || service.Store == nil || service.UOW == nil || effectRef == "" || customerID < 1 || employeeID == "" || externalUserID == "" {
		return ErrCustomerTagObservationUnavailable
	}
	// Capture the observation time before the network read. Two independent
	// readbacks can return out of order; using the completion time would let an
	// older Provider response that was delayed in transit overwrite a newer
	// observed contact state.
	observedAt := time.Now().UTC()
	if service.Now != nil {
		observedAt = service.Now().UTC()
	}
	contact, err := service.Provider.ReadExternalContact(ctx, externalUserID)
	if err != nil {
		return err
	}
	if contact.ExternalUserID != externalUserID {
		return ErrCustomerTagObservationUnavailable
	}
	var tags []wecomport.ExternalContactTag
	found := false
	for _, follow := range contact.FollowInfo {
		if follow.EmployeeID == employeeID {
			tags = append([]wecomport.ExternalContactTag(nil), follow.Tags...)
			found = true
			break
		}
	}
	if !found {
		return ErrCustomerTagObservationUnavailable
	}
	// The immutable effect reference supplies the run identity; the persisted key
	// never retains the Provider contact or employee identifier.
	sum := sha256.Sum256([]byte("wecom.tag.refresh.v1\x00" + effectRef))
	key := "tag-refresh:" + hex.EncodeToString(sum[:])
	return service.UOW.Within(ctx, func(tx context.Context) error {
		return service.Store.RecordCustomerTagRefresh(tx, "wecom-corp:"+service.CorpID, customerID, employeeID, tags, observedAt, key)
	})
}

var _ wecomport.CustomerTagObservationRefresher = CustomerTagObservationService{}
