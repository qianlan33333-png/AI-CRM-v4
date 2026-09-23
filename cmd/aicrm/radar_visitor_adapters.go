package main

import (
	"context"
	"fmt"
	"strconv"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

// radarVisitorPresentationAdapter is the composition-owned bridge for the
// sensitive, admin-only Radar visitor view. Radar continues to own all session
// aggregation; this adapter only reads Customer and Identity presentation Ports
// after CSRF/RBAC has been enforced by the Radar HTTP handler.
type radarVisitorPresentationAdapter struct {
	numbers    identityport.CustomerPublicNumbers
	uow        platformport.UnitOfWork
	directory  customerport.RadarVisitorDirectoryReader
	identities identityport.AdminRadarVisitorIdentityReader
	corpScope  string
}

func (adapter radarVisitorPresentationAdapter) SearchRadarVisitors(ctx context.Context, search string, limit int) ([]customerdomain.CustomerID, error) {
	if adapter.uow == nil || adapter.directory == nil || adapter.identities == nil || limit < 1 || limit > radarport.MaximumVisitorCandidates+1 {
		return nil, radarport.ErrUnavailable
	}
	var result []customerdomain.CustomerID
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		directoryIDs, err := adapter.directory.SearchRadarVisitorCustomers(tx, search, limit)
		if err != nil {
			return err
		}
		if len(directoryIDs) > radarport.MaximumVisitorCandidates {
			return radarport.ErrVisitorSearchTooWide
		}
		if adapter.numbers != nil {
			if n, e := strconv.ParseInt(search, 10, 64); e == nil && n >= 1000000 && n <= 9999999 {
				id, found, e := adapter.numbers.CustomerForPublicNumber(tx, search)
				if e != nil {
					return e
				}
				if found {
					directoryIDs = append(directoryIDs, id)
				}
			}
		}
		var searchErr error
		result, searchErr = adapter.identities.SearchAdminRadarVisitorCustomers(tx, adapter.corpScope, search, directoryIDs, limit)
		return searchErr
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (adapter radarVisitorPresentationAdapter) PresentRadarVisitors(ctx context.Context, sessions []radarport.VisitorSession) ([]radarport.Visitor, error) {
	if adapter.uow == nil || adapter.directory == nil || adapter.identities == nil {
		return nil, radarport.ErrUnavailable
	}
	resolved := make([]customerdomain.CustomerID, 0, len(sessions))
	seen := make(map[customerdomain.CustomerID]struct{}, len(sessions))
	for _, session := range sessions {
		if session.Attribution != radarport.AttributionResolved {
			continue
		}
		if session.CustomerID < 1 {
			return nil, radarport.ErrUnavailable
		}
		if _, exists := seen[session.CustomerID]; exists {
			continue
		}
		seen[session.CustomerID] = struct{}{}
		resolved = append(resolved, session.CustomerID)
	}
	identities := map[customerdomain.CustomerID]identityport.AdminRadarVisitorIdentity{}
	displays := map[customerdomain.CustomerID]customerport.RadarVisitorDirectoryDisplay{}
	numbers := map[customerdomain.CustomerID]string{}
	if len(resolved) > 0 {
		err := adapter.uow.Within(ctx, func(tx context.Context) error {
			var err error
			identities, err = adapter.identities.AdminRadarVisitorIdentities(tx, adapter.corpScope, resolved)
			if err != nil {
				return err
			}
			roots := make([]customerdomain.CustomerID, 0, len(identities))
			rootSeen := make(map[customerdomain.CustomerID]struct{}, len(identities))
			for _, projection := range identities {
				if projection.CanonicalCustomerID < 1 {
					return radarport.ErrUnavailable
				}
				if _, exists := rootSeen[projection.CanonicalCustomerID]; exists {
					continue
				}
				rootSeen[projection.CanonicalCustomerID] = struct{}{}
				roots = append(roots, projection.CanonicalCustomerID)
			}
			displays, err = adapter.directory.RadarVisitorDisplays(tx, roots)
			if err != nil {
				return err
			}
			if adapter.numbers != nil {
				numbers, err = adapter.numbers.CustomerPublicNumbers(tx, roots)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	items := make([]radarport.Visitor, 0, len(sessions))
	for _, session := range sessions {
		item := radarport.Visitor{OpenedAt: session.OpenedAt, AttributionStatus: session.Attribution, ExternalContactStatus: radarport.VisitorExternalContactMissing}
		if session.Attribution != radarport.AttributionResolved {
			items = append(items, item)
			continue
		}
		identity, exists := identities[session.CustomerID]
		if !exists || identity.CanonicalCustomerID < 1 {
			return nil, fmt.Errorf("radar visitor identity projection unavailable: %w", radarport.ErrUnavailable)
		}
		item.ExternalContactStatus = radarVisitorExternalContactStatus(identity.ExternalContactStatus)
		if identity.ExternalContactStatus == identityport.AdminRadarVisitorExternalContactAvailable && identity.ExternalContactID != "" {
			item.ExternalContactID = pointer(identity.ExternalContactID)
		}
		display := displays[identity.CanonicalCustomerID]
		if display.DisplayName != "" {
			item.Nickname = pointer(display.DisplayName)
		}
		oneID := customerdomain.CanonicalOneIDLabel(identity.CanonicalCustomerID)
		if adapter.numbers != nil {
			oneID = numbers[identity.CanonicalCustomerID]
		}
		if oneID == "" {
			return nil, fmt.Errorf("radar visitor canonical OneID unavailable: %w", radarport.ErrUnavailable)
		}
		item.OneID = pointer(oneID)
		items = append(items, item)
	}
	return items, nil
}

func radarVisitorExternalContactStatus(value identityport.AdminRadarVisitorExternalContactStatus) radarport.VisitorExternalContactStatus {
	switch value {
	case identityport.AdminRadarVisitorExternalContactAvailable:
		return radarport.VisitorExternalContactAvailable
	case identityport.AdminRadarVisitorExternalContactMissing:
		return radarport.VisitorExternalContactMissing
	case identityport.AdminRadarVisitorExternalContactAmbiguous:
		return radarport.VisitorExternalContactAmbiguous
	default:
		return radarport.VisitorExternalContactUnavailable
	}
}

func pointer(value string) *string { return &value }

var _ radarport.AdminVisitorPresentationReader = radarVisitorPresentationAdapter{}
