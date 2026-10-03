package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// Real authenticated Host regression: another package's newer runs cannot hide
// the selected package's older history; paging does not mutate/send anything.
func TestPostgreSQLAudienceSendHistoryChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newAudienceConfirmationChromiumFixture(t)
	insert := `INSERT INTO automation_runs(package_id,package_version,snapshot_id,agent_id,agent_published_version,binding_version,sender_set_version,preview_digest,state,target_count,skipped_count,created_by,created_at,updated_at) SELECT $1,1,1,1,1,1,1,decode(repeat('1a',32),'hex'),'completed',1,0,7,clock_timestamp(),clock_timestamp() FROM generate_series(1,$2::int)`
	for _, row := range [][2]int64{{fixture.packageID, 105}, {fixture.packageID + 100, 150}} {
		if _, err := fixture.application.pool.Native().Exec(fixture.ctx, insert, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "audience_confirmation_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_AUDIENCE_CONFIRMATION_TEST_URL="+fixture.server.URL,
		"AICRM_AUDIENCE_CONFIRMATION_TEST_USERNAME=groupops-browser-owner",
		"AICRM_AUDIENCE_CONFIRMATION_TEST_PASSWORD=groupops-browser-owner-password",
		"AICRM_AUDIENCE_CONFIRMATION_TEST_PACKAGE_ID="+strconv.FormatInt(fixture.packageID, 10),
		"AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR="+fixture.screenshots,
		"AICRM_AUDIENCE_TEST_SCENARIO=send-history",
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "audience_send_history_chromium: PASS") {
		t.Fatalf("send-history Chromium journey: %v\n%s", err, output)
	}
	var count int
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM automation_runs`).Scan(&count); err != nil || count != 255 {
		t.Fatalf("history readback mutated records count=%d err=%v", count, err)
	}
	for _, name := range []string{"audience-history-first.png", "audience-history-last.png", "audience-history-390.png"} {
		info, err := os.Stat(filepath.Join(fixture.screenshots, name))
		if err != nil || info.Size() < 512 {
			t.Fatalf("missing rendered evidence %s err=%v", name, err)
		}
	}
}
