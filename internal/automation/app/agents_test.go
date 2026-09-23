package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

type agentTestTxKey struct{}

type agentTestUOW struct{}

func (agentTestUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(context.WithValue(ctx, agentTestTxKey{}, true))
}

type agentTestEvents struct {
	rows []automationport.Event
}

func (e *agentTestEvents) Append(ctx context.Context, event automationport.Event) (automationport.EventID, error) {
	if in, _ := ctx.Value(agentTestTxKey{}).(bool); !in {
		return 0, errors.New("event escaped automation transaction")
	}
	e.rows = append(e.rows, event)
	return automationport.EventID(len(e.rows)), nil
}

type agentTestStore struct {
	agents   map[automationport.AgentID]automationport.Agent
	receipts map[string]Receipt
	nextID   automationport.AgentID
	updates  int
}

func newAgentTestStore(items ...automationport.Agent) *agentTestStore {
	store := &agentTestStore{agents: map[automationport.AgentID]automationport.Agent{}, receipts: map[string]Receipt{}, nextID: 100}
	for _, item := range items {
		store.agents[item.ID] = item
		if item.ID >= store.nextID {
			store.nextID = item.ID + 1
		}
	}
	return store
}

func (s *agentTestStore) List(_ context.Context, kind automationport.AutomationType) ([]automationport.Agent, error) {
	items := make([]automationport.Agent, 0, len(s.agents))
	for _, item := range s.agents {
		if kind == "" || item.AutomationType == kind {
			if item.Status != automationport.AgentStatusArchived {
				items = append(items, item)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *agentTestStore) Get(_ context.Context, id automationport.AgentID) (automationport.Agent, error) {
	item, ok := s.agents[id]
	if !ok || item.Status == automationport.AgentStatusArchived {
		return automationport.Agent{}, ErrAgentNotFound
	}
	return item, nil
}

func (s *agentTestStore) Lock(ctx context.Context, id automationport.AgentID) (automationport.Agent, error) {
	return s.Get(ctx, id)
}

func (s *agentTestStore) Create(_ context.Context, item automationport.Agent, now time.Time) (automationport.Agent, error) {
	item.ID = s.nextID
	s.nextID++
	item.CreatedAt, item.UpdatedAt = now, now
	s.agents[item.ID] = item
	return item, nil
}

func (s *agentTestStore) Update(_ context.Context, item automationport.Agent, now time.Time) (automationport.Agent, error) {
	s.updates++
	old, err := s.Get(context.Background(), item.ID)
	if err != nil {
		return automationport.Agent{}, err
	}
	item.CreatedAt, item.CreatedBy = old.CreatedAt, old.CreatedBy
	item.UpdatedAt = now
	s.agents[item.ID] = item
	return item, nil
}

func (s *agentTestStore) NextCopyCode(_ context.Context, source string) (string, error) {
	return source + "_copy_001", nil
}

func agentReceiptKey(value Reservation) string {
	return value.Operation + "\x00" + value.ActorScope + "\x00" + fmt.Sprintf("%x", value.KeyDigest)
}

func (s *agentTestStore) Reserve(_ context.Context, value Reservation) (Receipt, bool, error) {
	key := agentReceiptKey(value)
	if row, ok := s.receipts[key]; ok {
		return row, false, nil
	}
	row := Receipt{ID: int64(len(s.receipts) + 1), Operation: value.Operation, ActorScope: value.ActorScope, KeyDigest: value.KeyDigest, PayloadDigest: value.PayloadDigest, State: "in_progress"}
	s.receipts[key] = row
	return row, true, nil
}

func (s *agentTestStore) Complete(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (Receipt, error) {
	for key, row := range s.receipts {
		if row.ID != id || row.State != "in_progress" {
			continue
		}
		row.State = "completed"
		row.ResultSnapshot = append(json.RawMessage(nil), snapshot...)
		s.receipts[key] = row
		return row, nil
	}
	return Receipt{}, ErrAgentUnavailable
}

func agentTestService(now time.Time, store *agentTestStore, events *agentTestEvents) *Service {
	service := NewAgentService(agentTestUOW{}, store, events)
	service.now = func() time.Time { return now }
	return service
}

func TestAgentConfigurationLifecycleAndIntentBoundary(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	store, events := newAgentTestStore(), &agentTestEvents{}
	service := agentTestService(now, store, events)
	created, err := service.Create(context.Background(), automationport.CreateCommand{
		Actor:          7,
		IdempotencyKey: "agent-create-key-0001",
		Agent: automationport.Agent{
			AgentName:       "新客助手",
			AgentCode:       "new_customer_assistant",
			AutomationType:  automationport.AutomationTypeAgent,
			Status:          automationport.AgentStatusPaused,
			DraftRolePrompt: "保持准确、克制和友好。",
			DraftTaskPrompt: "根据已批准的上下文生成待审核建议。",
		},
	})
	if err != nil || created.ID != 100 || created.Status != automationport.AgentStatusPaused || created.ExecutionEnabled || created.DraftVersion != 1 || created.PublishedVersion != 1 || len(events.rows) != 1 || events.rows[0].Type != automationport.EventAgentCreated {
		t.Fatalf("created=%+v err=%v events=%+v", created, err, events.rows)
	}

	name, role := "新客助手 v2", "保持准确、克制、友好，不越权。"
	updated, err := service.Update(context.Background(), automationport.UpdateCommand{
		ID: created.ID, AgentName: &name, RolePrompt: &role, Actor: 7, IdempotencyKey: "agent-update-key-0001",
	})
	if err != nil || updated.AgentName != name || updated.DraftRolePrompt != role || updated.DraftVersion != 2 || updated.PublishedVersion != 1 || len(events.rows) != 2 || events.rows[1].Type != automationport.EventAgentUpdated {
		t.Fatalf("updated=%+v err=%v events=%+v", updated, err, events.rows)
	}

	published, err := service.Publish(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "agent-publish-key-0001"})
	if err != nil || published.PublishedRolePrompt != role || published.PublishedVersion != 2 || len(events.rows) != 3 || events.rows[2].Type != automationport.EventAgentPublished {
		t.Fatalf("published=%+v err=%v events=%+v", published, err, events.rows)
	}

	active, err := service.SetStatus(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "agent-activate-key-01"}, automationport.AgentStatusActive)
	if err != nil || active.Status != automationport.AgentStatusActive || !active.ExecutionEnabled || len(events.rows) != 4 || events.rows[3].Type != automationport.EventAgentStatusChanged {
		t.Fatalf("active=%+v err=%v events=%+v", active, err, events.rows)
	}
	paused, err := service.SetStatus(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "agent-pause-key-000001"}, automationport.AgentStatusPaused)
	if err != nil || paused.Status != automationport.AgentStatusPaused || paused.ExecutionEnabled || len(events.rows) != 5 || events.rows[4].Type != automationport.EventAgentStatusChanged {
		t.Fatalf("paused=%+v err=%v events=%+v", paused, err, events.rows)
	}
	archived, err := service.SetStatus(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "agent-archive-key-01"}, automationport.AgentStatusArchived)
	if err != nil || archived.Status != automationport.AgentStatusArchived || archived.ExecutionEnabled || len(events.rows) != 6 || events.rows[5].Type != automationport.EventAgentStatusChanged {
		t.Fatalf("archived=%+v err=%v events=%+v", archived, err, events.rows)
	}
	if _, err = service.Get(context.Background(), created.ID); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("archived Get error=%v", err)
	}
}

func TestFixedScriptContentAndCopyReplay(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	store, events := newAgentTestStore(), &agentTestEvents{}
	service := agentTestService(now, store, events)
	created, err := service.Create(context.Background(), automationport.CreateCommand{
		Actor:          9,
		IdempotencyKey: "fixed-create-key-0001",
		Agent: automationport.Agent{
			AgentName:           "欢迎固定话术",
			AgentCode:           "welcome_fixed_script",
			AutomationType:      automationport.AutomationTypeFixedScript,
			Status:              automationport.AgentStatusPaused,
			FixedContentPackage: automationport.FixedContentPackage{ContentText: "欢迎加入，我们会在工作时间回复。"},
		},
	})
	if err != nil || created.ID != 100 || created.FixedContentPackage.ContentText == "" || len(events.rows) != 1 {
		t.Fatalf("created=%+v err=%v events=%+v", created, err, events.rows)
	}

	content := automationport.FixedContentPackage{ContentText: "欢迎加入，稍后会有专人回复。"}
	updated, err := service.SaveFixedContent(context.Background(), automationport.FixedContentCommand{ID: created.ID, ContentPackage: content, Actor: 9, IdempotencyKey: "fixed-content-key-01"})
	if err != nil || updated.FixedContentPackage.ContentText != content.ContentText || updated.DraftVersion != 2 || updated.PublishedVersion != 1 || len(events.rows) != 2 || events.rows[1].Type != automationport.EventFixedContentUpdated {
		t.Fatalf("content update=%+v err=%v events=%+v", updated, err, events.rows)
	}

	copyKey := "fixed-copy-key-0001"
	copied, err := service.Copy(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 9, IdempotencyKey: copyKey})
	if err != nil || copied.ID == created.ID || copied.AgentCode != "welcome_fixed_script_copy_001" || copied.FixedContentPackage.ContentText != content.ContentText || len(events.rows) != 3 || events.rows[2].Type != automationport.EventAgentCopied {
		t.Fatalf("copied=%+v err=%v events=%+v", copied, err, events.rows)
	}
	replayed, err := service.Copy(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 9, IdempotencyKey: copyKey})
	if err != nil || replayed.ID != copied.ID || replayed.AgentCode != copied.AgentCode || len(events.rows) != 3 {
		t.Fatalf("copy replay=%+v err=%v events=%+v", replayed, err, events.rows)
	}
}

func TestActiveFixedContentRequiresPausePublishAndReactivate(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	store, events := newAgentTestStore(), &agentTestEvents{}
	service := agentTestService(now, store, events)
	created, err := service.Create(context.Background(), automationport.CreateCommand{
		Actor:          9,
		IdempotencyKey: "fixed-active-create-01",
		Agent: automationport.Agent{
			AgentName:           "激活中的固定话术",
			AgentCode:           "active_fixed_script",
			AutomationType:      automationport.AutomationTypeFixedScript,
			Status:              automationport.AgentStatusPaused,
			DraftRolePrompt:     "保持准确。",
			DraftTaskPrompt:     "回复欢迎语。",
			FixedContentPackage: automationport.FixedContentPackage{ContentText: "欢迎加入。"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := service.SetStatus(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 9, IdempotencyKey: "fixed-active-enable-01"}, automationport.AgentStatusActive)
	if err != nil || active.Status != automationport.AgentStatusActive || !active.ExecutionEnabled {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	changed := automationport.FixedContentPackage{ContentText: "欢迎加入，稍后回复。"}
	if _, err = service.SaveFixedContent(context.Background(), automationport.FixedContentCommand{ID: created.ID, ContentPackage: changed, Actor: 9, IdempotencyKey: "fixed-active-save-0001"}); !errors.Is(err, ErrAgentConflict) {
		t.Fatalf("active save err=%v, want conflict", err)
	}
	current, err := service.Get(context.Background(), created.ID)
	if err != nil || current.FixedContentPackage.ContentText != "欢迎加入。" || current.DraftVersion != 1 || current.PublishedVersion != 1 || len(events.rows) != 2 {
		t.Fatalf("active record mutated=%+v err=%v events=%+v", current, err, events.rows)
	}
	paused, err := service.SetStatus(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 9, IdempotencyKey: "fixed-active-pause-0001"}, automationport.AgentStatusPaused)
	if err != nil || paused.ExecutionEnabled {
		t.Fatalf("paused=%+v err=%v", paused, err)
	}
	saved, err := service.SaveFixedContent(context.Background(), automationport.FixedContentCommand{ID: created.ID, ContentPackage: changed, Actor: 9, IdempotencyKey: "fixed-paused-save-0001"})
	if err != nil || saved.DraftVersion != 2 || saved.PublishedVersion != 1 || saved.FixedContentPackage.ContentText != changed.ContentText {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	published, err := service.Publish(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 9, IdempotencyKey: "fixed-active-publish-01"})
	if err != nil || published.DraftVersion != published.PublishedVersion {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	reactivated, err := service.SetStatus(context.Background(), automationport.MutationCommand{ID: created.ID, Actor: 9, IdempotencyKey: "fixed-active-enable-02"}, automationport.AgentStatusActive)
	if err != nil || reactivated.Status != automationport.AgentStatusActive || !reactivated.ExecutionEnabled {
		t.Fatalf("reactivated=%+v err=%v", reactivated, err)
	}
}
