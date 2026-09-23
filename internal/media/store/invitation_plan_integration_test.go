package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	e "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type invitationTestEffects struct{ fail bool }

func (f invitationTestEffects) AcceptAndQueueWithin(ctx context.Context, c e.AcceptCommand) (e.Projection, e.Receipt, error) {
	tx, err := pg.RequireTransaction(ctx)
	if err != nil {
		return e.Projection{}, e.Receipt{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO invitation_effect_probe(digest) VALUES($1) RETURNING id`, string(c.ReceiptKey)).Scan(&id)
	if f.fail {
		err = errors.New("forced effect acceptance failure")
	}
	return e.Projection{ID: fmt.Sprintf("eer_%d", id)}, e.Receipt{}, err
}
func TestPostgreSQLInvitationAtomicSaveAndUpgrade(t *testing.T) {
	url, err := config.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("invitation_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, _ := pgxpool.ParseConfig(url)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	for _, f := range []string{"0007_media.sql", "0195_media_invitation_plans.sql", "0204_media_invitation_join_ways.sql"} {
		raw, err := os.ReadFile(filepath.Join("../../../migrations", f))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, string(raw)); err != nil {
			t.Fatal(f, err)
		}
	}
	if _, err = native.Exec(ctx, `CREATE TABLE invitation_effect_probe(id BIGINT GENERATED ALWAYS AS IDENTITY,digest TEXT)`); err != nil {
		t.Fatal(err)
	}
	pool, _ := pg.Wrap(native, time.Second)
	uow, _ := pg.NewUnitOfWork(pool)
	repo, _ := NewPostgreSQL(native, uow)
	n := 1
	input := p.InvitationInput{Name: "测试活动", Title: "欢迎入群", Mode: "sequence", Threshold: &n, Enabled: true, ChatIDs: []string{"a", "b"}}
	if _, err = repo.SaveInvitationPlan(ctx, input, 1, "failed-save-00000001", "https://crm.example", invitationTestEffects{fail: true}); err == nil {
		t.Fatal("expected rollback")
	}
	var count int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_group_invites)+(SELECT count(*) FROM invitation_effect_probe)+(SELECT count(*) FROM media_audit_events)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial acceptance", count, err)
	}
	plan, err := repo.SaveInvitationPlan(ctx, input, 1, "plan-save-0000000001", "https://crm.example", invitationTestEffects{})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.SaveInvitationPlan(ctx, input, 1, "plan-save-0000000001", "https://crm.example", invitationTestEffects{})
	if err != nil || replay.ID != plan.ID {
		t.Fatal("replay", err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM invitation_effect_probe`).Scan(&count); err != nil || count != 1 {
		t.Fatal("plan must have one stable Provider effect", count, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return repo.CompleteInvitationPlanCode(tx, p.InvitationCodeCompletion{EffectID: "eer_2", State: "executed", QRCode: "https://example.test/code", ConfigID: "config"})
	}); err != nil {
		t.Fatal(err)
	}
	// Numeric material IDs remain consumable through the existing material API.
	material, err := repo.GroupInvite(ctx, plan.ID)
	if err != nil || material["join_url"] != plan.JoinURL {
		t.Fatal(material, err)
	}
	before, err := repo.ReadInvitationPlan(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.State = "full"
	after.Bindings = append([]p.InvitationBinding(nil), before.Bindings...)
	after.Bindings[0].Retired = true
	if err = repo.ApplyInvitationEvaluation(ctx, before, after); err != nil {
		t.Fatal(err)
	}
	input.ID = plan.ID
	input.Version = plan.Version
	if _, err = repo.SaveInvitationPlan(ctx, input, 1, "stale-edit-00000001", "https://crm.example", invitationTestEffects{}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale edit accepted", err)
	}
	var legacyID int64
	if err = native.QueryRow(ctx, `INSERT INTO media_group_invites(name,title,description,join_url,enabled,created_by,updated_by) VALUES('旧邀请','旧标题','','https://work.weixin.qq.com/old-link',true,1,1) RETURNING id`).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	legacy, err := repo.ReadInvitationPlan(ctx, legacyID)
	if err != nil || legacy.State != "legacy" || legacy.Token != "" {
		t.Fatal("legacy visibility", legacy, err)
	}
	upgraded, err := repo.SaveInvitationPlan(ctx, p.InvitationInput{ID: legacyID, Version: legacy.Version, Name: legacy.Name, Title: legacy.Title, Mode: "single", Enabled: true, ChatIDs: []string{"a"}}, 1, "legacy-upgrade-000001", "https://crm.example", invitationTestEffects{})
	if err != nil || upgraded.ID != legacyID || upgraded.Token == "" {
		t.Fatal("explicit upgrade", upgraded, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM invitation_effect_probe`).Scan(&count); err != nil || count != 2 {
		t.Fatal("upgrade recreated shared official code", count, err)
	}

}
