package main

import (
	"net/http"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	overviewapp "github.com/qianlan33333-png/AI-CRM-v3/internal/overview/app"
	overviewhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/overview/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// newAdminOverviewHandler is the Composition-only adapter for the operating
// overview. Owner read ports are supplied by their domain applications; the
// HTTP shell never receives a database handle or a domain store.
func newAdminOverviewHandler(customers customerport.OverviewReader, payments paymentport.OverviewReader, distribution distributionport.OverviewReader, security overviewhttp.RequestSecurity) (http.Handler, error) {
	service, err := overviewapp.NewService(customers, payments, distribution)
	if err != nil {
		return nil, err
	}
	handler, err := overviewhttp.NewHandler(overviewhttp.Config{Reader: service, Security: security})
	if err != nil {
		return nil, err
	}
	return handler, nil
}
