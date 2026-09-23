package port

import (
	"context"
	"time"
)

// GovernanceReleaseEvidence is a narrow root-authenticated summary. It never
// contains credentials, process IDs or the original receipt body. Successful
// deployment observations cannot prove coverage of failed deployments.
type GovernanceReleaseFact struct {
	Sequence      int64     `json:"sequence"`
	ReleaseSHA    string    `json:"release_sha"`
	SucceededAt   time.Time `json:"succeeded_at"`
	ReceiptDigest string    `json:"receipt_digest"`
	Revoked       bool      `json:"revoked"`
}
type GovernanceReleaseEvidence struct {
	Version        int                     `json:"version"`
	GeneratedAt    time.Time               `json:"generated_at"`
	CurrentRelease string                  `json:"current_release"`
	Facts          []GovernanceReleaseFact `json:"facts"`
}
type GovernanceReleaseReader interface {
	ReadGovernanceReleases(context.Context) (GovernanceReleaseEvidence, error)
}
