package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type imagePreparationEffects struct{ fail bool }

func (s *imagePreparationEffects) AcceptAndQueueWithin(ctx context.Context, in effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	raw, _ := json.Marshal(in.Envelope)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO image_test_effects(envelope) VALUES($1) RETURNING id`, raw).Scan(&id)
	if s.fail {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("accept failed")
	}
	return effectport.Projection{ID: fmt.Sprintf("eer_%d", id), State: effectport.StateQueued}, effectport.Receipt{}, err
}

type imagePreparationUploader struct {
	calls   int
	unknown bool
	now     time.Time
	t       *testing.T
}

func (s *imagePreparationUploader) UploadSidebarImage(ctx context.Context, source outboundport.SidebarImagePreparationSource) (outboundport.SidebarImageUploadReceipt, bool, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err == nil {
		s.t.Fatal("provider upload inside transaction")
	}
	s.calls++
	if s.unknown {
		return outboundport.SidebarImageUploadReceipt{}, true, errors.New("uncertain upload")
	}
	return outboundport.SidebarImageUploadReceipt{MediaID: "official-image", ReadyUntil: s.now.Add(72 * time.Hour)}, true, nil
}

func TestSidebarImagePreparationPostgreSQL(t *testing.T) {
	dsn, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("sidebar_image_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := platformpostgres.Wrap(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0122_outbound_sidebar_image_preparation.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	recovery, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0123_outbound_sidebar_image_recovery.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(recovery)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE image_test_effects(id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,envelope JSONB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	effects := &imagePreparationEffects{}
	service, err := NewSidebarMediaPreparationService(uow, effects, pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	source := outboundport.SidebarImagePreparationSource{ImageID: 1, Content: []byte("valid-image-bytes"), FileName: "image.png", MediaType: "image/png", Scope: "corp-and-agent"}
	source.SourceDigest = sha256.Sum256(source.Content)
	required := now.Add(6 * time.Minute)
	var wg sync.WaitGroup
	results := make(chan outboundport.SidebarImagePreparation, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := service.PrepareSidebarImage(ctx, source, required)
			results <- r
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first outboundport.SidebarImagePreparation
	for r := range results {
		if first.EffectID == "" {
			first = r
		}
		if r.EffectID != first.EffectID || r.State != "queued" {
			t.Fatalf("not deduplicated: %#v", r)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM image_test_effects`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("effect count %d %v", count, err)
	}
	var envelope effectport.Envelope
	var encoded []byte
	if err = pool.QueryRow(ctx, `SELECT envelope FROM image_test_effects WHERE id=1`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	uploader := &imagePreparationUploader{now: now, t: t}
	provider, _ := NewSidebarMediaPreparationProvider(service, uploader)
	attempt := effectport.Attempt{EffectID: first.EffectID, Number: 1, Generation: 1, Fence: 1}
	outcome, err := provider.Execute(ctx, envelope, attempt)
	if err != nil || outcome.Completion != effectport.StateExecuted {
		t.Fatalf("upload: %#v %v", outcome, err)
	}
	// A failed completion transaction must roll back receipt, audit and outbox.
	err = uow.Within(ctx, func(txctx context.Context) error {
		if e := service.CompleteEffect(txctx, first.EffectID, envelope, attempt, outcome); e != nil {
			return e
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("wanted rollback")
	}
	current, _ := service.PrepareSidebarImage(ctx, source, required)
	if current.State != "queued" {
		t.Fatalf("split completion: %#v", current)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		return service.CompleteEffect(txctx, first.EffectID, envelope, attempt, outcome)
	}); err != nil {
		t.Fatal(err)
	}
	current, err = service.PrepareSidebarImage(ctx, source, required)
	if err != nil || current.State != "ready" || current.MediaID != "official-image" {
		t.Fatalf("ready: %#v %v", current, err)
	}
	// Known expiry permits a new upload; an unknown outcome then blocks all
	// replacement keys, including a changed source or another browser.
	now = now.Add(72 * time.Hour)
	required = now.Add(6 * time.Minute)
	next, err := service.PrepareSidebarImage(ctx, source, required)
	if err != nil || next.EffectID == first.EffectID {
		t.Fatalf("renew: %#v %v", next, err)
	}
	if err = pool.QueryRow(ctx, `SELECT envelope FROM image_test_effects WHERE id=2`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(encoded, &envelope)
	attempt.EffectID = next.EffectID
	uploader.unknown = true
	outcome, err = provider.Execute(ctx, envelope, attempt)
	if err != nil || outcome.Completion != effectport.StateUnknown {
		t.Fatalf("unknown: %#v %v", outcome, err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		return service.CompleteEffect(txctx, next.EffectID, envelope, attempt, outcome)
	}); err != nil {
		t.Fatal(err)
	}
	source.Content = []byte("changed-image-bytes")
	source.SourceDigest = sha256.Sum256(source.Content)
	current, err = service.PrepareSidebarImage(ctx, source, required)
	if err != nil || current.State != "outcome_unknown" || current.EffectID != next.EffectID {
		t.Fatalf("unknown replacement: %#v %v", current, err)
	}
	// Effect acceptance and owning intent are one transaction.
	effects.fail = true
	source.ImageID = 2
	if _, err = service.PrepareSidebarImage(ctx, source, required); err == nil {
		t.Fatal("expected acceptance failure")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM outbound_sidebar_image_preparations WHERE image_id=2`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan intent %d %v", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM image_test_effects`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("orphan effect %d %v", count, err)
	}
	if uploader.calls != 2 {
		t.Fatalf("upload calls %d", uploader.calls)
	}
}
