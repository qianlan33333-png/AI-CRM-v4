package store_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

// This is only a disabled Product-local configuration save. There is no
// destination manager, effect accepter, test acceptance, worker or dispatch.
func TestPostgreSQLExternalPushLocalSaveKeepsActorEventNamespacesAligned(t *testing.T) {
	f := newMemberGridAdversarialFixture(t)
	for _, name := range []string{"0095_product_external_push.sql", "0146_product_external_push_field_mapping.sql"} {
		migration, err := os.ReadFile(memberGridMigration(t, name))
		if err != nil {
			t.Fatal(err)
		}
		// The suffix imports retired 0010 projection fields; this fixture only
		// needs the current native Product configuration table definition.
		if _, err = f.native.Exec(f.ctx, strings.Split(string(migration), "-- Old 0010")[0]); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	wrapped, err := platformpostgres.Wrap(f.native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(f.native, uow)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	events, err := productstore.NewTransactionalEventAppender(audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	service, err := productapp.NewCommerceExternalPushService(uow, repository, nil, unusedExternalPushStatusReader{}, events)
	if err != nil {
		t.Fatal(err)
	}
	first := productport.SaveExternalPushConfigurationCommand{
		ProductID: f.productID, ProductKind: productport.ExternalPushServicePeriod,
		BusinessParametersSet: true, PushType: "days", Remark: "first local setting",
		CustomParams: map[string]any{}, Actor: 1, IdempotencyKey: "local-config-shared-browser-key-0001",
	}
	value, err := service.SaveExternalPushConfiguration(f.ctx, first)
	if err != nil || value.Enabled || value.Revision != 1 {
		t.Fatalf("first save=%+v err=%v", value, err)
	}
	second := first
	second.Actor, second.ExpectedRevision, second.Remark = 2, value.Revision, "second local setting"
	other, err := service.SaveExternalPushConfiguration(f.ctx, second)
	if err != nil || other.Enabled || other.Revision != 2 || other.Remark != second.Remark {
		t.Fatalf("independent actor collided with local audit/outbox: value=%+v err=%v", other, err)
	}
	for _, input := range []productport.SaveExternalPushConfigurationCommand{first, second} {
		replay, err := service.SaveExternalPushConfiguration(f.ctx, input)
		if err != nil || replay.Revision != input.ExpectedRevision+1 || replay.Remark != input.Remark {
			t.Fatalf("actor=%d replay=%+v err=%v", input.Actor, replay, err)
		}
	}
	var receipts, auditRows, outbox int64
	if err = f.native.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM product_operation_receipts),(SELECT count(*) FROM audit_events),(SELECT count(*) FROM outbox_events)`).Scan(&receipts, &auditRows, &outbox); err != nil || receipts != 2 || auditRows != 2 || outbox != 2 {
		t.Fatalf("receipts=%d audit=%d outbox=%d err=%v", receipts, auditRows, outbox, err)
	}
}

type unusedExternalPushStatusReader struct{}

func (unusedExternalPushStatusReader) ReadExternalPushTestStatus(context.Context, productport.ID, string) (productport.ExternalPushTestStatus, error) {
	panic("local save must never read an effect status")
}
