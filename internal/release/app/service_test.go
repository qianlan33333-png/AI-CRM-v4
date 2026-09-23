package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	releaseport "github.com/qianlan33333-png/AI-CRM-v3/internal/release/port"
)

func TestReleaseLifecycleReplayGenerationAndRollbackReconciliation(t *testing.T) {
	service, repository := newTestService()
	candidate := registerReadyCandidate(t, service, "a")
	detail, err := service.Detail(context.Background(), candidate.ID)
	if err != nil || !detail.Readiness.Ready || detail.ActiveWorker != nil {
		t.Fatalf("prepared detail=%#v err=%v", detail, err)
	}

	first, err := service.StartCutover(context.Background(), CandidateCommand{CandidateID: candidate.ID, ActorID: 7, IdempotencyKey: "release-start-key-0001"})
	if err != nil || first.Generation != 1 || !first.Active {
		t.Fatalf("first worker=%#v err=%v", first, err)
	}
	replayed, err := service.StartCutover(context.Background(), CandidateCommand{CandidateID: candidate.ID, ActorID: 7, IdempotencyKey: "release-start-key-0001"})
	if err != nil || replayed.Generation != first.Generation || replayed.Fence != first.Fence {
		t.Fatalf("start replay=%#v err=%v", replayed, err)
	}
	second, err := service.RestartCutover(context.Background(), WorkerCommand{
		CandidateID: candidate.ID, ActorID: 7, Generation: first.Generation, Fence: first.Fence,
		IdempotencyKey: "release-restart-key-01",
	})
	if err != nil || second.Generation != 2 || second.Fence == first.Fence {
		t.Fatalf("restarted worker=%#v err=%v", second, err)
	}
	detail, err = service.Detail(context.Background(), candidate.ID)
	if err != nil || detail.ActiveWorker == nil || detail.ActiveWorker.Generation != second.Generation {
		t.Fatalf("active detail=%#v err=%v", detail, err)
	}
	rawDetail, err := json.Marshal(detail)
	if err != nil || strings.Contains(string(rawDetail), second.Fence) || strings.Contains(string(rawDetail), `"Fence"`) {
		t.Fatalf("detail exposed worker fence: %s err=%v", rawDetail, err)
	}
	if _, err = service.CompleteStep(context.Background(), StepCommand{
		CandidateID: candidate.ID, ActorID: 7, Generation: first.Generation, Fence: first.Fence,
		Step: releaseport.CutoverAnnounce, IdempotencyKey: "release-stale-step-key",
	}); !errors.Is(err, ErrFence) {
		t.Fatalf("stale worker err=%v", err)
	}

	for index, step := range releaseport.FixedCutoverSteps {
		entry, stepErr := service.CompleteStep(context.Background(), StepCommand{
			CandidateID: candidate.ID, ActorID: 7, Generation: second.Generation, Fence: second.Fence,
			Step: step, IdempotencyKey: fmt.Sprintf("release-step-key-%04d", index),
		})
		if stepErr != nil || entry.Step != step || entry.Generation != second.Generation {
			t.Fatalf("step %s entry=%#v err=%v", step, entry, stepErr)
		}
	}
	activated, err := service.Activate(context.Background(), WorkerCommand{
		CandidateID: candidate.ID, ActorID: 7, Generation: second.Generation, Fence: second.Fence,
		IdempotencyKey: "release-activate-key-01",
	})
	if err != nil || activated.State != releaseport.CandidateActivated {
		t.Fatalf("activated=%#v err=%v", activated, err)
	}
	if repository.activeWorker() != nil {
		t.Fatalf("activation leaked global active worker: %#v", repository.activeWorker())
	}
	detail, err = service.Detail(context.Background(), candidate.ID)
	if err != nil || detail.ActiveWorker != nil || len(detail.CutoverProgress) != len(releaseport.FixedCutoverSteps) {
		t.Fatalf("activated detail=%#v err=%v", detail, err)
	}

	checks := []struct {
		kind   releaseport.RollbackCheckKind
		passed bool
		key    string
	}{
		{releaseport.RollbackSchemaCompatibility, true, "rollback-schema-key-01"},
		{releaseport.RollbackDataReconciliation, false, "rollback-data-blocked-1"},
		{releaseport.RollbackOutboundReconciliation, true, "rollback-outbound-key1"},
	}
	for _, check := range checks {
		if _, err = service.RecordRollbackCheck(context.Background(), RollbackCheckCommand{
			CandidateID: candidate.ID, ActorID: 7, Kind: check.kind, Passed: check.passed,
			EvidenceSHA: digest64("c"), IdempotencyKey: check.key,
		}); err != nil {
			t.Fatal(err)
		}
	}
	eligibility, err := service.RollbackEligibility(context.Background(), candidate.ID)
	if err != nil || eligibility.Eligible || len(eligibility.Blocked) != 1 || eligibility.Blocked[0] != releaseport.RollbackDataReconciliation {
		t.Fatalf("blocked eligibility=%#v err=%v", eligibility, err)
	}
	if _, err = service.RequestRollback(context.Background(), CandidateCommand{CandidateID: candidate.ID, ActorID: 7, IdempotencyKey: "rollback-request-not-ready"}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("not-ready rollback err=%v", err)
	}
	if _, err = service.RecordRollbackCheck(context.Background(), RollbackCheckCommand{
		CandidateID: candidate.ID, ActorID: 7, Kind: releaseport.RollbackDataReconciliation, Passed: true,
		EvidenceSHA: digest64("d"), IdempotencyKey: "rollback-data-passed-01",
	}); err != nil {
		t.Fatal(err)
	}
	requested, err := service.RequestRollback(context.Background(), CandidateCommand{CandidateID: candidate.ID, ActorID: 7, IdempotencyKey: "rollback-request-ready-01"})
	if err != nil || requested.State != releaseport.CandidateRollbackPending {
		t.Fatalf("rollback requested=%#v err=%v", requested, err)
	}
	if _, err = service.CompleteRollback(context.Background(), CandidateCommand{CandidateID: candidate.ID, ActorID: 7, IdempotencyKey: "rollback-complete-early"}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("early complete err=%v", err)
	}
	if _, err = service.RecordRollbackCheck(context.Background(), RollbackCheckCommand{
		CandidateID: candidate.ID, ActorID: 7, Kind: releaseport.RollbackExecutionReconciliation, Passed: true,
		EvidenceSHA: digest64("e"), IdempotencyKey: "rollback-post-verify-01",
	}); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := service.CompleteRollback(context.Background(), CandidateCommand{CandidateID: candidate.ID, ActorID: 7, IdempotencyKey: "rollback-complete-key-01"})
	if err != nil || rolledBack.State != releaseport.CandidateRolledBack {
		t.Fatalf("rolled back=%#v err=%v", rolledBack, err)
	}
}

func TestConcurrentStartAndStepHaveOneWinner(t *testing.T) {
	service, _ := newTestService()
	candidate := registerReadyCandidate(t, service, "b")

	type startResult struct {
		lease releaseport.WorkerLease
		err   error
	}
	starts := make(chan startResult, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			lease, err := service.StartCutover(context.Background(), CandidateCommand{
				CandidateID: candidate.ID, ActorID: 7,
				IdempotencyKey: fmt.Sprintf("concurrent-start-key-%02d", index),
			})
			starts <- startResult{lease: lease, err: err}
		}()
	}
	wait.Wait()
	close(starts)
	winners := make([]releaseport.WorkerLease, 0, 1)
	losers := 0
	for result := range starts {
		if result.err == nil {
			winners = append(winners, result.lease)
		} else if errors.Is(result.err, ErrInvalidState) {
			losers++
		} else {
			t.Fatalf("unexpected start err=%v", result.err)
		}
	}
	if len(winners) != 1 || losers != 1 {
		t.Fatalf("start winners=%d losers=%d", len(winners), losers)
	}
	active := winners[0]
	restarts := make(chan startResult, 2)
	for index := 0; index < 2; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			lease, err := service.RestartCutover(context.Background(), WorkerCommand{
				CandidateID: candidate.ID, ActorID: 7, Generation: active.Generation, Fence: active.Fence,
				IdempotencyKey: fmt.Sprintf("concurrent-restart-key-%02d", index),
			})
			restarts <- startResult{lease: lease, err: err}
		}()
	}
	wait.Wait()
	close(restarts)
	restartWinners, restartLosers := 0, 0
	for result := range restarts {
		if result.err == nil {
			restartWinners++
			active = result.lease
		} else if errors.Is(result.err, ErrFence) {
			restartLosers++
		} else {
			t.Fatalf("unexpected restart err=%v", result.err)
		}
	}
	if restartWinners != 1 || restartLosers != 1 || active.Generation != 2 {
		t.Fatalf("restart winners=%d losers=%d active=%#v", restartWinners, restartLosers, active)
	}

	steps := make(chan error, 2)
	for index := 0; index < 2; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.CompleteStep(context.Background(), StepCommand{
				CandidateID: candidate.ID, ActorID: 7, Generation: active.Generation, Fence: active.Fence,
				Step: releaseport.CutoverAnnounce, IdempotencyKey: fmt.Sprintf("concurrent-step-key-%02d", index),
			})
			steps <- err
		}()
	}
	wait.Wait()
	close(steps)
	stepWinners, stepLosers := 0, 0
	for err := range steps {
		if err == nil {
			stepWinners++
		} else if errors.Is(err, ErrInvalidState) {
			stepLosers++
		} else {
			t.Fatalf("unexpected step err=%v", err)
		}
	}
	if stepWinners != 1 || stepLosers != 1 {
		t.Fatalf("step winners=%d losers=%d", stepWinners, stepLosers)
	}
}

func TestReplayConflictAndReceiptLast(t *testing.T) {
	service, repository := newTestService()
	command := RegisterCommand{
		CommitSHA: digest40("1"), ArtifactDigest: digest64("2"), ManifestDigest: digest64("3"),
		ConfigDigest: digest64("4"), TargetSchemaVersion: 74, ActorID: 7,
		IdempotencyKey: "candidate-register-replay-key",
	}
	first, err := service.Register(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Register(context.Background(), command)
	if err != nil || second.ID != first.ID || len(repository.candidates) != 1 {
		t.Fatalf("replay=%#v err=%v candidates=%d", second, err, len(repository.candidates))
	}
	command.ArtifactDigest = digest64("9")
	if _, err = service.Register(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("tampered replay err=%v", err)
	}
	command.IdempotencyKey = "candidate-register-same-sha-new-key"
	if _, err = service.Register(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("same commit with different artifact err=%v", err)
	}
	if got := repository.sequence[len(repository.sequence)-2:]; got[0] != "candidate.create" || got[1] != "receipt.complete" {
		t.Fatalf("receipt was not last: %v", got)
	}
}

func TestPrerequisiteReceiptBindsExactCandidateAndCannotBeReplaced(t *testing.T) {
	service, repository := newTestService()
	candidate, err := service.Register(context.Background(), RegisterCommand{
		CommitSHA: digest40("subject"), ArtifactDigest: digest64("artifact"), ManifestDigest: digest64("manifest"),
		ConfigDigest: digest64("config"), TargetSchemaVersion: 74, ActorID: 7,
		IdempotencyKey: "candidate-subject-register-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := ReceiptCommand{
		CandidateID: candidate.ID, ActorID: 7, Kind: releaseport.PrerequisiteNightly,
		EvidenceSHA: digest64("nightly-evidence"), IdempotencyKey: "candidate-nightly-receipt-key",
	}
	receipt, err := service.RecordPrerequisite(context.Background(), command)
	if err != nil || !prerequisiteSubjectMatches(candidate, receipt) {
		t.Fatalf("receipt=%#v err=%v", receipt, err)
	}
	command.EvidenceSHA = digest64("replacement")
	command.IdempotencyKey = "candidate-nightly-replace-key"
	if _, err = service.RecordPrerequisite(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("replacement prerequisite err=%v", err)
	}
	repository.prerequisites[0].CandidateConfigDigest = digest64("tampered-config")
	ready, err := service.Readiness(context.Background(), candidate.ID)
	if err != nil || ready.Ready || len(ready.Invalid) != 1 || ready.Invalid[0] != releaseport.PrerequisiteNightly {
		t.Fatalf("tampered subject readiness=%#v err=%v", ready, err)
	}
}

func registerReadyCandidate(t *testing.T, service *Service, suffix string) releaseport.Candidate {
	t.Helper()
	candidate, err := service.Register(context.Background(), RegisterCommand{
		CommitSHA: digest40(suffix), ArtifactDigest: digest64("a" + suffix), ManifestDigest: digest64("b" + suffix),
		ConfigDigest: digest64("c" + suffix), TargetSchemaVersion: 74, ActorID: 7,
		IdempotencyKey: "candidate-register-key-" + suffix + "-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, kind := range requiredPrerequisites {
		if _, err = service.RecordPrerequisite(context.Background(), ReceiptCommand{
			CandidateID: candidate.ID, ActorID: 7, Kind: kind, EvidenceSHA: digest64(fmt.Sprintf("%s-%d", suffix, index)),
			IdempotencyKey: fmt.Sprintf("prerequisite-%s-key-%03d", suffix, index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ready, err := service.Readiness(context.Background(), candidate.ID)
	if err != nil || !ready.Ready || len(ready.Missing) != 0 {
		t.Fatalf("readiness=%#v err=%v", ready, err)
	}
	prepared, err := service.PrepareCandidate(context.Background(), CandidateCommand{CandidateID: candidate.ID, ActorID: 7, IdempotencyKey: "candidate-prepare-key-" + suffix})
	if err != nil || prepared.State != releaseport.CandidatePrepared {
		t.Fatalf("prepared=%#v err=%v", prepared, err)
	}
	return prepared
}

func digest40(seed string) string { return fmt.Sprintf("%040x", []byte(seed))[:40] }
func digest64(seed string) string { return fmt.Sprintf("%064x", []byte(seed))[:64] }

type testUOW struct{ mutex sync.Mutex }

func (uow *testUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	uow.mutex.Lock()
	defer uow.mutex.Unlock()
	return callback(ctx)
}

type testRepository struct {
	candidates    map[int64]releaseport.Candidate
	prerequisites []releaseport.PrerequisiteReceipt
	workers       []releaseport.WorkerLease
	journal       []releaseport.CutoverJournalEntry
	rollback      []releaseport.RollbackCheck
	receipts      map[string]releaseport.OperationReceipt
	sequence      []string
	nextCandidate int64
	nextReceipt   int64
}

func newTestService() (*Service, *testRepository) {
	repository := &testRepository{candidates: map[int64]releaseport.Candidate{}, receipts: map[string]releaseport.OperationReceipt{}, nextCandidate: 1, nextReceipt: 1}
	service := NewService(&testUOW{}, repository)
	clock := time.Date(2026, time.August, 25, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	fence := 0
	var fenceMutex sync.Mutex
	service.newFence = func() (string, error) {
		fenceMutex.Lock()
		defer fenceMutex.Unlock()
		fence++
		return fmt.Sprintf("%064x", fence), nil
	}
	return service, repository
}

func (repository *testRepository) CreateCandidate(_ context.Context, value releaseport.Candidate) (releaseport.Candidate, error) {
	for _, current := range repository.candidates {
		if current.CommitSHA == value.CommitSHA {
			return releaseport.Candidate{}, releaseport.ErrConflict
		}
	}
	value.ID = repository.nextCandidate
	repository.nextCandidate++
	repository.candidates[value.ID] = value
	repository.sequence = append(repository.sequence, "candidate.create")
	return value, nil
}

func (repository *testRepository) GetCandidate(_ context.Context, id int64) (releaseport.Candidate, error) {
	value, ok := repository.candidates[id]
	if !ok {
		return releaseport.Candidate{}, releaseport.ErrNotFound
	}
	return value, nil
}

func (repository *testRepository) LockCandidate(ctx context.Context, id int64) (releaseport.Candidate, error) {
	return repository.GetCandidate(ctx, id)
}

func (repository *testRepository) ListCandidates(context.Context, int32) ([]releaseport.Candidate, error) {
	values := make([]releaseport.Candidate, 0, len(repository.candidates))
	for _, value := range repository.candidates {
		values = append(values, value)
	}
	return values, nil
}

func (repository *testRepository) TransitionCandidate(_ context.Context, id int64, from, to releaseport.CandidateState, now time.Time) (releaseport.Candidate, error) {
	value, ok := repository.candidates[id]
	if !ok {
		return releaseport.Candidate{}, releaseport.ErrNotFound
	}
	if value.State != from {
		return releaseport.Candidate{}, releaseport.ErrConflict
	}
	value.State = to
	switch to {
	case releaseport.CandidatePrepared:
		value.PreparedAt = &now
	case releaseport.CandidateActivated:
		value.ActivatedAt = &now
	case releaseport.CandidateRollbackPending:
		value.RollbackRequestedAt = &now
	case releaseport.CandidateRolledBack:
		value.RolledBackAt = &now
	}
	repository.candidates[id] = value
	return value, nil
}

func (repository *testRepository) CreatePrerequisite(_ context.Context, value releaseport.PrerequisiteReceipt) (releaseport.PrerequisiteReceipt, error) {
	for _, existing := range repository.prerequisites {
		if existing.CandidateID == value.CandidateID && existing.Kind == value.Kind {
			return releaseport.PrerequisiteReceipt{}, releaseport.ErrConflict
		}
	}
	value.ID = int64(len(repository.prerequisites) + 1)
	repository.prerequisites = append(repository.prerequisites, value)
	return value, nil
}

func (repository *testRepository) ListPrerequisites(_ context.Context, id int64) ([]releaseport.PrerequisiteReceipt, error) {
	values := make([]releaseport.PrerequisiteReceipt, 0)
	for _, value := range repository.prerequisites {
		if value.CandidateID == id {
			values = append(values, value)
		}
	}
	return values, nil
}

func (repository *testRepository) StartWorker(_ context.Context, value releaseport.WorkerLease) (releaseport.WorkerLease, error) {
	if repository.activeWorker() != nil {
		return releaseport.WorkerLease{}, releaseport.ErrConflict
	}
	for _, worker := range repository.workers {
		if worker.CandidateID == value.CandidateID && worker.Generation >= value.Generation {
			value.Generation = worker.Generation + 1
		}
	}
	if value.Generation == 0 {
		value.Generation = 1
	}
	repository.workers = append(repository.workers, value)
	return value, nil
}

func (repository *testRepository) GetActiveWorker(_ context.Context, id int64) (releaseport.WorkerLease, error) {
	for _, worker := range repository.workers {
		if worker.CandidateID == id && worker.Active {
			return worker, nil
		}
	}
	return releaseport.WorkerLease{}, releaseport.ErrNotFound
}

func (repository *testRepository) FindActiveWorkerSummary(_ context.Context, id int64) (*releaseport.WorkerSummary, error) {
	worker, err := repository.GetActiveWorker(context.Background(), id)
	if errors.Is(err, releaseport.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &releaseport.WorkerSummary{
		CandidateID: worker.CandidateID, Generation: worker.Generation,
		StartedBy: worker.StartedBy, StartedAt: worker.StartedAt,
	}, nil
}

func (repository *testRepository) RetireWorker(_ context.Context, id, generation int64, fence string, now time.Time) error {
	for index := range repository.workers {
		worker := &repository.workers[index]
		if worker.CandidateID == id && worker.Generation == generation && worker.Fence == fence && worker.Active {
			worker.Active = false
			worker.RetiredAt = &now
			return nil
		}
	}
	return releaseport.ErrConflict
}

func (repository *testRepository) activeWorker() *releaseport.WorkerLease {
	for index := range repository.workers {
		if repository.workers[index].Active {
			return &repository.workers[index]
		}
	}
	return nil
}

func (repository *testRepository) AppendCutoverStep(_ context.Context, value releaseport.CutoverJournalEntry) (releaseport.CutoverJournalEntry, error) {
	value.ID = int64(len(repository.journal) + 1)
	repository.journal = append(repository.journal, value)
	return value, nil
}

func (repository *testRepository) ListCutoverSteps(_ context.Context, id int64) ([]releaseport.CutoverJournalEntry, error) {
	values := make([]releaseport.CutoverJournalEntry, 0)
	for _, value := range repository.journal {
		if value.CandidateID == id {
			values = append(values, value)
		}
	}
	return values, nil
}

func (repository *testRepository) CreateRollbackCheck(_ context.Context, value releaseport.RollbackCheck) (releaseport.RollbackCheck, error) {
	value.ID = int64(len(repository.rollback) + 1)
	repository.rollback = append(repository.rollback, value)
	return value, nil
}

func (repository *testRepository) ListRollbackChecks(_ context.Context, id int64) ([]releaseport.RollbackCheck, error) {
	values := make([]releaseport.RollbackCheck, 0)
	for _, value := range repository.rollback {
		if value.CandidateID == id {
			values = append(values, value)
		}
	}
	return values, nil
}

func receiptKey(value releaseport.OperationReceipt) string {
	return fmt.Sprintf("%s/%d/%s", value.Action, value.ActorID, value.KeyDigest)
}

func (repository *testRepository) ReserveOperationReceipt(_ context.Context, value releaseport.OperationReceipt) (releaseport.OperationReceipt, bool, error) {
	key := receiptKey(value)
	if existing, ok := repository.receipts[key]; ok {
		return existing, false, nil
	}
	value.ID = repository.nextReceipt
	repository.nextReceipt++
	repository.receipts[key] = value
	return value, true, nil
}

func (repository *testRepository) CompleteOperationReceipt(_ context.Context, id int64, result json.RawMessage, now time.Time) (releaseport.OperationReceipt, error) {
	for key, value := range repository.receipts {
		if value.ID == id && value.State == "in_progress" {
			value.State = "completed"
			value.Result = append(json.RawMessage{}, result...)
			value.CompletedAt = &now
			repository.receipts[key] = value
			repository.sequence = append(repository.sequence, "receipt.complete")
			return value, nil
		}
	}
	return releaseport.OperationReceipt{}, releaseport.ErrConflict
}
