package config

import (
	"os"
	"strings"
)

func InvitationScreenshotDirectory() string {
	return strings.TrimSpace(os.Getenv("AICRM_INVITATION_SCREENSHOTS"))
}
