package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	e "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	g "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaApp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	d "github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediaStore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type invitationVirtualEffects struct{ fail bool }

func (f invitationVirtualEffects) AcceptAndQueueWithin(ctx context.Context, command e.AcceptCommand) (e.Projection, e.Receipt, error) {
	tx, err := pg.RequireTransaction(ctx)
	if err != nil {
		return e.Projection{}, e.Receipt{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO invitation_effect_probe(digest) VALUES($1) ON CONFLICT(digest) DO UPDATE SET digest=EXCLUDED.digest RETURNING id`, string(command.ReceiptKey)).Scan(&id)
	if f.fail {
		err = fmt.Errorf("forced native acceptance failure")
	}
	return e.Projection{ID: fmt.Sprintf("eer_%d", id)}, e.Receipt{}, err
}

type invitationVirtualProvider struct {
	creates, updates int
	qr, config       string
	nativeOptions    w.InvitationJoinWayOptions
}

func (v *invitationVirtualProvider) CreateInvitationCode(context.Context, string) (w.InvitationCode, error) {
	return w.InvitationCode{}, fmt.Errorf("legacy per-group code must not be created")
}
func (v *invitationVirtualProvider) CreateInvitationCodeForGroups(_ context.Context, ids []string) (w.InvitationCode, error) {
	v.creates++
	if len(ids) != 1 || ids[0] != "a" {
		return w.InvitationCode{}, fmt.Errorf("initial groups: %v", ids)
	}
	return w.InvitationCode{ConfigID: v.config, QRCode: v.qr}, nil
}
func (v *invitationVirtualProvider) UpdateInvitationCodeForGroups(_ context.Context, configID string, ids []string) (w.InvitationCode, error) {
	v.updates++
	if configID != v.config || len(ids) != 1 || ids[0] != "b" {
		return w.InvitationCode{}, fmt.Errorf("updated config/groups: %q %v", configID, ids)
	}
	return w.InvitationCode{ConfigID: v.config, QRCode: v.qr}, nil
}

func (v *invitationVirtualProvider) CreateNativeInvitationCode(_ context.Context, ids []string, o w.InvitationJoinWayOptions) (w.InvitationCode, error) {
	v.creates++
	v.nativeOptions = o
	if len(ids) != 2 {
		return w.InvitationCode{}, fmt.Errorf("native initial groups %v", ids)
	}
	return w.InvitationCode{ConfigID: v.config, QRCode: v.qr}, nil
}
func (v *invitationVirtualProvider) UpdateNativeInvitationCode(_ context.Context, id string, ids []string, o w.InvitationJoinWayOptions) (w.InvitationCode, error) {
	v.updates++
	v.nativeOptions = o
	if id != v.config || len(ids) != 2 {
		return w.InvitationCode{}, fmt.Errorf("native update changed identity %s %v", id, ids)
	}
	return w.InvitationCode{ConfigID: v.config, QRCode: v.qr}, nil
}

type invitationVirtualCatalog struct{ g.Catalog }

func (invitationVirtualCatalog) ReadCatalogGroup(_ context.Context, id string) (g.CatalogGroup, error) {
	now := time.Now().UTC().Add(-24 * time.Hour)
	return g.CatalogGroup{ChatID: id, MemberCount: 200, ObservedAt: &now}, nil
}

func (invitationVirtualCatalog) RefreshCatalogGroup(context.Context, string) (g.CatalogGroup, error) {
	return g.CatalogGroup{}, fmt.Errorf("directory provider disabled")
}

// TestInvitationVirtualAcceptance checks the complete local intent, Provider
// boundary and stable QR contract in an isolated PostgreSQL schema.
func TestInvitationVirtualAcceptance(t *testing.T) {
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
	schema := fmt.Sprintf("invitation_virtual_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	for _, name := range []string{"0007_media.sql", "0195_media_invitation_plans.sql", "0204_media_invitation_join_ways.sql", "0215_media_native_invitation.sql"} {
		body, readErr := os.ReadFile(filepath.Join("../../migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = native.Exec(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err = native.Exec(ctx, `CREATE TABLE invitation_effect_probe(id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,digest TEXT NOT NULL UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	pool, err := pg.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := pg.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := mediaStore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	effects := invitationVirtualEffects{}
	threshold := 1
	input := p.InvitationInput{Name: "virtual plan", Title: "join", Mode: "sequence", Threshold: &threshold, Enabled: true, ChatIDs: []string{"a", "b"}}
	plan, err := repo.SaveInvitationPlan(ctx, input, 1, "virtual-plan-save-00001", "https://crm.example", effects)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := repo.SaveInvitationPlan(ctx, input, 1, "virtual-plan-save-00001", "https://crm.example", effects)
	if err != nil || replayed.ID != plan.ID {
		t.Fatalf("save replay %+v %v", replayed, err)
	}
	var count int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM invitation_effect_probe`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("create effects=%d err=%v", count, err)
	}
	t.Logf("phase=plan_saved plan_id=%d effects=%d", plan.ID, count)
	provider := &invitationVirtualProvider{config: "stable-config", qr: "https://wework.qpic.cn/stable-code"}
	adapter := &outbound.InvitationCodeProvider{Store: repo, Provider: provider, Enabled: true}
	sink := outbound.InvitationCodeCompletionSink{Store: repo}
	runEffect := func(ids []string) {
		t.Helper()
		raw, _ := json.Marshal(ids)
		source := e.Hash("media.invitation.join-way.v2", strconv.FormatInt(plan.ID, 10), string(raw))
		intent, readErr := repo.ReadInvitationPlanCodeIntent(ctx, string(source))
		if readErr != nil {
			t.Fatal(readErr)
		}
		env := e.Envelope{Owner: e.OwnerOutbound, Kind: e.KindInvitationCode, SourceRefDigest: source, TargetRefDigest: e.Hash("invitation.plan.target.v2", strconv.FormatInt(plan.ID, 10)), PayloadDigest: e.Hash("invitation.plan-code.v2", string(raw)), PolicyVersionHash: e.Hash("invitation.code.policy.v2")}
		attempt := e.Attempt{EffectID: intent.EffectID, Number: 1}
		result, executeErr := adapter.Execute(ctx, env, attempt)
		if executeErr != nil || result.Completion != e.StateExecuted {
			t.Fatalf("virtual Provider result=%+v err=%v", result, executeErr)
		}
		if err = uow.Within(ctx, func(tx context.Context) error { return sink.CompleteEffect(tx, intent.EffectID, env, attempt, result) }); err != nil {
			t.Fatal(err)
		}
	}
	runEffect([]string{"a"})
	plan, err = repo.ReadPublicInvitation(ctx, plan.Token)
	if err != nil || plan.ProviderConfigID != provider.config || plan.ProviderQRCode != provider.qr {
		t.Fatalf("created QR %+v %v", plan, err)
	}
	now := time.Now().UTC()
	facts := map[string]g.CatalogGroup{"a": {ChatID: "a", MemberCount: 0, ObservedAt: &now}, "b": {ChatID: "b", MemberCount: 0, ObservedAt: &now}}
	next := d.EvaluateInvitation(plan, facts, now)
	if next.CurrentChatID != "a" {
		t.Fatalf("initial target=%q", next.CurrentChatID)
	}
	if err = repo.ApplyInvitationEvaluationWithEffects(ctx, plan, next, effects); err != nil {
		t.Fatal(err)
	}
	plan, err = repo.ReadPublicInvitation(ctx, plan.Token)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("phase=initial_active state=%s current=%s bindings=%+v provider_state=%s", plan.State, plan.CurrentChatID, plan.Bindings, plan.ProviderState)
	facts["a"] = g.CatalogGroup{ChatID: "a", MemberCount: 1, ObservedAt: &now}
	input.ID, input.Version = plan.ID, plan.Version
	saved, saveErr := repo.SaveInvitationPlan(ctx, input, 1, "virtual-plan-edit-00001", "https://crm.example", effects)
	if saveErr != nil || saved.CurrentChatID != "a" || saved.ProviderQRCode != provider.qr {
		t.Fatalf("save advanced before Provider update: state=%q chat=%q qr=%q err=%v", saved.State, saved.CurrentChatID, saved.ProviderQRCode, saveErr)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM invitation_effect_probe`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unchanged binding recreated QR: effects=%d err=%v", count, err)
	}
	plan = saved
	next = d.EvaluateInvitation(plan, facts, now)
	if next.CurrentChatID != "b" {
		t.Fatalf("switch target=%q", next.CurrentChatID)
	}
	if err = repo.ApplyInvitationEvaluationWithEffects(ctx, plan, next, effects); err != nil {
		t.Fatal(err)
	}
	beforeProvider, err := repo.ReadPublicInvitation(ctx, plan.Token)
	if err != nil {
		t.Fatal(err)
	}
	if beforeProvider.ProviderQRCode != provider.qr || beforeProvider.State == "active" {
		t.Fatalf("switch visible before Provider confirmation: state=%q qr=%q", beforeProvider.State, beforeProvider.ProviderQRCode)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM invitation_effect_probe`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("switch effects=%d err=%v", count, err)
	}
	history, err := repo.InvitationHistory(ctx, plan.ID)
	if err != nil || len(history) != 2 || history[0].From != "a" || history[0].To != "b" {
		t.Fatalf("switch history=%+v err=%v", history, err)
	}
	runEffect([]string{"b"})
	afterProvider, err := repo.ReadPublicInvitation(ctx, plan.Token)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := d.EvaluateInvitation(afterProvider, facts, now)
	if err = repo.ApplyInvitationEvaluationWithEffects(ctx, afterProvider, confirmed, effects); err != nil {
		t.Fatal(err)
	}
	final, err := repo.ReadPublicInvitation(ctx, plan.Token)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "active" || final.CurrentChatID != "b" || final.ProviderConfigID != provider.config || final.ProviderQRCode != provider.qr || provider.creates != 1 || provider.updates != 1 {
		t.Fatalf("stable QR or Provider call mismatch: state=%q chat=%q config=%q qr=%q creates=%d updates=%d", final.State, final.CurrentChatID, final.ProviderConfigID, final.ProviderQRCode, provider.creates, provider.updates)
	}
	t.Logf("phase=switch_confirmed config_stable=%t qr_stable=%t creates=%d updates=%d", true, true, provider.creates, provider.updates)
	// Convert the existing plan without changing its public token or provider QR.
	nativeInput := p.InvitationInput{ID: final.ID, Version: final.Version, Name: final.Name, Title: final.Title, Enabled: true, Mode: "native", ChatIDs: []string{"a", "b"}, NativeOptions: &w.InvitationJoinWayOptions{AutoCreateRoom: true, RoomBaseName: "活动群", RoomBaseID: 10, State: "trial"}}
	if _, err = repo.SaveInvitationPlan(ctx, nativeInput, 1, "native-rollback-00001", "https://crm.example", invitationVirtualEffects{fail: true}); err == nil {
		t.Fatal("native acceptance rollback missing")
	}
	unchanged, readErr := repo.ReadInvitationPlan(ctx, final.ID)
	if readErr != nil || unchanged.Mode != "sequence" || unchanged.Version != final.Version {
		t.Fatal("native rollback changed plan", unchanged, readErr)
	}
	converted, err := repo.SaveInvitationPlan(ctx, nativeInput, 1, "native-convert-00001", "https://crm.example", effects)
	if err != nil || converted.Token != final.Token || converted.ProviderConfigID != final.ProviderConfigID || converted.ProviderState != "accepted" || converted.NativeOptions == nil {
		t.Fatalf("conversion failed %+v %v", converted, err)
	}
	if out := d.EvaluateInvitation(converted, nil, now); out.State != "preparing" {
		t.Fatal("old QR visible during native update", out)
	}
	runNative := func() {
		t.Helper()
		var source string
		if err := native.QueryRow(ctx, `SELECT source_digest FROM media_invitation_join_ways WHERE invite_id=$1`, final.ID).Scan(&source); err != nil {
			t.Fatal(err)
		}
		intent, err := repo.ReadInvitationPlanCodeIntent(ctx, source)
		if err != nil || intent.NativeOptions == nil {
			t.Fatal(intent, err)
		}
		env := p.NativeInvitationEnvelope(intent.InviteID, e.Digest(source), intent.ChatIDs, *intent.NativeOptions)
		attempt := e.Attempt{EffectID: intent.EffectID, Number: 1}
		result, err := adapter.Execute(ctx, env, attempt)
		if err != nil || result.Completion != e.StateExecuted {
			t.Fatal(result, err)
		}
		if err = uow.Within(ctx, func(tx context.Context) error { return sink.CompleteEffect(tx, intent.EffectID, env, attempt, result) }); err != nil {
			t.Fatal(err)
		}
	}
	// A different configuration cannot replace an unresolved native effect.
	nativeInput.Version = converted.Version
	changedOptions := *nativeInput.NativeOptions
	changedOptions.AutoCreateRoom = false
	nativeInput.NativeOptions = &changedOptions
	if _, err = repo.SaveInvitationPlan(ctx, nativeInput, 1, "native-unresolved-0001", "https://crm.example", effects); err == nil {
		t.Fatal("unresolved native intent overwritten")
	}
	runNative()
	nativePlan, err := repo.ReadInvitationPlan(ctx, final.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nativePlan.NativeOptions == nil || !nativePlan.NativeOptions.AutoCreateRoom || provider.creates != 1 || provider.updates != 2 {
		t.Fatal("native options not delivered", nativePlan, provider)
	}
	confirmedNative := d.EvaluateInvitation(nativePlan, map[string]g.CatalogGroup{"a": {MemberCount: 200, ObservedAt: &now}, "b": {MemberCount: 500, ObservedAt: &now}}, now)
	if confirmedNative.State != "active" || confirmedNative.CurrentChatID != "" {
		t.Fatal("native full groups hidden", confirmedNative)
	}
	if err = repo.ApplyInvitationEvaluationWithEffects(ctx, nativePlan, confirmedNative, effects); err != nil {
		t.Fatal(err)
	}
	nativePlan, err = repo.ReadInvitationPlan(ctx, final.ID)
	if err != nil {
		t.Fatal(err)
	}
	sameInput := nativeInput
	sameInput.NativeOptions = nativePlan.NativeOptions
	sameInput.Version = nativePlan.Version
	same, err := repo.SaveInvitationPlan(ctx, sameInput, 1, "native-unchanged-0001", "https://crm.example", effects)
	if err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM invitation_effect_probe`).Scan(&count); err != nil || count != 3 {
		t.Fatal("unchanged native config queued another write", count, err)
	}
	nativeInput.Version = same.Version
	updated, err := repo.SaveInvitationPlan(ctx, nativeInput, 1, "native-auto-off-00001", "https://crm.example", effects)
	if err != nil {
		t.Fatal(err)
	}
	runNative()
	updated, err = repo.ReadInvitationPlan(ctx, updated.ID)
	if err != nil || updated.Token != final.Token || updated.ProviderConfigID != provider.config || updated.ProviderQRCode != provider.qr || updated.NativeOptions.AutoCreateRoom || provider.nativeOptions.AutoCreateRoom || provider.creates != 1 || provider.updates != 3 {
		t.Fatal("native update failed", updated, err, provider)
	}
	service := &mediaApp.InvitationService{Store: repo, Catalog: invitationVirtualCatalog{}, Effects: effects, Origin: "https://crm.example", WriteEnabled: false}
	disabledInput := nativeInput
	disabledInput.Version = updated.Version
	disabledInput.NativeOptions = updated.NativeOptions
	for i, ids := range [][]string{{"a"}, {"b", "a"}} {
		disabledInput.ChatIDs = ids
		if _, err = service.Save(ctx, disabledInput, 1, fmt.Sprintf("native-disabled-change-%d", i)); err == nil {
			t.Fatal("disabled native writer accepted a Provider group-list mutation")
		}
	}
	disabledInput.ChatIDs = []string{"a", "b"}
	disabledInput.Name = "updated local metadata"
	metadata, err := service.Save(ctx, disabledInput, 1, "native-disabled-metadata-1")
	if err != nil || metadata.Name != disabledInput.Name || metadata.ProviderConfigID != updated.ProviderConfigID {
		t.Fatal("disabled writer must still allow local metadata", metadata, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM invitation_effect_probe`).Scan(&count); err != nil || count != 4 {
		t.Fatal("disabled native save queued a Provider effect", count, err)
	}
	t.Log("phase=native_disabled_writer group_target_mutations_denied=true local_metadata_allowed=true zero_extra_effects=true")
	t.Log("phase=native_accepted full_groups_delegated=true rollback=true unresolved_guard=true auto_on_off=true stable_link_and_qr=true")

}
