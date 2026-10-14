package port

import (
	"encoding/json"
	"errors"
	"regexp"
)

// WeCom add_msg_template accepts at most 2000 exact group targets per task.
const MaxBatchTargetCount = 2000

var batchReference = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// FrozenExecutionTargets preserves historical single-group snapshots and
// exposes the complete immutable batch only for the versioned batch contract.
func FrozenExecutionTargets(content json.RawMessage, anchor string) ([]string, error) {
	var snapshot struct {
		Version int      `json:"schema_version"`
		Kind    string   `json:"kind"`
		Targets []string `json:"target_chat_references"`
	}
	if json.Unmarshal(content, &snapshot) != nil || !batchReference.MatchString(anchor) {
		return nil, errors.New("invalid frozen group targets")
	}
	if snapshot.Version != 3 {
		if snapshot.Targets != nil {
			return nil, errors.New("unexpected batch targets")
		}
		return []string{anchor}, nil
	}
	if snapshot.Kind != "webhook_message" || len(snapshot.Targets) == 0 || len(snapshot.Targets) > MaxBatchTargetCount || snapshot.Targets[0] != anchor {
		return nil, errors.New("invalid frozen batch targets")
	}
	for i, target := range snapshot.Targets {
		if !batchReference.MatchString(target) || (i > 0 && target <= snapshot.Targets[i-1]) {
			return nil, errors.New("invalid or repeated batch target")
		}
	}
	return append([]string(nil), snapshot.Targets...), nil
}

type GroupDeliveryStatus struct {
	TargetReference string `json:"target_reference"`
	Status          *int   `json:"status,omitempty"`
	DeliveryProven  bool   `json:"delivery_proven"`
	EvidenceDigest  string `json:"-"`
}
