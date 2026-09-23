package externaleffects

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"testing"
	"time"
)

func TestStoppedAttemptEvidenceRequiresCompletedUnknownAttempt(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repo := &Repository{pool: pool}
	var id int64
	d := string(digestForTest("stopped-attempt"))
	completed := time.Now().UTC().Add(-3 * time.Hour)
	err = pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state,attempt_count,generation) VALUES('outbound','outbound_message',$1,$1,$1,$1,$1,'outcome_unknown',1,1) RETURNING id`, d).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state,completed_at) VALUES($1,1,1,1,'outcome_unknown',$2)`, id, completed); err != nil {
		t.Fatal(err)
	}
	for _, complete := range []bool{true, false} {
		if !complete {
			if _, err = pool.Exec(ctx, `UPDATE external_effect_attempts SET completed_at=NULL WHERE effect_id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
		err = uow.Within(ctx, func(tx context.Context) error {
			proof, e := repo.StoppedAttemptWithin(tx, effectID(id))
			if e == nil && (proof.State != port.StateUnknown || proof.CompletedAt.IsZero()) {
				t.Fatal("missing evidence")
			}
			return e
		})
		if complete && err != nil {
			t.Fatal(err)
		}
		if !complete && err == nil {
			t.Fatal("unfinished attempt accepted")
		}
	}
}
