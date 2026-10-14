package store

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	"strconv"
)

type groupObservation struct {
	Status   int    `json:"status"`
	Evidence string `json:"evidence_digest"`
}

func sameBatchTargets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func groupDeliveryStatuses(targets []string, raw []byte) []groupopsport.GroupDeliveryStatus {
	observed := map[string]groupObservation{}
	_ = json.Unmarshal(raw, &observed)
	result := make([]groupopsport.GroupDeliveryStatus, 0, len(targets))
	for _, target := range targets {
		item := groupopsport.GroupDeliveryStatus{TargetReference: target}
		if observation, ok := observed[target]; ok {
			status := observation.Status
			item.Status = &status
			item.DeliveryProven = status == 1
			item.EvidenceDigest = observation.Evidence
		}
		result = append(result, item)
	}
	return result
}

func recordBatchGroupDelivery(ctx context.Context, tx pgx.Tx, task groupopsport.GroupMessageReceipt, evidence string, targets []string, effectID int64) error {
	if !sameBatchTargets(targets, task.ChatIDs) || task.ChatID != targets[0] {
		return ErrConflict
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT delivery_observations FROM group_ops_group_message_tasks WHERE execution_id=$1 AND external_effect_id=$2 AND msgid=$3 AND sender_userid_snapshot=$4 AND chat_reference=$5 FOR UPDATE`, task.ExecutionID, effectID, task.MessageID, task.SenderUserID, task.ChatID).Scan(&raw)
	if err != nil {
		return err
	}
	observed := map[string]groupObservation{}
	if json.Unmarshal(raw, &observed) != nil {
		return ErrInvalid
	}
	allowed := map[string]bool{}
	for _, target := range targets {
		allowed[target] = true
	}
	seen := map[string]bool{}
	for _, item := range task.GroupDeliveries {
		if !allowed[item.TargetReference] || seen[item.TargetReference] {
			return ErrConflict
		}
		seen[item.TargetReference] = true
		if item.Status == nil {
			continue
		}
		expected := string(effectport.Hash("group-ops.wecom-delivery.v1", task.MessageID, task.SenderUserID, item.TargetReference, strconv.Itoa(*item.Status)))
		if item.EvidenceDigest != expected {
			return ErrConflict
		}
		if previous, ok := observed[item.TargetReference]; !ok || previous.Status != 1 {
			observed[item.TargetReference] = groupObservation{Status: *item.Status, Evidence: expected}
		}
	}
	allDelivered := true
	for _, target := range targets {
		value, ok := observed[target]
		if !ok || value.Status != 1 {
			allDelivered = false
		}
	}
	parts := []string{task.MessageID, task.SenderUserID}
	for _, target := range targets {
		if value, ok := observed[target]; ok {
			parts = append(parts, target, strconv.Itoa(value.Status))
		} else {
			parts = append(parts, target, "missing")
		}
	}
	evidence = string(effectport.Hash(append([]string{"group-ops.wecom-batch-delivery.v1"}, parts...)...))
	status := 0
	if allDelivered {
		status = 1
	}
	raw, err = json.Marshal(observed)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE group_ops_group_message_tasks SET delivery_observations=$3,delivery_status=$4,delivery_evidence_digest=CASE WHEN delivery_status=1 THEN delivery_evidence_digest ELSE $5 END,delivery_checked_at=clock_timestamp() WHERE execution_id=$1 AND external_effect_id=$2`, task.ExecutionID, effectID, raw, status, evidence)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE group_ops_executions SET delivery_proven=(delivery_proven OR $2),updated_at=clock_timestamp() WHERE id=$1 AND state='provider_accepted'`, task.ExecutionID, allDelivered)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
