package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoricalQuestionnaireAuditParametersHaveConcretePostgreSQLTypes(t *testing.T) {
	for _, want := range []string{"$2::boolean", "$3::timestamptz"} {
		if !strings.Contains(historicalQuestionnaireAuditSQL, want) {
			t.Fatalf("audit SQL is missing explicit parameter cast %s", want)
		}
	}
}

func TestEncryptedSnapshotAuthenticatesAndRejectsTampering(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"manifest":{"source_system":"test"}}`)
	sealed, err := encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := decrypt(key, sealed)
	if err != nil || !bytes.Equal(opened, plain) {
		t.Fatalf("roundtrip err=%v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err = decrypt(key, sealed); err == nil {
		t.Fatal("tampered snapshot accepted")
	}
}

func TestReadKeyRequiresOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	value := make([]byte, 32)
	if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(value)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readKey(path); err == nil {
		t.Fatal("group-readable key accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if key, err := readKey(path); err != nil || len(key) != 32 {
		t.Fatalf("key err=%v", err)
	}
}

func TestValidateSnapshotRejectsTypedTableWithoutPanicking(t *testing.T) {
	snapshot := frozenSurveySnapshot(t, time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC))
	setFrozenTable(t, &snapshot, "questionnaire_questions", []map[string]any{{"id": "not-an-integer"}})
	if err := validateSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "invalid frozen table questionnaire_questions") {
		t.Fatalf("typed table validation err=%v", err)
	}
}

func TestAssessmentBusinessKeyPreservesLegacyValuesOnlyInItsLocalScope(t *testing.T) {
	for _, key := range []string{"维度 1/增长", "暖男/女型", "dimension key"} {
		if !validAssessmentBusinessKey(key) {
			t.Fatalf("valid legacy key rejected: %q", key)
		}
	}
	for _, key := range []string{" key", "key ", "key\nline", strings.Repeat("字", 129)} {
		if validAssessmentBusinessKey(key) {
			t.Fatalf("unsafe key accepted: %q", key)
		}
	}
	if !validAssessmentBusinessKey("") {
		t.Fatal("legacy empty key rejected")
	}
	if validAssessmentBusinessKey(string([]byte{0xff})) {
		t.Fatal("invalid UTF-8 key accepted")
	}
	if got := safeOpaque("暖男/女型"); got != "" {
		t.Fatalf("generic opaque contract widened: %q", got)
	}
}

func TestValidateSnapshotRejectsInvalidAssessmentBusinessKeyWithoutChangingIt(t *testing.T) {
	snapshot := frozenSurveySnapshot(t, time.Date(2026, 9, 5, 8, 30, 0, 0, time.UTC))
	var questions []question
	decodeTable(snapshot, "questionnaire_questions", &questions)
	questions[0].Dimension = " bad"
	setFrozenTable(t, &snapshot, "questionnaire_questions", questions)
	if err := validateSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "invalid assessment business key questionnaire_questions/10") {
		t.Fatalf("invalid assessment key snapshot err=%v", err)
	}
	var options []option
	decodeTable(snapshot, "questionnaire_options", &options)
	options[0].TypeKey = "bad\nkey"
	setFrozenTable(t, &snapshot, "questionnaire_options", options)
	if err := validateSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "invalid assessment business key questionnaire_questions/10") {
		t.Fatalf("question key validation should remain first err=%v", err)
	}
	questions[0].Dimension = ""
	setFrozenTable(t, &snapshot, "questionnaire_questions", questions)
	if err := validateSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "invalid assessment business key questionnaire_options/20") {
		t.Fatalf("invalid assessment type snapshot err=%v", err)
	}
}

func TestDefinitionDigestMatchesHistoricalNilChildren(t *testing.T) {
	q := questionnaire{ID: 14}
	index := &frozenSourceIndex{}
	want := recordDigest(struct {
		Q         questionnaire
		Questions []question
		Rules     []rule
	}{Q: q})
	if got := index.definitionDigest(q); got != want {
		t.Fatal("empty definition differs from importer nil map entries")
	}
}
