package store

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type PersistenceFailure struct {
	SQLState   string
	Constraint string
}

// SafePersistenceFailure extracts only non-sensitive PostgreSQL classifier
// fields. It never returns the original database error, SQL text or row data.
func SafePersistenceFailure(err error) (PersistenceFailure, bool) {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError == nil {
		return PersistenceFailure{}, false
	}
	return PersistenceFailure{SQLState: postgresError.Code, Constraint: postgresError.ConstraintName}, true
}

func unique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
