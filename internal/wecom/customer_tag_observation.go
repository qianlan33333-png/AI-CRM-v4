package wecom

import (
	"context"
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

func (service CustomerTagObservationService) RefreshCustomerTagObservation(context.Context, string, customerdomain.CustomerID, string, string) error {
	return ErrCustomerTagObservationUnavailable
}

var _ wecomport.CustomerTagObservationRefresher = CustomerTagObservationService{}
