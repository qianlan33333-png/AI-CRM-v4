package wecom

import (
	"context"
	"crypto/sha256"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// OwnerHandoffRelationship returns only one exact, current relationship. It
// does not infer an employee from any other contact relation and never writes
// the existing WeCom projection.
func (PostgreSQLFollowRelationshipStore) OwnerHandoffRelationship(ctx context.Context, customerID customerdomain.CustomerID, corpScope, employeeUserID string) (wecomport.OwnerHandoffRelationship, error) {
	if err := lockDirectoryRead(ctx); err != nil {
		return wecomport.OwnerHandoffRelationship{}, err
	}
	corpID, ok := ownerHandoffCorpID(corpScope)
	if !ok || customerID < 1 || !validFollowText(employeeUserID, 1024) {
		return wecomport.OwnerHandoffRelationship{}, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return wecomport.OwnerHandoffRelationship{}, err
	}
	var active bool
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT o.relationship_status='active',o.updated_at FROM wecom_customer_owner_observations o JOIN wecom_customer_sync_runs r ON r.id=o.last_seen_run_id AND r.status='succeeded' WHERE o.corp_scope='wecom-corp:'||$1 AND o.employee_id=$2 AND customer_id=$3`, corpID, employeeUserID, customerID).Scan(&active, &updated)
	if err == pgx.ErrNoRows {
		return wecomport.OwnerHandoffRelationship{CustomerID: customerID, CorpScope: corpScope, EmployeeUserID: employeeUserID}, nil
	}
	if err != nil {
		return wecomport.OwnerHandoffRelationship{}, err
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{"owner-handoff-relation-v1", corpID, employeeUserID, strconv.FormatInt(int64(customerID), 10), strconv.FormatBool(active), updated.UTC().Format(time.RFC3339Nano)}, "\x00")))
	return wecomport.OwnerHandoffRelationship{CustomerID: customerID, CorpScope: corpScope, EmployeeUserID: employeeUserID, Active: active, VersionDigest: digest}, nil
}

func ownerHandoffCorpID(scope string) (string, bool) {
	if !strings.HasPrefix(scope, "wecom-corp:") {
		return "", false
	}
	corpID := strings.TrimPrefix(scope, "wecom-corp:")
	return corpID, validFollowText(corpID, 256)
}

var _ wecomport.OwnerHandoffRelationshipReader = PostgreSQLFollowRelationshipStore{}
var _ wecomport.OwnerHandoffRelationshipLister = PostgreSQLFollowRelationshipStore{}

// ListOwnerHandoffCustomerIDs reads only current source follow relations for
// the explicit all-range operation. It never infers a source employee.
func (PostgreSQLFollowRelationshipStore) ListOwnerHandoffCustomerIDs(ctx context.Context, corpScope, employeeUserID string, limit int) ([]customerdomain.CustomerID, error) {
	if err := lockDirectoryRead(ctx); err != nil {
		return nil, err
	}
	corpID, ok := ownerHandoffCorpID(corpScope)
	if !ok || !validFollowText(employeeUserID, 1024) || limit < 1 || limit > 100001 {
		return nil, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT o.customer_id FROM wecom_customer_owner_observations o JOIN wecom_customer_sync_runs r ON r.id=o.last_seen_run_id AND r.status='succeeded' WHERE o.corp_scope='wecom-corp:'||$1 AND o.employee_id=$2 AND relationship_status='active' ORDER BY customer_id LIMIT $3`, corpID, employeeUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]customerdomain.CustomerID, 0)
	for rows.Next() {
		var id customerdomain.CustomerID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
