package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpsSecretFileRejectsExposureAndDoesNotEcho(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook")
	secret := "private-webhook-value"
	if err := os.WriteFile(path, []byte(secret), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readOpsSecretFile(path); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("insecure credential accepted or exposed: %v", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := readOpsSecretFile(path); err != nil || value != secret {
		t.Fatal("protected credential unreadable")
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readOpsSecretFile(link); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestOpsNotificationRequiresExplicitInspectionEnablement(t *testing.T) {
	t.Setenv("AICRM_OPS_ENABLED", "false")
	t.Setenv("AICRM_OPS_RETENTION_ENABLED", "false")
	t.Setenv("AICRM_OPS_NOTIFICATION_ENABLED", "true")
	if _, err := loadOps(); err == nil {
		t.Fatal("notification enabled without inspection")
	}
	t.Setenv("AICRM_OPS_NOTIFICATION_ENABLED", "false")
	c, err := loadOps()
	if err != nil || c.Enabled || c.NotificationEnabled || c.WebhookURL != "" {
		t.Fatal("default unexpectedly enabled")
	}
}

func TestOpsRetentionCannotRunWithoutInspectionEvidence(t *testing.T) {
	t.Setenv("AICRM_OPS_ENABLED", "false")
	t.Setenv("AICRM_OPS_RETENTION_ENABLED", "true")
	t.Setenv("AICRM_OPS_NOTIFICATION_ENABLED", "false")
	if _, err := loadOps(); err == nil {
		t.Fatal("unobserved automatic cleanup enabled")
	}
}
