package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	releaseport "github.com/qianlan33333-png/AI-CRM-v3/internal/release/port"
)

var (
	ErrInvalidCommand = errors.New("invalid release command")
	ErrNotReady       = errors.New("release candidate is not ready")
	ErrInvalidState   = errors.New("invalid release state transition")
	ErrConflict       = releaseport.ErrConflict
	ErrFence          = errors.New("release worker fence rejected")
	ErrUnavailable    = releaseport.ErrUnavailable
)

type Service struct {
	uow      platformport.UnitOfWork
	store    releaseport.Repository
	now      func() time.Time
	newFence func() (string, error)
}

func NewService(uow platformport.UnitOfWork, store releaseport.Repository) *Service {
	return &Service{uow: uow, store: store, now: time.Now, newFence: randomFence}
}

type RegisterCommand struct {
	CommitSHA           string
	ArtifactDigest      string
	ManifestDigest      string
	ConfigDigest        string
	TargetSchemaVersion int64
	ActorID             int64
	IdempotencyKey      string
}

type ReceiptCommand struct {
	CandidateID    int64
	ActorID        int64
	Kind           releaseport.PrerequisiteKind
	EvidenceSHA    string
	IdempotencyKey string
}

type CandidateCommand struct {
	CandidateID    int64
	ActorID        int64
	IdempotencyKey string
}

type WorkerCommand struct {
	CandidateID    int64
	ActorID        int64
	Generation     int64
	Fence          string
	IdempotencyKey string
}

type StepCommand struct {
	CandidateID    int64
	ActorID        int64
	Generation     int64
	Fence          string
	Step           releaseport.CutoverStep
	IdempotencyKey string
}

type RollbackCheckCommand struct {
	CandidateID    int64
	ActorID        int64
	Kind           releaseport.RollbackCheckKind
	Passed         bool
	EvidenceSHA    string
	IdempotencyKey string
}

func (s *Service) Register(ctx context.Context, command RegisterCommand) (releaseport.Candidate, error) {
	if !validRegister(command) {
		return releaseport.Candidate{}, ErrInvalidCommand
	}
	return mutate(s, ctx, "candidate.register", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time) (releaseport.Candidate, error) {
		return s.store.CreateCandidate(tx, releaseport.Candidate{
			CommitSHA: command.CommitSHA, ArtifactDigest: command.ArtifactDigest,
			ManifestDigest: command.ManifestDigest, ConfigDigest: command.ConfigDigest,
			TargetSchemaVersion: command.TargetSchemaVersion, State: releaseport.CandidateDraft,
			CreatedBy: command.ActorID, CreatedAt: now,
		})
	})
}

func (s *Service) Get(ctx context.Context, candidateID int64) (releaseport.Candidate, error) {
	if s == nil || s.store == nil || candidateID < 1 {
		return releaseport.Candidate{}, ErrInvalidCommand
	}
	return s.store.GetCandidate(ctx, candidateID)
}

// Detail returns one app-owned progress snapshot. LockCandidate serializes it
// with every mutation before the related append-only facts are read. Fence is
// intentionally projected out of both journal and active-worker summaries.
func (s *Service) Detail(ctx context.Context, candidateID int64) (releaseport.Detail, error) {
	if s == nil || s.uow == nil || s.store == nil || candidateID < 1 {
		return releaseport.Detail{}, ErrInvalidCommand
	}
	var result releaseport.Detail
	err := s.uow.Within(ctx, func(tx context.Context) error {
		candidate, err := s.store.LockCandidate(tx, candidateID)
		if err != nil {
			return err
		}
		prerequisites, err := s.store.ListPrerequisites(tx, candidateID)
		if err != nil {
			return err
		}
		journal, err := s.store.ListCutoverSteps(tx, candidateID)
		if err != nil {
			return err
		}
		rollbackChecks, err := s.store.ListRollbackChecks(tx, candidateID)
		if err != nil {
			return err
		}
		activeWorker, err := s.store.FindActiveWorkerSummary(tx, candidateID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		result = releaseport.Detail{
			Candidate: candidate, Prerequisites: prerequisites,
			Readiness:       readiness(candidate, prerequisites, now),
			CutoverProgress: cutoverProgress(journal), RollbackChecks: rollbackChecks,
			RollbackEligibility: rollbackEligibility(candidateID, rollbackChecks, now),
			ActiveWorker:        activeWorker,
		}
		return nil
	})
	if err != nil {
		return releaseport.Detail{}, err
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, limit int32) ([]releaseport.Candidate, error) {
	if s == nil || s.store == nil || limit < 1 || limit > 100 {
		return nil, ErrInvalidCommand
	}
	return s.store.ListCandidates(ctx, limit)
}

func (s *Service) RecordPrerequisite(ctx context.Context, command ReceiptCommand) (releaseport.PrerequisiteReceipt, error) {
	if command.CandidateID < 1 || command.ActorID < 1 || !knownPrerequisite(command.Kind) || !hexDigest(command.EvidenceSHA) || !validKey(command.IdempotencyKey) {
		return releaseport.PrerequisiteReceipt{}, ErrInvalidCommand
	}
	return mutate(s, ctx, "prerequisite.record", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time) (releaseport.PrerequisiteReceipt, error) {
		candidate, err := s.store.LockCandidate(tx, command.CandidateID)
		if err != nil {
			return releaseport.PrerequisiteReceipt{}, err
		}
		if candidate.State != releaseport.CandidateDraft {
			return releaseport.PrerequisiteReceipt{}, ErrInvalidState
		}
		return s.store.CreatePrerequisite(tx, releaseport.PrerequisiteReceipt{
			CandidateID:        command.CandidateID,
			CandidateCommitSHA: candidate.CommitSHA, CandidateArtifactDigest: candidate.ArtifactDigest,
			CandidateManifestDigest: candidate.ManifestDigest, CandidateConfigDigest: candidate.ConfigDigest,
			CandidateSchemaVersion: candidate.TargetSchemaVersion,
			Kind:                   command.Kind, EvidenceSHA: command.EvidenceSHA, RecordedBy: command.ActorID, RecordedAt: now,
		})
	})
}

func (s *Service) Readiness(ctx context.Context, candidateID int64) (releaseport.Readiness, error) {
	if s == nil || s.store == nil || candidateID < 1 {
		return releaseport.Readiness{}, ErrInvalidCommand
	}
	candidate, err := s.store.GetCandidate(ctx, candidateID)
	if err != nil {
		return releaseport.Readiness{}, err
	}
	receipts, err := s.store.ListPrerequisites(ctx, candidateID)
	if err != nil {
		return releaseport.Readiness{}, err
	}
	return readiness(candidate, receipts, s.now().UTC()), nil
}

func (s *Service) PrepareCandidate(ctx context.Context, command CandidateCommand) (releaseport.Candidate, error) {
	if !validCandidateCommand(command) {
		return releaseport.Candidate{}, ErrInvalidCommand
	}
	return mutate(s, ctx, "candidate.prepare", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time) (releaseport.Candidate, error) {
		candidate, err := s.store.LockCandidate(tx, command.CandidateID)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if candidate.State != releaseport.CandidateDraft {
			return releaseport.Candidate{}, ErrInvalidState
		}
		receipts, err := s.store.ListPrerequisites(tx, command.CandidateID)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if !readiness(candidate, receipts, now).Ready {
			return releaseport.Candidate{}, ErrNotReady
		}
		return s.store.TransitionCandidate(tx, command.CandidateID, releaseport.CandidateDraft, releaseport.CandidatePrepared, now)
	})
}

func (s *Service) StartCutover(ctx context.Context, command CandidateCommand) (releaseport.WorkerLease, error) {
	if !validCandidateCommand(command) {
		return releaseport.WorkerLease{}, ErrInvalidCommand
	}
	return s.startWorker(ctx, "cutover.start", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time, fence string) (releaseport.WorkerLease, error) {
		candidate, err := s.store.LockCandidate(tx, command.CandidateID)
		if err != nil {
			return releaseport.WorkerLease{}, err
		}
		if candidate.State != releaseport.CandidatePrepared {
			return releaseport.WorkerLease{}, ErrInvalidState
		}
		lease, err := s.store.StartWorker(tx, releaseport.WorkerLease{
			CandidateID: command.CandidateID, Fence: fence, StartedBy: command.ActorID,
			StartedAt: now, Active: true,
		})
		if err != nil {
			return releaseport.WorkerLease{}, err
		}
		if _, err = s.store.TransitionCandidate(tx, command.CandidateID, releaseport.CandidatePrepared, releaseport.CandidateCutoverActive, now); err != nil {
			return releaseport.WorkerLease{}, err
		}
		return lease, nil
	})
}

// RestartCutover explicitly rotates a live generation. It preserves completed
// journal entries while fencing the previous worker, so a crashed worker does
// not make the release plane permanently unusable.
func (s *Service) RestartCutover(ctx context.Context, command WorkerCommand) (releaseport.WorkerLease, error) {
	if !validWorkerCommand(command) {
		return releaseport.WorkerLease{}, ErrInvalidCommand
	}
	return s.startWorker(ctx, "cutover.restart", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time, fence string) (releaseport.WorkerLease, error) {
		candidate, err := s.store.LockCandidate(tx, command.CandidateID)
		if err != nil {
			return releaseport.WorkerLease{}, err
		}
		if candidate.State != releaseport.CandidateCutoverActive {
			return releaseport.WorkerLease{}, ErrInvalidState
		}
		active, err := s.store.GetActiveWorker(tx, command.CandidateID)
		if err != nil {
			return releaseport.WorkerLease{}, err
		}
		if active.Generation != command.Generation || active.Fence != command.Fence {
			return releaseport.WorkerLease{}, ErrFence
		}
		if err = s.store.RetireWorker(tx, command.CandidateID, command.Generation, command.Fence, now); err != nil {
			return releaseport.WorkerLease{}, err
		}
		return s.store.StartWorker(tx, releaseport.WorkerLease{
			CandidateID: command.CandidateID, Fence: fence, StartedBy: command.ActorID,
			StartedAt: now, Active: true,
		})
	})
}

func (s *Service) startWorker(ctx context.Context, action string, actorID int64, key string, payload any, callback func(context.Context, time.Time, string) (releaseport.WorkerLease, error)) (releaseport.WorkerLease, error) {
	if s == nil || s.newFence == nil || callback == nil {
		return releaseport.WorkerLease{}, ErrInvalidCommand
	}
	fence, err := s.newFence()
	if err != nil || !validFence(fence) {
		return releaseport.WorkerLease{}, ErrUnavailable
	}
	return mutate(s, ctx, action, actorID, key, payload, func(tx context.Context, now time.Time) (releaseport.WorkerLease, error) {
		return callback(tx, now, fence)
	})
}

func (s *Service) CompleteStep(ctx context.Context, command StepCommand) (releaseport.CutoverJournalEntry, error) {
	if command.CandidateID < 1 || command.ActorID < 1 || command.Generation < 1 || !knownStep(command.Step) || !validFence(command.Fence) || !validKey(command.IdempotencyKey) {
		return releaseport.CutoverJournalEntry{}, ErrInvalidCommand
	}
	return mutate(s, ctx, "cutover.step.complete", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time) (releaseport.CutoverJournalEntry, error) {
		candidate, err := s.store.LockCandidate(tx, command.CandidateID)
		if err != nil {
			return releaseport.CutoverJournalEntry{}, err
		}
		if candidate.State != releaseport.CandidateCutoverActive {
			return releaseport.CutoverJournalEntry{}, ErrInvalidState
		}
		lease, err := s.store.GetActiveWorker(tx, command.CandidateID)
		if err != nil {
			return releaseport.CutoverJournalEntry{}, err
		}
		if lease.Generation != command.Generation || lease.Fence != command.Fence {
			return releaseport.CutoverJournalEntry{}, ErrFence
		}
		entries, err := s.store.ListCutoverSteps(tx, command.CandidateID)
		if err != nil {
			return releaseport.CutoverJournalEntry{}, err
		}
		if len(entries) >= len(releaseport.FixedCutoverSteps) || releaseport.FixedCutoverSteps[len(entries)] != command.Step {
			return releaseport.CutoverJournalEntry{}, ErrInvalidState
		}
		return s.store.AppendCutoverStep(tx, releaseport.CutoverJournalEntry{
			CandidateID: command.CandidateID, Generation: command.Generation, Step: command.Step,
			Fence: command.Fence, CompletedBy: command.ActorID, CompletedAt: now,
		})
	})
}

func (s *Service) Activate(ctx context.Context, command WorkerCommand) (releaseport.Candidate, error) {
	if !validWorkerCommand(command) {
		return releaseport.Candidate{}, ErrInvalidCommand
	}
	return mutate(s, ctx, "candidate.activate", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time) (releaseport.Candidate, error) {
		candidate, err := s.store.LockCandidate(tx, command.CandidateID)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if candidate.State != releaseport.CandidateCutoverActive {
			return releaseport.Candidate{}, ErrInvalidState
		}
		lease, err := s.store.GetActiveWorker(tx, command.CandidateID)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if lease.Generation != command.Generation || lease.Fence != command.Fence {
			return releaseport.Candidate{}, ErrFence
		}
		entries, err := s.store.ListCutoverSteps(tx, command.CandidateID)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if !completeJournal(entries) {
			return releaseport.Candidate{}, ErrNotReady
		}
		activated, err := s.store.TransitionCandidate(tx, command.CandidateID, releaseport.CandidateCutoverActive, releaseport.CandidateActivated, now)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if err = s.store.RetireWorker(tx, command.CandidateID, command.Generation, command.Fence, now); err != nil {
			return releaseport.Candidate{}, err
		}
		return activated, nil
	})
}

func (s *Service) RecordRollbackCheck(ctx context.Context, command RollbackCheckCommand) (releaseport.RollbackCheck, error) {
	if command.CandidateID < 1 || command.ActorID < 1 || !knownRollbackCheck(command.Kind) || !hexDigest(command.EvidenceSHA) || !validKey(command.IdempotencyKey) {
		return releaseport.RollbackCheck{}, ErrInvalidCommand
	}
	return mutate(s, ctx, "rollback.check.record", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time) (releaseport.RollbackCheck, error) {
		candidate, err := s.store.LockCandidate(tx, command.CandidateID)
		if err != nil {
			return releaseport.RollbackCheck{}, err
		}
		precheck := command.Kind != releaseport.RollbackExecutionReconciliation
		if precheck && candidate.State != releaseport.CandidateActivated || !precheck && candidate.State != releaseport.CandidateRollbackPending {
			return releaseport.RollbackCheck{}, ErrInvalidState
		}
		return s.store.CreateRollbackCheck(tx, releaseport.RollbackCheck{
			CandidateID: command.CandidateID, Kind: command.Kind, Passed: command.Passed,
			EvidenceSHA: command.EvidenceSHA, RecordedBy: command.ActorID, RecordedAt: now,
		})
	})
}

func (s *Service) RollbackEligibility(ctx context.Context, candidateID int64) (releaseport.RollbackEligibility, error) {
	if s == nil || s.store == nil || candidateID < 1 {
		return releaseport.RollbackEligibility{}, ErrInvalidCommand
	}
	if _, err := s.store.GetCandidate(ctx, candidateID); err != nil {
		return releaseport.RollbackEligibility{}, err
	}
	checks, err := s.store.ListRollbackChecks(ctx, candidateID)
	if err != nil {
		return releaseport.RollbackEligibility{}, err
	}
	return rollbackEligibility(candidateID, checks, s.now().UTC()), nil
}

func (s *Service) RequestRollback(ctx context.Context, command CandidateCommand) (releaseport.Candidate, error) {
	if !validCandidateCommand(command) {
		return releaseport.Candidate{}, ErrInvalidCommand
	}
	return mutate(s, ctx, "rollback.request", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time) (releaseport.Candidate, error) {
		candidate, err := s.store.LockCandidate(tx, command.CandidateID)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if candidate.State != releaseport.CandidateActivated {
			return releaseport.Candidate{}, ErrInvalidState
		}
		checks, err := s.store.ListRollbackChecks(tx, command.CandidateID)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if !rollbackEligibility(command.CandidateID, checks, now).Eligible {
			return releaseport.Candidate{}, ErrNotReady
		}
		return s.store.TransitionCandidate(tx, command.CandidateID, releaseport.CandidateActivated, releaseport.CandidateRollbackPending, now)
	})
}

func (s *Service) CompleteRollback(ctx context.Context, command CandidateCommand) (releaseport.Candidate, error) {
	if !validCandidateCommand(command) {
		return releaseport.Candidate{}, ErrInvalidCommand
	}
	return mutate(s, ctx, "rollback.complete", command.ActorID, command.IdempotencyKey, command, func(tx context.Context, now time.Time) (releaseport.Candidate, error) {
		candidate, err := s.store.LockCandidate(tx, command.CandidateID)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if candidate.State != releaseport.CandidateRollbackPending {
			return releaseport.Candidate{}, ErrInvalidState
		}
		checks, err := s.store.ListRollbackChecks(tx, command.CandidateID)
		if err != nil {
			return releaseport.Candidate{}, err
		}
		if !latestCheckPassed(checks, releaseport.RollbackExecutionReconciliation) {
			return releaseport.Candidate{}, ErrNotReady
		}
		return s.store.TransitionCandidate(tx, command.CandidateID, releaseport.CandidateRollbackPending, releaseport.CandidateRolledBack, now)
	})
}

var requiredPrerequisites = []releaseport.PrerequisiteKind{
	releaseport.PrerequisiteNightly,
	releaseport.PrerequisiteBackupRestoreDrill,
	releaseport.PrerequisiteMigration,
	releaseport.PrerequisiteContactClosure,
	releaseport.PrerequisiteCampaignClosure,
	releaseport.PrerequisiteOutboundClosure,
	releaseport.PrerequisiteCommerceClosure,
}

var requiredRollbackChecks = []releaseport.RollbackCheckKind{
	releaseport.RollbackSchemaCompatibility,
	releaseport.RollbackDataReconciliation,
	releaseport.RollbackOutboundReconciliation,
}

func readiness(candidate releaseport.Candidate, receipts []releaseport.PrerequisiteReceipt, now time.Time) releaseport.Readiness {
	have := make(map[releaseport.PrerequisiteKind]bool, len(receipts))
	invalid := make(map[releaseport.PrerequisiteKind]bool)
	for _, receipt := range receipts {
		if prerequisiteSubjectMatches(candidate, receipt) {
			have[receipt.Kind] = true
		} else {
			invalid[receipt.Kind] = true
		}
	}
	result := releaseport.Readiness{CandidateID: candidate.ID, CheckedAt: now}
	for _, kind := range requiredPrerequisites {
		if invalid[kind] {
			result.Invalid = append(result.Invalid, kind)
		} else if !have[kind] {
			result.Missing = append(result.Missing, kind)
		}
	}
	result.Ready = len(result.Missing) == 0 && len(result.Invalid) == 0
	return result
}

func prerequisiteSubjectMatches(candidate releaseport.Candidate, receipt releaseport.PrerequisiteReceipt) bool {
	return receipt.CandidateID == candidate.ID &&
		receipt.CandidateCommitSHA == candidate.CommitSHA &&
		receipt.CandidateArtifactDigest == candidate.ArtifactDigest &&
		receipt.CandidateManifestDigest == candidate.ManifestDigest &&
		receipt.CandidateConfigDigest == candidate.ConfigDigest &&
		receipt.CandidateSchemaVersion == candidate.TargetSchemaVersion
}

func cutoverProgress(entries []releaseport.CutoverJournalEntry) []releaseport.CutoverProgressEntry {
	result := make([]releaseport.CutoverProgressEntry, len(entries))
	for index, entry := range entries {
		result[index] = releaseport.CutoverProgressEntry{
			ID: entry.ID, CandidateID: entry.CandidateID, Generation: entry.Generation,
			Step: entry.Step, CompletedBy: entry.CompletedBy, CompletedAt: entry.CompletedAt,
		}
	}
	return result
}

func rollbackEligibility(candidateID int64, checks []releaseport.RollbackCheck, now time.Time) releaseport.RollbackEligibility {
	latest := make(map[releaseport.RollbackCheckKind]bool, len(requiredRollbackChecks))
	seen := make(map[releaseport.RollbackCheckKind]bool, len(requiredRollbackChecks))
	for _, check := range checks {
		if check.Kind == releaseport.RollbackExecutionReconciliation {
			continue
		}
		seen[check.Kind] = true
		latest[check.Kind] = check.Passed
	}
	result := releaseport.RollbackEligibility{CandidateID: candidateID, CheckedAt: now}
	for _, kind := range requiredRollbackChecks {
		if !seen[kind] {
			result.Missing = append(result.Missing, kind)
		} else if !latest[kind] {
			result.Blocked = append(result.Blocked, kind)
		}
	}
	result.Eligible = len(result.Missing) == 0 && len(result.Blocked) == 0
	return result
}

func latestCheckPassed(checks []releaseport.RollbackCheck, kind releaseport.RollbackCheckKind) bool {
	found := false
	passed := false
	for _, check := range checks {
		if check.Kind == kind {
			found = true
			passed = check.Passed
		}
	}
	return found && passed
}

func completeJournal(entries []releaseport.CutoverJournalEntry) bool {
	if len(entries) != len(releaseport.FixedCutoverSteps) {
		return false
	}
	for index, entry := range entries {
		if entry.Step != releaseport.FixedCutoverSteps[index] {
			return false
		}
	}
	return true
}

func knownPrerequisite(kind releaseport.PrerequisiteKind) bool {
	for _, required := range requiredPrerequisites {
		if kind == required {
			return true
		}
	}
	return false
}

func knownStep(step releaseport.CutoverStep) bool {
	for _, expected := range releaseport.FixedCutoverSteps {
		if step == expected {
			return true
		}
	}
	return false
}

func knownRollbackCheck(kind releaseport.RollbackCheckKind) bool {
	if kind == releaseport.RollbackExecutionReconciliation {
		return true
	}
	for _, required := range requiredRollbackChecks {
		if kind == required {
			return true
		}
	}
	return false
}

func validRegister(command RegisterCommand) bool {
	return command.ActorID > 0 && command.TargetSchemaVersion > 0 && sha40(command.CommitSHA) &&
		hexDigest(command.ArtifactDigest) && hexDigest(command.ManifestDigest) && hexDigest(command.ConfigDigest) &&
		validKey(command.IdempotencyKey)
}

func validCandidateCommand(command CandidateCommand) bool {
	return command.CandidateID > 0 && command.ActorID > 0 && validKey(command.IdempotencyKey)
}

func validWorkerCommand(command WorkerCommand) bool {
	return command.CandidateID > 0 && command.ActorID > 0 && command.Generation > 0 && validFence(command.Fence) && validKey(command.IdempotencyKey)
}

func sha40(value string) bool {
	return len(value) == 40 && value == strings.ToLower(value) && isHex(value)
}

func hexDigest(value string) bool {
	return len(value) == 64 && value == strings.ToLower(value) && isHex(value)
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func validFence(value string) bool { return hexDigest(value) }

func validKey(value string) bool {
	return len(value) >= 16 && len(value) <= 128 && strings.TrimSpace(value) == value
}

func mutate[T any](service *Service, ctx context.Context, action string, actorID int64, key string, payload any, callback func(context.Context, time.Time) (T, error)) (T, error) {
	var zero T
	if service == nil || service.uow == nil || service.store == nil || callback == nil {
		return zero, ErrInvalidCommand
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return zero, ErrInvalidCommand
	}
	keyDigest := digest(key)
	payloadDigest := digest(string(raw))
	var result T
	err = service.uow.Within(ctx, func(tx context.Context) error {
		receiptNow := service.now().UTC()
		receipt, created, receiptErr := service.store.ReserveOperationReceipt(tx, releaseport.OperationReceipt{
			Action: action, ActorID: actorID, KeyDigest: keyDigest, PayloadDigest: payloadDigest,
			State: "in_progress", CreatedAt: receiptNow,
		})
		if receiptErr != nil {
			return receiptErr
		}
		if !created {
			if receipt.PayloadDigest != payloadDigest || receipt.Action != action || receipt.ActorID != actorID {
				return ErrConflict
			}
			if receipt.State != "completed" || !json.Valid(receipt.Result) || json.Unmarshal(receipt.Result, &result) != nil {
				return ErrConflict
			}
			return nil
		}
		result, receiptErr = callback(tx, service.now().UTC())
		if receiptErr != nil {
			return receiptErr
		}
		resultJSON, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return ErrUnavailable
		}
		completed, receiptErr := service.store.CompleteOperationReceipt(tx, receipt.ID, resultJSON, service.now().UTC())
		if receiptErr != nil {
			return receiptErr
		}
		if completed.State != "completed" || completed.ID != receipt.ID {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return zero, err
	}
	return result, nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func randomFence() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
