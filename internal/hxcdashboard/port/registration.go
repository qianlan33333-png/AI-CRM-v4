package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

type RegistrationFact struct {
	State   string
	AsOf    time.Time
	Version int64
}
type RegistrationReader interface {
	RegistrationFacts(context.Context, []customerdomain.CustomerID, time.Time) (map[customerdomain.CustomerID]RegistrationFact, error)
}
