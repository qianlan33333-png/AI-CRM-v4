package externaleffects

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestPostgreSQLFinalFailureWithoutExternalCallProof verifies the recovery
// precondition against the real EER tables. It never invokes an Adapter: the
// proof is intentionally only about durable, completed local attempt facts.
func TestPostgreSQLFinalFailureWithoutExternalCallProof(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository := &Repository{pool: pool}

	t.Run("proves every completed local receiver add failure", func(t *testing.T) {
		id, latest := insertFinalFailureProofFixture(t, pool, "valid", effectport.StateFinalFailed, 2, []proofAttempt{{number: 1, state: effectport.StateFinalFailed, completed: time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)}, {number: 2, state: effectport.StateFinalFailed, completed: time.Date(2026, 9, 14, 2, 0, 0, 0, time.UTC)}})
		var proof effectport.FinalFailureWithoutExternalCallEvidence
		err := uow.Within(ctx, func(tx context.Context) error {
			var inner error
			proof, inner = repository.FinalFailureWithoutExternalCallWithin(tx, effectID(id), effectport.OwnerPayment, effectport.KindWeChatPayReceiverAdd)
			return inner
		})
		if err != nil {
			t.Fatal(err)
		}
		if proof.ID != effectID(id) || proof.Owner != effectport.OwnerPayment || proof.Kind != effectport.KindWeChatPayReceiverAdd || proof.State != effectport.StateFinalFailed || proof.AttemptCount != 2 || !proof.CompletedAt.Equal(latest) {
			t.Fatalf("proof=%+v", proof)
		}
	})

	for _, test := range []struct {
		name          string
		state         effectport.State
		attemptCount  int
		attempts      []proofAttempt
		expectedOwner effectport.Owner
		expectedKind  effectport.Kind
	}{
		{
			name:  "previous call was attempted",
			state: effectport.StateFinalFailed, attemptCount: 2,
			attempts:      []proofAttempt{{number: 1, state: effectport.StateFinalFailed, callAttempted: true, completed: proofCompletedAt(1)}, {number: 2, state: effectport.StateFinalFailed, completed: proofCompletedAt(2)}},
			expectedOwner: effectport.OwnerPayment, expectedKind: effectport.KindWeChatPayReceiverAdd,
		},
		{
			name:  "previous real external call",
			state: effectport.StateFinalFailed, attemptCount: 1,
			attempts:      []proofAttempt{{number: 1, state: effectport.StateFinalFailed, callAttempted: true, realExternal: true, completed: proofCompletedAt(1)}},
			expectedOwner: effectport.OwnerPayment, expectedKind: effectport.KindWeChatPayReceiverAdd,
		},
		{
			name:  "unknown attempt",
			state: effectport.StateFinalFailed, attemptCount: 1,
			attempts:      []proofAttempt{{number: 1, state: effectport.StateUnknown, completed: proofCompletedAt(1)}},
			expectedOwner: effectport.OwnerPayment, expectedKind: effectport.KindWeChatPayReceiverAdd,
		},
		{
			name:  "unfinished attempt",
			state: effectport.StateFinalFailed, attemptCount: 1,
			attempts:      []proofAttempt{{number: 1, state: effectport.StateFinalFailed}},
			expectedOwner: effectport.OwnerPayment, expectedKind: effectport.KindWeChatPayReceiverAdd,
		},
		{
			name:  "missing attempt count",
			state: effectport.StateFinalFailed, attemptCount: 2,
			attempts:      []proofAttempt{{number: 1, state: effectport.StateFinalFailed, completed: proofCompletedAt(1)}},
			expectedOwner: effectport.OwnerPayment, expectedKind: effectport.KindWeChatPayReceiverAdd,
		},
		{
			name:  "wrong expected owner",
			state: effectport.StateFinalFailed, attemptCount: 1,
			attempts:      []proofAttempt{{number: 1, state: effectport.StateFinalFailed, completed: proofCompletedAt(1)}},
			expectedOwner: effectport.OwnerOutbound, expectedKind: effectport.KindOutboundMessage,
		},
		{
			name:  "wrong expected kind",
			state: effectport.StateFinalFailed, attemptCount: 1,
			attempts:      []proofAttempt{{number: 1, state: effectport.StateFinalFailed, completed: proofCompletedAt(1)}},
			expectedOwner: effectport.OwnerPayment, expectedKind: effectport.KindWeChatPayProfitSharing,
		},
		{
			name:  "effect is not terminal",
			state: effectport.StateRetryable, attemptCount: 1,
			attempts:      []proofAttempt{{number: 1, state: effectport.StateFinalFailed, completed: proofCompletedAt(1)}},
			expectedOwner: effectport.OwnerPayment, expectedKind: effectport.KindWeChatPayReceiverAdd,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, _ := insertFinalFailureProofFixture(t, pool, test.name, test.state, test.attemptCount, test.attempts)
			err := uow.Within(ctx, func(tx context.Context) error {
				_, inner := repository.FinalFailureWithoutExternalCallWithin(tx, effectID(id), test.expectedOwner, test.expectedKind)
				return inner
			})
			if !errors.Is(err, effectport.ErrReconciliationConflict) {
				t.Fatalf("proof error=%v; want reconciliation conflict", err)
			}
		})
	}
}

func TestPostgreSQLFinalFailureWithoutExternalCallProofRejectsConcurrentEffectLock(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository := &Repository{pool: pool}
	id, _ := insertFinalFailureProofFixture(t, pool, "locked", effectport.StateFinalFailed, 1, []proofAttempt{{number: 1, state: effectport.StateFinalFailed, completed: proofCompletedAt(1)}})

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err = connection.Exec(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = connection.Exec(context.Background(), "ROLLBACK") }()
	if _, err = connection.Exec(ctx, `SELECT id FROM external_effects WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}

	err = uow.Within(ctx, func(tx context.Context) error {
		_, inner := repository.FinalFailureWithoutExternalCallWithin(tx, effectID(id), effectport.OwnerPayment, effectport.KindWeChatPayReceiverAdd)
		return inner
	})
	if !errors.Is(err, effectport.ErrReconciliationConflict) {
		t.Fatalf("concurrent proof error=%v; want reconciliation conflict", err)
	}
	var attempts int
	if err = pool.QueryRow(ctx, `SELECT attempt_count FROM external_effects WHERE id=$1`, id).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("effect changed while lock conflicted attempts=%d err=%v", attempts, err)
	}
}

type proofAttempt struct {
	number        int
	state         effectport.State
	callAttempted bool
	realExternal  bool
	completed     time.Time
}

func proofCompletedAt(number int) time.Time {
	return time.Date(2026, 9, 14, 0, number, 0, 0, time.UTC)
}

func insertFinalFailureProofFixture(t *testing.T, pool *pgxpool.Pool, label string, state effectport.State, attemptCount int, attempts []proofAttempt) (int64, time.Time) {
	t.Helper()
	ctx := context.Background()
	digest := string(digestForTest("final-failure-proof:" + label))
	updated := proofCompletedAt(3)
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state,attempt_count,generation,updated_at) VALUES('payment','wechat_pay_profit_sharing_receiver_v1',$1,$1,$1,$1,$1,$2,$3,$4,$5) RETURNING id`, digest, state, attemptCount, max(1, attemptCount), updated).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var latest time.Time
	for _, attempt := range attempts {
		if !attempt.completed.IsZero() && (latest.IsZero() || attempt.completed.After(latest)) {
			latest = attempt.completed
		}
		var completed any
		if !attempt.completed.IsZero() {
			completed = attempt.completed
		}
		if _, err := pool.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state,call_attempted,real_external_call_executed,completed_at) VALUES($1,$2::integer,$3::bigint,$2::bigint,$4,$5,$6,$7)`, id, attempt.number, max(1, attempt.number), attempt.state, attempt.callAttempted, attempt.realExternal, completed); err != nil {
			t.Fatal(err)
		}
	}
	return id, latest
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
