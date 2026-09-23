package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type registrationTestUOW struct{}

func (registrationTestUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type registrationTestStore struct {
	agreement    distributionstore.Agreement
	distributors map[int64]distributiondomain.Distributor
	readiness    map[int64]distributionport.ReceiverReadiness
	receipts     map[string]distributionstore.OperationReceipt
	nextID       int64
	inserts      int
	audits       int
	outbox       int
	readLocks    []bool
}

func (s *registrationTestStore) ActiveAgreementWithin(context.Context) (distributionstore.Agreement, error) {
	return s.agreement, nil
}
func (s *registrationTestStore) ReadDistributorByCustomerWithin(_ context.Context, customerID int64, lock bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	s.readLocks = append(s.readLocks, lock)
	value, ok := s.distributors[customerID]
	if !ok {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrNotFound
	}
	return value, s.readiness[customerID], nil
}
func (s *registrationTestStore) ReadDistributorWithin(_ context.Context, distributorID int64, _ bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	for _, value := range s.distributors {
		if value.ID == distributorID {
			return value, s.readiness[value.CustomerID], nil
		}
	}
	return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrNotFound
}
func (s *registrationTestStore) InsertDistributorWithin(_ context.Context, customerID int64, publicNo, agreementVersion string, now time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	if _, exists := s.distributors[customerID]; exists {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrConflict
	}
	s.nextID++
	s.inserts++
	value := distributiondomain.Distributor{ID: s.nextID, CustomerID: customerID, PublicNo: publicNo, AgreementVersion: agreementVersion, Enabled: true, RegisteredAt: now, Version: 1}
	s.distributors[customerID] = value
	return value, s.readiness[customerID], nil
}
func (s *registrationTestStore) UpdateReceiverReadinessWithin(_ context.Context, distributorID, expectedVersion int64, readiness distributionport.ReceiverReadiness, _ time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	for customerID, value := range s.distributors {
		if value.ID != distributorID {
			continue
		}
		if value.Version != expectedVersion {
			return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrConflict
		}
		value.Version++
		s.distributors[customerID] = value
		s.readiness[customerID] = readiness
		return value, readiness, nil
	}
	return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrNotFound
}
func (*registrationTestStore) LockOperationReceiptWithin(context.Context, string, string, string) error {
	return nil
}
func (s *registrationTestStore) ReadOperationReceiptWithin(_ context.Context, operation, actorScope, idempotencyKey string) (distributionstore.OperationReceipt, bool, error) {
	value, found := s.receipts[operation+":"+actorScope+":"+idempotencyKey]
	return value, found, nil
}
func (s *registrationTestStore) AppendOperationReceiptWithin(_ context.Context, operation, actorScope, idempotencyKey string, digest [sha256.Size]byte, _ string, resultID int64, _ time.Time) error {
	key := operation + ":" + actorScope + ":" + idempotencyKey
	if _, exists := s.receipts[key]; exists {
		return distributionport.ErrConflict
	}
	s.receipts[key] = distributionstore.OperationReceipt{PayloadDigest: digest, ResultKind: "distributor", ResultID: resultID}
	return nil
}
func (s *registrationTestStore) AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error {
	s.audits++
	return nil
}
func (s *registrationTestStore) AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error {
	s.outbox++
	return nil
}

func registrationFixture(t *testing.T) (*RegistrationService, *registrationTestStore, distributionport.RegisterCommand) {
	t.Helper()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	store := &registrationTestStore{agreement: distributionstore.Agreement{Version: "v1", Content: "推广协议"}, distributors: map[int64]distributiondomain.Distributor{}, readiness: map[int64]distributionport.ReceiverReadiness{}, receipts: map[string]distributionstore.OperationReceipt{}, nextID: 40}
	service, err := NewRegistrationService(registrationTestUOW{}, store, &settlementPaymentStub{})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	command := distributionport.RegisterCommand{Actor: distributionport.TrustedSessionActor{CustomerID: 17, IdentityID: 27, AppID: "wx-registration", AppScope: "wechat-app:wx-registration", Channel: "mini_program", OccurredAt: now}, AgreementVersion: "v1", IdempotencyKey: "registration-idempotency-key"}
	return service, store, command
}

type receiverPreparationOrderStub struct {
	settlementPaymentStub
	store    *registrationTestStore
	prepared paymentport.ReceiverReadiness
}

func (stub *receiverPreparationOrderStub) PrepareProfitSharingReceiverWithin(_ context.Context, _ paymentport.ReceiverPreparation) (paymentport.ReceiverReadiness, error) {
	if len(stub.store.readLocks) != 1 || stub.store.readLocks[0] {
		return paymentport.ReceiverReadiness{}, errors.New("distribution row was locked before Payment receiver transition")
	}
	return stub.prepared, nil
}

func TestPrepareReceiverTakesPaymentBeforeDistributionSnapshotLock(t *testing.T) {
	service, store, command := registrationFixture(t)
	store.distributors[command.Actor.CustomerID] = distributiondomain.Distributor{ID: 77, CustomerID: command.Actor.CustomerID, PublicNo: "DLOCK77", AgreementVersion: "v1", Enabled: true, RegisteredAt: command.Actor.OccurredAt, Version: 3}
	payment := &receiverPreparationOrderStub{store: store, prepared: paymentport.ReceiverReadiness{Reference: "psrecv_77", AppID: command.Actor.AppID, State: "accepted", UpdatedAt: command.Actor.OccurredAt}}
	service.settlement = payment
	result, err := service.PrepareReceiver(context.Background(), command.Actor)
	if err != nil || result.State != "processing" || result.Receiver.Reference != "psrecv_77" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(store.readLocks) != 2 || store.readLocks[0] || !store.readLocks[1] {
		t.Fatalf("read locks=%v; want initial unlocked read then Distribution lock after Payment", store.readLocks)
	}
	if got := store.distributors[command.Actor.CustomerID].Version; got != 4 || store.audits != 1 {
		t.Fatalf("updated distributor version/audits=%d/%d", got, store.audits)
	}
}

func TestRegisterPersistsReceiptAndExactRetryReplaysOriginalDistributor(t *testing.T) {
	service, store, command := registrationFixture(t)
	first, err := service.Register(context.Background(), command)
	if err != nil || first.Distributor.ID < 1 || store.inserts != 1 || store.audits != 1 || store.outbox != 1 || len(store.receipts) != 1 {
		t.Fatalf("first profile=%+v inserts/audits/outbox/receipts=%d/%d/%d/%d err=%v", first, store.inserts, store.audits, store.outbox, len(store.receipts), err)
	}
	second, err := service.Register(context.Background(), command)
	if err != nil || second.Distributor.ID != first.Distributor.ID || second.Distributor.PublicNo != first.Distributor.PublicNo || store.inserts != 1 || store.audits != 1 || store.outbox != 1 || len(store.receipts) != 1 {
		t.Fatalf("replay profile=%+v inserts/audits/outbox/receipts=%d/%d/%d/%d err=%v", second, store.inserts, store.audits, store.outbox, len(store.receipts), err)
	}
}

func TestRegisterRejectsSameActorAndKeyPayloadDrift(t *testing.T) {
	service, store, command := registrationFixture(t)
	if _, err := service.Register(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	store.agreement.Version = "v2"
	drift := command
	drift.AgreementVersion = "v2"
	if _, err := service.Register(context.Background(), drift); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("drift error=%v", err)
	}
	if store.inserts != 1 || store.audits != 1 || store.outbox != 1 || len(store.receipts) != 1 {
		t.Fatalf("drift wrote inserts/audits/outbox/receipts=%d/%d/%d/%d", store.inserts, store.audits, store.outbox, len(store.receipts))
	}
}

func TestRegisterRejectsReceiptWhoseResultBelongsToAnotherCustomer(t *testing.T) {
	service, store, command := registrationFixture(t)
	store.distributors[99] = distributiondomain.Distributor{ID: 88, CustomerID: 99, PublicNo: "DOTHER88", AgreementVersion: "v1", Enabled: true, RegisteredAt: time.Now().UTC(), Version: 1}
	store.receipts["register:customer:17:"+command.IdempotencyKey] = distributionstore.OperationReceipt{PayloadDigest: registrationPayloadDigest("v1"), ResultKind: "distributor", ResultID: 88}
	if _, err := service.Register(context.Background(), command); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("foreign receipt error=%v", err)
	}
	if store.inserts != 0 || store.audits != 0 || store.outbox != 0 {
		t.Fatalf("foreign receipt wrote inserts/audits/outbox=%d/%d/%d", store.inserts, store.audits, store.outbox)
	}
}

func TestRegisterRejectsReceiptWithAnotherResultKind(t *testing.T) {
	service, store, command := registrationFixture(t)
	store.distributors[17] = distributiondomain.Distributor{ID: 88, CustomerID: 17, PublicNo: "DWRONGKIND", AgreementVersion: "v1", Enabled: true, RegisteredAt: time.Now().UTC(), Version: 1}
	store.receipts["register:customer:17:"+command.IdempotencyKey] = distributionstore.OperationReceipt{PayloadDigest: registrationPayloadDigest("v1"), ResultKind: "credential", ResultID: 88}
	if _, err := service.Register(context.Background(), command); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("wrong receipt kind error=%v", err)
	}
	if store.inserts != 0 || store.audits != 0 || store.outbox != 0 {
		t.Fatalf("wrong receipt kind wrote inserts/audits/outbox=%d/%d/%d", store.inserts, store.audits, store.outbox)
	}
}

func TestRegisterBackfillsReceiptForExistingDistributorWithoutRepeatingRegistrationEvents(t *testing.T) {
	service, store, command := registrationFixture(t)
	now := time.Now().UTC()
	store.distributors[17] = distributiondomain.Distributor{ID: 61, CustomerID: 17, PublicNo: "DLEGACY61", AgreementVersion: "v1", Enabled: true, RegisteredAt: now, Version: 1}
	profile, err := service.Register(context.Background(), command)
	if err != nil || profile.Distributor.ID != 61 || store.inserts != 0 || store.audits != 0 || store.outbox != 0 || len(store.receipts) != 1 {
		t.Fatalf("legacy profile=%+v inserts/audits/outbox/receipts=%d/%d/%d/%d err=%v", profile, store.inserts, store.audits, store.outbox, len(store.receipts), err)
	}
}
