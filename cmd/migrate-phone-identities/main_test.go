package main

import (
	"testing"
)

func TestNormalizePhoneUsesExplicitCNDefault(t *testing.T) {
	for input, want := range map[string]string{"13812345678": "13812345678", "+86 138-1234-5678": "13812345678"} {
		got, err := normalizePhone(input)
		if err != nil || got != want {
			t.Fatalf("normalizePhone(%q)=%q err=%v want=%q", input, got, err, want)
		}
	}
	if _, err := normalizePhone("+12025550123"); err == nil {
		t.Fatal("non-mainland phone accepted")
	}
	for _, input := range []string{"12345678", "8613812345678", "+0123", "not-a-phone"} {
		if got, err := normalizePhone(input); err == nil {
			t.Fatalf("normalizePhone(%q)=%q must fail", input, got)
		}
	}
}

func TestSyntaxClassifyAccountsForEveryInputRow(t *testing.T) {
	rows := []snapshotRow{
		{SchemaVersion: schemaVersion, SourceRowID: "row-1", CorpID: "corp", ExternalUserID: "ext-1", Phone: "13812345678", SourceUpdatedAt: "2026-09-01T00:00:00Z"},
		{SchemaVersion: schemaVersion, SourceRowID: "row-1", CorpID: "corp", ExternalUserID: "ext-1", Phone: "13812345678", SourceUpdatedAt: "2026-09-01T00:00:00Z"},
		{SchemaVersion: schemaVersion, SourceRowID: "", CorpID: "wrong", ExternalUserID: "", Phone: "bad"},
	}
	classified := syntaxClassify(rows, "corp")
	value := summarize(classified)
	if value.Input != 3 || value.DuplicateInput != 1 || value.Invalid != 1 || classified[0].outcome != "ready" {
		t.Fatalf("classified=%+v counts=%+v", classified, value)
	}
	if classified[1].receiptRowID == classified[0].receiptRowID || classified[2].receiptRowID == "" {
		t.Fatalf("receipt IDs must remain unique and non-empty")
	}
}
