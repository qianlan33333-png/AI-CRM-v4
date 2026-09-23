package config

import (
	"errors"
	"os"
	"strings"
)

// Ops contains deployment-only secrets. Never serialize this configuration.
type Ops struct {
	Enabled             bool
	RetentionEnabled    bool
	NotificationEnabled bool
	TargetRef           string
	WebhookURL          string
	SigningSecret       string
}

func loadOps() (Ops, error) {
	var c Ops
	var err error
	if c.Enabled, err = strictBool("AICRM_OPS_ENABLED", false); err != nil {
		return c, err
	}
	if c.RetentionEnabled, err = strictBool("AICRM_OPS_RETENTION_ENABLED", false); err != nil {
		return c, err
	}
	if c.NotificationEnabled, err = strictBool("AICRM_OPS_NOTIFICATION_ENABLED", false); err != nil {
		return c, err
	}
	if c.RetentionEnabled && !c.Enabled {
		return c, errors.New("ops retention requires inspections")
	}
	c.TargetRef = valueOrDefault("AICRM_OPS_TARGET_REF", "original-ops-group")
	// A runtime-owned, permission-restricted file avoids secrets in shell arguments,
	// reports, effect envelopes and environment dumps.
	if c.NotificationEnabled {
		if !c.Enabled {
			return c, errors.New("ops notifications require inspections")
		}
		c.WebhookURL, err = readOpsSecretFile(os.Getenv("AICRM_OPS_WEBHOOK_FILE"))
		if err != nil {
			return Ops{}, err
		}
		if path := os.Getenv("AICRM_OPS_SIGNING_SECRET_FILE"); path != "" {
			c.SigningSecret, err = readOpsSecretFile(path)
			if err != nil {
				return Ops{}, err
			}
		}
	}
	return c, nil
}

func readOpsSecretFile(path string) (string, error) {
	const message = "ops credential file unavailable or insecure"
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return "", errors.New(message)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New(message)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New(message)
	}
	return value, nil
}
