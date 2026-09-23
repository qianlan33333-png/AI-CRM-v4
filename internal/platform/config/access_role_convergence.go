package config

import "os"

// AccessRoleConvergenceApproved exposes the one-time release acknowledgement
// without letting migration commands read deployment environment directly.
func AccessRoleConvergenceApproved() bool {
	return os.Getenv("AICRM_ACCESS_CONVERGENCE_APPROVED") == "1"
}
