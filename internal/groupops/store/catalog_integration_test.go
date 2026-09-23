package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	catalogapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLCatalogDailyManualRerunAndFreshness(t *testing.T) {
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
	schema := fmt.Sprintf("catalog_%d", time.Now().UnixNano())
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
	// Use the actual original directory definition and current evolution.
	raw, err := os.ReadFile("../../../migrations/0012_group_ops.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	a := strings.Index(s, "CREATE TABLE group_ops_directory_groups (")
	b := strings.Index(s[a:], "CREATE TABLE group_ops_directory_refresh_receipts") + a
	if _, err = native.Exec(ctx, `CREATE TABLE admin_users(id BIGINT PRIMARY KEY);`+s[a:b]+`ALTER TABLE group_ops_directory_groups ADD COLUMN external_member_count INTEGER;`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"0119_group_ops_unnamed_groups.sql", "0194_group_invitation_catalog.sql"} {
		raw, err = os.ReadFile(filepath.Join("../../../migrations", f))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, string(raw)); err != nil {
			t.Fatal(f, err)
		}
	}
	pool, _ := pg.Wrap(native, time.Second)
	uow, _ := pg.NewUnitOfWork(pool)
	repo, _ := NewPostgreSQL(native, uow)
	now := time.Now().UTC()
	jobs := []int64{}
	enqueue := func(ctx context.Context, id int64) error {
		if _, err := pg.RequireTransaction(ctx); err != nil {
			return err
		}
		jobs = append(jobs, id)
		return nil
	}
	if err = repo.WithinCatalogRequest(ctx, false, now, enqueue); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	initial, err := repo.CatalogStatus(ctx)
	// PostgreSQL timestamps are stored with microsecond precision, while `now`
	// may contain nanoseconds. Compare within the persistence precision.
	if err != nil || initial.NextAutoAt.Sub(now.Add(24*time.Hour)) > time.Millisecond || now.Add(24*time.Hour).Sub(initial.NextAutoAt) > time.Millisecond {
		t.Fatal(initial, err)
	}
	if err = repo.WithinCatalogRequest(ctx, true, now.Add(time.Hour), enqueue); err != nil {
		t.Fatal(err)
	}
	if err = repo.WithinCatalogRequest(ctx, true, now.Add(2*time.Hour), enqueue); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatal("duplicate in-flight scan", jobs)
	}
	if err = repo.FinishCatalogRun(ctx, jobs[0], false, now.Add(3*time.Hour), enqueue); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatal("missing coalesced rerun", jobs)
	}
	status, err := repo.CatalogStatus(ctx)
	if err != nil || !status.NextAutoAt.Equal(initial.NextAutoAt) || status.Rerun {
		t.Fatal(status, err)
	}
	fresh := now.Add(4 * time.Hour)
	g := p.CatalogGroup{ChatID: "test", Name: "测试群", OwnerUserID: "owner", MemberCount: 18, State: "ready", ObservedAt: &fresh, CheckedAt: fresh}
	if err = repo.SaveCatalogGroup(ctx, g, jobs[1]); err != nil {
		t.Fatal(err)
	}
	old := g
	old.Name = "旧名"
	old.CheckedAt = now
	old.ObservedAt = &now
	if err = repo.SaveCatalogGroup(ctx, old, 0); err != nil {
		t.Fatal(err)
	}
	failed := p.CatalogGroup{ChatID: "test", State: "failed", CheckedAt: fresh.Add(time.Minute)}
	if err = repo.SaveCatalogGroup(ctx, failed, jobs[1]); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ReadCatalogGroup(ctx, "test")
	if err != nil || got.Name != "测试群" || got.MemberCount != 18 || got.State != "failed" {
		t.Fatal(got, err)
	}
	if err = repo.SaveCatalogGroup(ctx, p.CatalogGroup{ChatID: "missing", State: "failed", CheckedAt: fresh}, jobs[1]); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishCatalogRun(ctx, jobs[1], false, fresh, enqueue); err != nil {
		t.Fatal(err)
	}
	status, err = repo.CatalogStatus(ctx)
	if err != nil || status.Run.State != "partial" || status.Run.Discovered != 2 || status.Run.Failed != 2 {
		t.Fatal(status, err)
	}
	page, err := repo.ListCatalog(ctx, "测试", 50, 0)
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	provider := &catalogPages{}
	repairs := []string{}
	service := catalogapp.CatalogService{Store: repo, Provider: provider, Enabled: true, Enqueue: enqueue, RetryDetail: func(_ context.Context, id string) error { repairs = append(repairs, id); return nil }}
	if _, err = service.RequestCatalogSync(ctx, true); err != nil {
		t.Fatal(err)
	}
	runID := jobs[len(jobs)-1]
	if err = service.ProcessCatalog(ctx, runID); err != nil {
		t.Fatal(err)
	}
	status, err = repo.CatalogStatus(ctx)
	if err != nil || status.Run.State != "partial" || provider.pages != 2 || len(repairs) != 1 {
		t.Fatalf("paging/partial: %+v pages=%d repairs=%v err=%v", status, provider.pages, repairs, err)
	}
	if err = service.ProcessCatalog(ctx, runID); err != nil || provider.pages != 2 {
		t.Fatal("completed replay scanned again", err)
	}
	provider.repaired = true
	if _, err = service.RefreshCatalogGroup(ctx, "retry-name"); err != nil {
		t.Fatal(err)
	}
	got, err = repo.ReadCatalogGroup(ctx, "retry-name")
	if err != nil || got.Name != "补拉群名" || got.ObservedAt == nil {
		t.Fatal("detail repair", got, err)
	}

}

type catalogPages struct {
	pages    int
	repaired bool
}

func (p *catalogPages) ListAllGroupChats(_ context.Context, cursor string, _ int) (w.GroupChatPage, error) {
	p.pages++
	if cursor == "" {
		return w.GroupChatPage{Items: []w.GroupChatListItem{{ChatID: "new-named"}, {ChatID: "new-unnamed"}}, NextCursor: "page-2"}, nil
	}
	return w.GroupChatPage{Items: []w.GroupChatListItem{{ChatID: "retry-name"}}}, nil
}
func (p *catalogPages) GetGroupChat(_ context.Context, id string) (w.GroupChat, error) {
	if id == "retry-name" && !p.repaired {
		return w.GroupChat{}, errors.New("temporary detail error")
	}
	name := "新群名"
	if id == "new-unnamed" {
		name = ""
	}
	if id == "retry-name" {
		name = "补拉群名"
	}
	return w.GroupChat{ChatID: id, Name: name, OwnerUserID: "owner", MemberCount: 2}, nil
}
