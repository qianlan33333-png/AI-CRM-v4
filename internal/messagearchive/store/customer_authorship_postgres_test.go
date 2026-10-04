package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestCustomerAuthorshipFiltersBeforePaginationPostgreSQL(t *testing.T) {
	native, cleanup := archiveIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	var customer, lineageMember, other int64
	for _, dest := range []*int64{&customer, &lineageMember, &other} {
		if err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := native.Exec(ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES('wecom-corp:authorship-test')`); err != nil {
		t.Fatal(err)
	}
	seq := 0
	insert := func(chatType, actor, status string, sender *int64, at time.Time) int64 {
		t.Helper()
		seq++
		var id int64
		if err := native.QueryRow(ctx, `INSERT INTO message_archive_messages(corp_scope,seq,msgid,msgtype,conversation_type,msgtime_ms,occurred_at,content_text)
			VALUES('wecom-corp:authorship-test',$1,$2,'text',$3,$4,$5,$6) RETURNING id`, seq, fmt.Sprintf("message-%d", seq), chatType, at.UnixMilli(), at, fmt.Sprintf("synthetic statement %d", seq)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, participant := range []struct {
			role, actor, status string
			customer            *int64
		}{{"sender", actor, status, sender}, {"recipient", "external_customer", "found", &customer}} {
			digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", id, participant.role)))
			if _, err := native.Exec(ctx, `INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value_digest,customer_id_at_ingest,resolution_status)
				VALUES($1,$2,$3,$4,$5,$6)`, id, participant.role, participant.actor, digest[:], participant.customer, participant.status); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	private := insert("private", "external_customer", "found", &customer, now.Add(-6*time.Minute))
	group := insert("group", "external_customer", "found", &customer, now.Add(-5*time.Minute))
	lineage := insert("group", "external_customer", "found", &lineageMember, now.Add(-4*time.Minute))
	_ = insert("group", "unknown", "conflict", nil, now.Add(-3*time.Minute))
	_ = insert("private", "staff", "not_applicable", nil, now.Add(-2*time.Minute))
	otherGroup := insert("group", "external_customer", "found", &other, now.Add(-time.Minute))
	_ = insert("group", "external_customer", "found", &customer, now.Add(time.Minute))
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	query := archiveport.CustomerQuery{CustomerIDs: []customerdomain.CustomerID{customerdomain.CustomerID(customer), customerdomain.CustomerID(lineageMember)}, AuthoredByCustomerOnly: true, Limit: 1, Watermark: now}
	read := func(query archiveport.CustomerQuery) archiveport.CustomerPage {
		t.Helper()
		var page archiveport.CustomerPage
		if err := uow.Within(ctx, func(tx context.Context) error {
			var err error
			page, err = (PostgreSQL{}).CustomerMessages(tx, query)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return page
	}
	for _, expected := range []int64{lineage, group, private} {
		page := read(query)
		if len(page.Items) != 1 || page.Items[0].ID != expected || page.Items[0].Direction != "customer_to_staff" {
			t.Fatalf("authored page includes a recipient/staff/unresolved message or loses own history: %+v, expected id=%d", page, expected)
		}
		query.AfterAt, query.AfterID = page.Items[0].OccurredAt, page.Items[0].ID
	}
	if page := read(query); len(page.Items) != 0 {
		t.Fatalf("authored page should be exhausted: %+v", page)
	}
	query.AfterAt, query.AfterID, query.AuthoredByCustomerOnly = time.Time{}, 0, false
	page := read(query)
	if len(page.Items) != 1 || page.Items[0].ID != otherGroup {
		t.Fatalf("default conversation view changed: %+v", page)
	}
	query.AuthoredByCustomerOnly, query.Limit, query.ChatType = true, 20, "private"
	page = read(query)
	if len(page.Items) != 1 || page.Items[0].ID != private {
		t.Fatalf("private authorship filter changed: %+v", page)
	}
	query.CustomerIDs = []customerdomain.CustomerID{customerdomain.CustomerID(other)}
	query.ChatType = ""
	page = read(query)
	if len(page.Items) != 1 || page.Items[0].ID != otherGroup {
		t.Fatalf("another customer's sender scope is incorrect: %+v", page)
	}
}
