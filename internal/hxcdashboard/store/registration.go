package store

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

// RegistrationFacts pins the publication within the caller's UoW. Absence is
// explicit unknown, never unregistered; no network or source lookup occurs.
func (s *PostgreSQL) RegistrationFacts(ctx context.Context, customers []customerdomain.CustomerID, at time.Time) (map[customerdomain.CustomerID]hxcport.RegistrationFact, error) {
	ids, e := sharedFactsIDs(customers)
	if e != nil {
		return nil, e
	}
	out := map[customerdomain.CustomerID]hxcport.RegistrationFact{}
	for _, id := range customers {
		out[id] = hxcport.RegistrationFact{State: "unknown"}
	}
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return nil, e
	}
	if at.IsZero() {
		return nil, hxcport.ErrSharedFactsVersionUnavailable
	}
	var version int64
	var asOf time.Time
	if e = tx.QueryRow(ctx, `SELECT id,projection_as_of FROM hxc_dashboard_versions WHERE projection_as_of <= $1 ORDER BY projection_as_of DESC,id DESC LIMIT 1`, at.UTC()).Scan(&version, &asOf); e != nil {
		return nil, hxcport.ErrSharedFactsVersionUnavailable
	}
	for _, id := range customers {
		out[id] = hxcport.RegistrationFact{State: "unknown", AsOf: asOf, Version: version}
	}
	rows, e := tx.Query(ctx, `SELECT r.customer_id,r.state,v.projection_as_of,v.id FROM hxc_dashboard_versions v JOIN hxc_registration_coverage r ON r.projection_id=v.id WHERE v.id=$2 AND r.customer_id=ANY($1)`, ids, version)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id customerdomain.CustomerID
		var f hxcport.RegistrationFact
		if e = rows.Scan(&id, &f.State, &f.AsOf, &f.Version); e != nil {
			return nil, e
		}
		out[id] = f
	}
	return out, rows.Err()
}

var _ hxcport.RegistrationReader = (*PostgreSQL)(nil)
