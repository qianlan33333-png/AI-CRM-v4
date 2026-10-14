package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	port "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	postgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

func controlAction(action string) bool { return action == "stop" || action == "reconcile" }
func (w *PrivateMessageWriter) RequestPrivateMessageControlWithin(ctx context.Context, c port.PrivateMessageControlCommand) (port.PrivateMessageControl, error) {
	if w == nil || w.pool == nil || !controlAction(c.Action) || c.ActorID < 1 || c.PayloadReference == "" || !effect.ValidDigest(c.RequestKey) {
		return port.PrivateMessageControl{}, port.ErrInvalidPrivateMessageIntent
	}
	tx, err := postgres.RequireTransaction(ctx)
	if err != nil {
		return port.PrivateMessageControl{}, err
	}
	// Serialize all keys for the same immutable original task. A second stop
	// key cannot create a second Provider call, including after an unknown result.
	var msg string
	if err = tx.QueryRow(ctx, `SELECT message_id FROM outbound_private_message_receipts WHERE payload_reference=$1 FOR UPDATE`, c.PayloadReference).Scan(&msg); err != nil || msg == "" {
		return port.PrivateMessageControl{}, port.ErrInvalidPrivateMessageIntent
	}
	var prior port.PrivateMessageControl
	err = tx.QueryRow(ctx, `SELECT effect_id FROM outbound_private_message_controls WHERE payload_reference=$1 AND action=$2 AND (action='stop' OR request_key=$3)`, c.PayloadReference, c.Action, string(c.RequestKey)).Scan(&prior.EffectID)
	if err == nil {
		prior.Action, prior.State, prior.Replayed = c.Action, "queued", true
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return prior, err
	}
	logicalKey := string(c.RequestKey)
	if c.Action == "stop" {
		logicalKey = "original-task-stop"
	}
	source := effect.Hash("outbound.private-control.source.v1", c.PayloadReference, c.Action, logicalKey)
	envelope := effect.Envelope{Owner: effect.OwnerOutbound, Kind: effect.KindPrivateMessageControl, SourceRefDigest: source, TargetRefDigest: effect.Hash("outbound.private-control.target.v1", c.PayloadReference), PayloadDigest: effect.Hash("outbound.private-control.payload.v1", c.PayloadReference, c.Action), PolicyVersionHash: effect.Hash("outbound.private-control.single-task.v1")}
	projection, _, err := w.effects.AcceptAndQueueWithin(ctx, effect.AcceptCommand{ReceiptKey: effect.Hash("outbound.private-control.receipt.v1", c.PayloadReference, c.Action, logicalKey), Envelope: envelope})
	if err != nil {
		return prior, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbound_private_message_controls(payload_reference,action,effect_id,source_digest,actor_id,request_key) VALUES($1,$2,$3,$4,$5,$6)`, c.PayloadReference, c.Action, projection.ID, string(source), c.ActorID, string(c.RequestKey))
	return port.PrivateMessageControl{EffectID: projection.ID, Action: c.Action, State: "queued"}, err
}
func (w *PrivateMessageWriter) ReadPrivateMessageControl(ctx context.Context, ref, action string, effectIDs ...string) (port.PrivateMessageControl, error) {
	if w == nil || w.pool == nil || !controlAction(action) {
		return port.PrivateMessageControl{}, port.ErrInvalidPrivateMessageIntent
	}
	var v port.PrivateMessageControl
	wanted := ""
	if len(effectIDs) > 0 {
		wanted = effectIDs[0]
	}
	err := w.pool.QueryRow(ctx, `SELECT effect_id,action,result_state,receipt_digest,sent_at,stopped_at,observed_at FROM outbound_private_message_controls WHERE payload_reference=$1 AND action=$2 AND ($3='' OR effect_id=$3) ORDER BY id DESC LIMIT 1`, ref, action, wanted).Scan(&v.EffectID, &v.Action, &v.State, &v.ReceiptDigest, &v.SentAt, &v.StoppedAt, &v.ObservedAt)
	if err == nil && v.State == "" {
		reader, ok := w.effects.(effect.Reader)
		if !ok {
			return v, errors.New("private control effect reader unavailable")
		}
		projection, e := reader.Get(ctx, v.EffectID)
		if e != nil {
			return v, e
		}
		v.State = string(projection.State)
	}
	return v, err
}

type controlArtifact struct {
	State      string     `json:"state"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
	StoppedAt  *time.Time `json:"stopped_at,omitempty"`
	ObservedAt time.Time  `json:"observed_at"`
	StopCalled bool       `json:"stop_called"`
}
type PrivateMessageControlProvider struct {
	Enabled bool
	Store   *PrivateMessageWriter
	Reader  port.PrivateMessageDeliveryReader
	Stopper port.PrivateMessageStopper
}

func (p *PrivateMessageControlProvider) Execute(ctx context.Context, envelope effect.Envelope, attempt effect.Attempt) (effect.AdapterResult, error) {
	receipt := effect.Hash("outbound.private-control.result.v1", string(envelope.Fingerprint()))
	fail := func(state effect.State, code string, attempted bool) effect.AdapterResult {
		return effect.AdapterResult{Completion: state, ReceiptDigest: effect.Hash(string(receipt), code), FailureCode: code, CallAttempted: attempted, RealExternalCallExecuted: attempted}
	}
	if p == nil || !p.Enabled || p.Store == nil || p.Reader == nil || p.Stopper == nil || envelope.Kind != effect.KindPrivateMessageControl || !envelope.Valid() {
		return fail(effect.StateFinalFailed, "control_disabled", false), nil
	}
	var ref, action string
	if err := p.Store.pool.QueryRow(ctx, `SELECT payload_reference,action FROM outbound_private_message_controls WHERE source_digest=$1`, string(envelope.SourceRefDigest)).Scan(&ref, &action); err != nil {
		return fail(effect.StateFinalFailed, "control_not_found", false), nil
	}
	original, found, err := p.Store.PrivateMessageReceipt(ctx, ref)
	if err != nil || !found || original.MessageID == "" {
		return fail(effect.StateFinalFailed, "original_task_unavailable", false), nil
	}
	evidence := controlArtifact{ObservedAt: time.Now().UTC()}
	providerRead := false
	finish := func() (effect.AdapterResult, error) {
		raw, _ := json.Marshal(evidence)
		artifact := effect.ResultArtifact{Kind: "outbound.private-control.v1", Payload: raw}
		artifact.Digest = effect.Hash("external-effect.artifact.v1", artifact.Kind, string(raw))
		return effect.AdapterResult{Completion: effect.StateExecuted, ReceiptDigest: effect.Hash(string(receipt), string(artifact.Digest)), CallAttempted: providerRead || evidence.StopCalled, RealExternalCallExecuted: providerRead || evidence.StopCalled, Artifact: artifact}, nil
	}
	// Existing owner-maintenance stop proof is server-owned and audited. Never
	// query a stopped task as though a missing Provider row proved non-delivery.
	if original.Status != nil && *original.Status == 1 && original.SentAt != nil {
		evidence.State, evidence.SentAt = "delivered", original.SentAt
		return finish()
	}
	if original.Reason == "provider_stopped" {
		evidence.State = "stopped"
		evidence.StoppedAt = &original.ObservedAt
		// Preserve the first verified stop time across later observation jobs.
		var confirmed time.Time
		readErr := p.Store.pool.QueryRow(ctx, `SELECT stopped_at FROM outbound_private_message_controls WHERE payload_reference=$1 AND result_state='stopped' ORDER BY id LIMIT 1`, ref).Scan(&confirmed)
		if readErr == nil {
			evidence.StoppedAt = &confirmed
		} else if !errors.Is(readErr, pgx.ErrNoRows) {
			return fail(effect.StateFinalFailed, "stop_proof_read_unavailable", false), nil
		}
		return finish()
	}
	providerRead = true
	current, err := readSingleControlTarget(ctx, p.Reader, original)
	if err != nil {
		return fail(effect.StateFinalFailed, "control_read_unavailable", false), nil
	}
	if current.Status == nil {
		return fail(effect.StateFinalFailed, "delivery_unverified", false), nil
	}
	if *current.Status == 1 {
		if current.SentAt == nil {
			return fail(effect.StateFinalFailed, "sent_time_missing", false), nil
		}
		evidence.State, evidence.SentAt = "delivered", current.SentAt
		return finish()
	}
	if action == "reconcile" {
		if *current.Status != 0 {
			return fail(effect.StateFinalFailed, "delivery_not_pending", true), nil
		}
		evidence.State = "pending"
		return finish()
	}
	if *current.Status != 0 {
		return fail(effect.StateFinalFailed, "original_task_not_pending", false), nil
	}
	attempted, err := p.Stopper.StopPrivateMessage(ctx, original.MessageID)
	if err != nil {
		state := effect.StateFinalFailed
		var e port.PrivateMessageSendError
		if attempted && (!errors.As(err, &e) || e.OutcomeUnknown()) {
			state = effect.StateUnknown
		}
		return fail(state, "provider_stop_unconfirmed", attempted), nil
	}
	if !attempted {
		return fail(effect.StateFinalFailed, "provider_stop_unconfirmed", false), nil
	}
	now := time.Now().UTC()
	evidence.State, evidence.StoppedAt, evidence.StopCalled = "stopped", &now, true
	// A simultaneous employee send wins over the cancellation projection. A
	// successful stop response is retained when a stopped task is no longer queryable.
	after, readErr := readSingleControlTarget(ctx, p.Reader, original)
	if readErr == nil && after.Status != nil && *after.Status == 1 && after.SentAt != nil {
		evidence.State, evidence.SentAt, evidence.StoppedAt = "delivered", after.SentAt, nil
	}
	return finish()
}

func readSingleControlTarget(ctx context.Context, r port.PrivateMessageDeliveryReader, original port.PrivateMessageDelivery) (port.PrivateMessageDelivery, error) {
	cursor := ""
	seen := map[string]bool{}
	var items []port.PrivateMessageDelivery
	for {
		if seen[cursor] {
			return original, errors.New("control cursor repeated")
		}
		seen[cursor] = true
		page, err := r.GetPrivateMessageSendResult(ctx, original.MessageID, original.SenderUserID, cursor)
		if err != nil {
			return original, err
		}
		items = append(items, page.Items...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(items) != 1 || items[0].MessageID != original.MessageID || items[0].SenderUserID != original.SenderUserID || items[0].ExternalUserID != original.ExternalUserID {
		return original, errors.New("control task is shared or mismatched")
	}
	if items[0].Status != nil && *items[0].Status == 1 && (items[0].SentAt == nil || items[0].SentAt.After(time.Now().Add(time.Minute))) {
		return original, errors.New("control sent time is unverified")
	}
	if original.Status != nil && *original.Status == 1 && (items[0].Status == nil || *items[0].Status != 1 || items[0].SentAt == nil || original.SentAt == nil || !items[0].SentAt.Equal(*original.SentAt)) {
		return original, errors.New("control would regress proven delivery")
	}
	return items[0], nil
}

// Completion participates in EER's completion UOW; it never rewrites the
// original creation effect or its attempt history.
func (w *PrivateMessageWriter) CompleteEffect(ctx context.Context, effectID string, envelope effect.Envelope, _ effect.Attempt, result effect.AdapterResult) error {
	if envelope.Kind != effect.KindPrivateMessageControl {
		return errors.New("invalid private control kind")
	}
	tx, err := postgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if result.Completion != effect.StateExecuted {
		return nil
	}
	var a controlArtifact
	if result.Artifact.Kind != "outbound.private-control.v1" || !result.Artifact.Valid() || json.Unmarshal(result.Artifact.Payload, &a) != nil || a.ObservedAt.IsZero() || ((a.State == "delivered") != (a.SentAt != nil)) || ((a.State == "stopped") != (a.StoppedAt != nil)) || (a.State != "delivered" && a.State != "stopped" && a.State != "pending") {
		return errors.New("invalid private control evidence")
	}
	var ref string
	if err = tx.QueryRow(ctx, `UPDATE outbound_private_message_controls SET result_state=$2,receipt_digest=$3,sent_at=$4,stopped_at=$5,observed_at=$6 WHERE effect_id=$1 RETURNING payload_reference`, effectID, a.State, string(result.ReceiptDigest), a.SentAt, a.StoppedAt, a.ObservedAt).Scan(&ref); err != nil {
		return err
	}
	if !result.CallAttempted {
		var status *int
		var sent *time.Time
		var reason string
		if err = tx.QueryRow(ctx, `SELECT delivery_status,sent_at,reason FROM outbound_private_message_receipts WHERE payload_reference=$1 FOR UPDATE`, ref).Scan(&status, &sent, &reason); err != nil {
			return err
		}
		if a.State == "pending" || (a.State == "stopped" && reason != "provider_stopped") || (a.State == "delivered" && (status == nil || *status != 1 || sent == nil || !sent.Equal(*a.SentAt))) {
			return errors.New("private control lacks prior owner evidence")
		}
	}
	if a.State == "delivered" {
		tag, e := tx.Exec(ctx, `UPDATE outbound_private_message_receipts SET delivery_status=1,sent_at=$2,observed_at=$3 WHERE payload_reference=$1 AND (delivery_status IS NULL OR delivery_status<>1 OR sent_at=$2)`, ref, a.SentAt, a.ObservedAt)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return errors.New("private control delivery conflict")
		}
	}
	if a.State == "stopped" {
		tag, e := tx.Exec(ctx, `UPDATE outbound_private_message_receipts SET reason='provider_stopped',observed_at=$2 WHERE payload_reference=$1 AND (delivery_status IS NULL OR delivery_status<>1)`, ref, a.ObservedAt)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return errors.New("private control stop conflicts with delivery")
		}
	}
	return nil
}
