package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPostgreSQLSubmissionSubjectMigrationPreservesLegacyFacts(t *testing.T) {
	f := newAttachmentFixture(t)
	ctx := context.Background()
	command := f.imageCommand(t, f.customer(t), "L", "IMG", 77)
	receipt, err := f.submissions.Submit(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-migration structure around a genuine completed command,
	// including encrypted answers, private images and one consumed code.
	if _, err = f.pool.Exec(ctx, `ALTER TABLE survey_submissions DROP COLUMN provider_identity_id`); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string][32]byte {
		t.Helper()
		out := map[string][32]byte{}
		for _, table := range []string{"survey_submissions", "survey_submission_claims", "survey_result_tokens", "survey_submission_answers", "survey_submission_attachments", "survey_redemption_codes", "media_private_images", "media_blobs", "survey_audit_events", "survey_outbox"} {
			var raw string
			if e := f.pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(f) - 'provider_identity_id' ORDER BY to_jsonb(f)::text),'[]'::jsonb)::text FROM `+table+` f`).Scan(&raw); e != nil {
				t.Fatal(e)
			}
			out[table] = sha256.Sum256([]byte(raw))
		}
		return out
	}
	before := snapshot()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0244_survey_submission_subject.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	for table, digest := range snapshot() {
		if digest != before[table] {
			t.Fatalf("additive migration changed retained %s facts", table)
		}
	}
	var legacyNull bool
	if err = f.pool.QueryRow(ctx, `SELECT provider_identity_id IS NULL FROM survey_submissions WHERE id=$1`, receipt.SubmissionID).Scan(&legacyNull); err != nil || !legacyNull {
		t.Fatalf("legacy subject was guessed: null=%t err=%v", legacyNull, err)
	}
	if got, e := f.submissions.Submit(ctx, command); e != nil || got != receipt {
		t.Fatalf("same-lineage legacy receipt lost: exact=%t err=%v", got == receipt, e)
	}
	assertImageCounts(t, f, 1, 2, 1)
	stored, err := f.submissions.GetSubmission(ctx, receipt.SubmissionID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Identity.ProviderIdentityID = 123 // Private DTO field, never public.
	raw, err := json.Marshal(stored)
	if err != nil || strings.Contains(string(raw), "provider_identity_id") || strings.Contains(string(raw), "ProviderIdentityID") {
		t.Fatalf("private handle leaked through Submission JSON: err=%v", err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE survey_submissions SET provider_identity_id=0 WHERE id=$1`, receipt.SubmissionID); err == nil {
		t.Fatal("nonpositive subject bypassed migration constraint")
	}
}
