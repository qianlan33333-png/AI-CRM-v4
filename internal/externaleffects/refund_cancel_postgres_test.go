package externaleffects

import (
	"context"
	"errors"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

func TestPostgreSQLRefundCancellationAndSplitDispatchHaveOneWinner(t *testing.T) {
	for _, scenario := range []string{"refund-first", "dispatch-first", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := effectIntegrationPool(t)
			defer cleanup()
			ctx := context.Background()
			workers := river.NewWorkers()
			if err := river.AddWorkerSafely[EffectJobArgs](workers, NewWorker(nil, nil)); err != nil {
				t.Fatal(err)
			}
			client, err := platformjobqueue.NewInsertClient(pool, workers)
			if err != nil {
				t.Fatal(err)
			}
			repository, err := NewRepository(pool, client)
			if err != nil {
				t.Fatal(err)
			}
			envelope := effectport.Envelope{Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayProfitSharing, SourceRefDigest: digestForTest("refund-race-source"), TargetRefDigest: digestForTest("refund-race-target"), PayloadDigest: digestForTest("refund-race-payload"), PolicyVersionHash: digestForTest("refund-race-policy")}
			accepted, _, err := repository.AcceptAndQueue(ctx, effectport.AcceptCommand{ReceiptKey: digestForTest("refund-race-accept"), Envelope: envelope})
			if err != nil {
				t.Fatal(err)
			}
			id, err := parseEffectID(accepted.ID)
			if err != nil {
				t.Fatal(err)
			}
			var jobID int64
			if err = pool.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1`, id).Scan(&jobID); err != nil {
				t.Fatal(err)
			}
			wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer wrapped.Close()
			uow, err := platformpostgres.NewUnitOfWork(wrapped)
			if err != nil {
				t.Fatal(err)
			}
			adapter := &integrationAdapter{result: AdapterResult{Completion: StateExecuted, ReceiptDigest: digestForTest("refund-race-executed"), CallAttempted: true, RealExternalCallExecuted: true}}
			command := effectport.CancelCommand{EffectID: accepted.ID, ReceiptKey: digestForTest("refund-race-cancel"), ReasonCode: "refund_requested", Actor: effectport.ControlActor{AdminUserID: 17}, OnlyIfUnattempted: true}
			cancel := func() error {
				return uow.Within(ctx, func(tx context.Context) error {
					_, inner := repository.CancelQueuedEffectWithin(tx, command)
					return inner
				})
			}
			dispatch := func() error { return repository.RunAttempt(ctx, id, 1, jobID, adapter) }
			if scenario == "concurrent" {
				start, done := make(chan struct{}), make(chan error, 2)
				go func() { <-start; done <- cancel() }()
				go func() { <-start; done <- dispatch() }()
				close(start)
				for range 2 {
					if inner := <-done; inner != nil {
						t.Fatal(inner)
					}
				}
			} else {
				first, second := cancel, dispatch
				if scenario == "dispatch-first" {
					first, second = dispatch, cancel
				}
				if err = first(); err != nil {
					t.Fatal(err)
				}
				if err = second(); err != nil {
					t.Fatal(err)
				}
			}
			if err = cancel(); err != nil {
				t.Fatalf("same-command replay=%v", err)
			}
			projection, err := repository.Get(ctx, accepted.ID)
			if err != nil {
				t.Fatal(err)
			}
			var receipts int
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM external_effect_operation_receipts WHERE effect_id=$1 AND operation='cancel'`, id).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if projection.State == StateCancelled {
				if adapter.calls != 0 || projection.AttemptCount != 0 || receipts != 1 {
					t.Fatalf("cancelled=%+v calls=%d receipts=%d", projection, adapter.calls, receipts)
				}
			} else if projection.State == StateExecuted {
				if adapter.calls != 1 || projection.AttemptCount != 1 || receipts != 0 {
					t.Fatalf("executed=%+v calls=%d receipts=%d", projection, adapter.calls, receipts)
				}
			} else {
				t.Fatalf("unexpected split state=%+v", projection)
			}
			if scenario == "refund-first" && projection.State != StateCancelled || scenario == "dispatch-first" && projection.State != StateExecuted {
				t.Fatalf("wrong winner=%+v", projection)
			}
			if projection.State == StateCancelled {
				strict := command
				strict.OnlyIfUnattempted = false
				if err = uow.Within(ctx, func(tx context.Context) error {
					_, inner := repository.CancelQueuedEffectWithin(tx, strict)
					return inner
				}); !errors.Is(err, ErrPayloadMismatch) {
					t.Fatalf("changed cancellation contract must not replay the receipt: %v", err)
				}
			}
		})
	}
}
