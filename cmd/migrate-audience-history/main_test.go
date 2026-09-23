package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) snapshot {
	t.Helper()
	s := snapshot{Schema: schema, SourceSystem: "production-v2", CapturedAt: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), ScopeDeclaration: map[string]string{"unionid": "", "wecom_external_userid": ""}, Tables: map[string]table{}}
	data := map[string][]string{
		"ai_audience_package_group":   {`{"id":1,"name":"组"}`},
		"ai_audience_package":         {`{"id":2,"group_id":1,"current_version_id":3,"status":"active","natural_language_definition":"全部付费会员","daily_enabled":true,"lease_token":"historical-only"}`},
		"ai_audience_package_version": {`{"id":3,"package_id":2,"template_key":null,"parameters_json":{"large":9007199254740993},"snapshot_sql_text":"SELECT pg_sleep(100); DROP TABLE customers;","ai_prompt":"完整旧文本"}`},
		"ai_audience_member_current":  {`{"id":4,"package_id":2,"identity_type":"unionid","identity_value":"sensitive-member","unionid":"sensitive-member","status":"active","payload_json":{"raw":"source-only"}}`, `{"id":5,"package_id":2,"status":"exited","unionid":null}`},
	}
	for name, items := range data {
		tab := table{Rows: []row{}}
		for _, text := range items {
			b, m, e := canonicalFact([]byte(text))
			if e != nil {
				t.Fatal(e)
			}
			id, _ := integer(m, "id", true)
			tab.Rows = append(tab.Rows, row{ID: id, Digest: sum(b), Fact: b})
		}
		tab.Count = len(tab.Rows)
		raw, _ := json.Marshal(tab.Rows)
		tab.Digest = sum(raw)
		s.Tables[name] = tab
	}
	return s
}
func TestEncryptedCapturePreservesFactsWithoutExecutingOrExposingThem(t *testing.T) {
	s := fixture(t)
	if e := validate(s); e != nil {
		t.Fatal(e)
	}
	plain, _ := json.Marshal(s)
	key := bytes.Repeat([]byte{7}, 32)
	sealed, e := seal(plain, key)
	if e != nil {
		t.Fatal(e)
	}
	for _, sensitive := range [][]byte{[]byte("sensitive-member"), []byte("DROP TABLE"), []byte("完整旧文本")} {
		if bytes.Contains(sealed, sensitive) {
			t.Fatal("plaintext exposed")
		}
	}
	got, e := open(sealed, key)
	if e != nil || !bytes.Equal(plain, got) {
		t.Fatal("fact loss")
	}
	if !bytes.Contains(got, []byte("9007199254740993")) {
		t.Fatal("numeric precision lost")
	}
	report, _ := json.Marshal(summary(s, sum(plain)))
	for _, sensitive := range []string{"sensitive-member", "DROP TABLE", "historical-only", "source-only"} {
		if bytes.Contains(report, []byte(sensitive)) {
			t.Fatal("report exposes source")
		}
	}
	states := summary(s, sum(plain))["states"].(map[string]int)
	if states["members_active"] != 1 || states["members_exited"] != 1 || states["versions_template_null"] != 1 {
		t.Fatal(states)
	}
	sealed[len(sealed)-1] ^= 1
	if _, e = open(sealed, key); e == nil {
		t.Fatal("tampering accepted")
	}
	if _, e = open(sealed, bytes.Repeat([]byte{8}, 32)); e == nil {
		t.Fatal("wrong key accepted")
	}
}
func TestInspectRequiresExactProtectedDigestAndNoApply(t *testing.T) {
	d := t.TempDir()
	key := bytes.Repeat([]byte{7}, 32)
	kp := filepath.Join(d, "key")
	os.WriteFile(kp, []byte(base64.RawStdEncoding.EncodeToString(key)), 0600)
	s := fixture(t)
	plain, _ := json.Marshal(s)
	sealed, _ := seal(plain, key)
	p := filepath.Join(d, "snapshot")
	if e := writeExclusive(p, sealed); e != nil {
		t.Fatal(e)
	}
	if e := writeExclusive(p, sealed); e == nil {
		t.Fatal("overwrote snapshot")
	}
	if e := run([]string{"inspect", "--snapshot", p, "--snapshot-key-file", kp, "--expected-sha256", sum(plain)}); e != nil {
		t.Fatal(e)
	}
	if e := run([]string{"inspect", "--snapshot", p, "--snapshot-key-file", kp, "--expected-sha256", "wrong"}); e == nil {
		t.Fatal("digest ignored")
	}
	if e := run([]string{"apply", "--snapshot", p, "--snapshot-key-file", kp}); e == nil {
		t.Fatal("apply accepted")
	}
	os.Chmod(p, 0644)
	if _, e := readPrivate(p, 1<<20); e == nil {
		t.Fatal("public file accepted")
	}
	alias := filepath.Join(d, "alias")
	os.Symlink(kp, alias)
	if _, e := readKey(alias); e == nil {
		t.Fatal("symlink accepted")
	}
}
func TestValidationRejectsDriftAndCrossPackageVersion(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*snapshot)
	}{
		{"count", func(s *snapshot) { v := s.Tables[tableNames[0]]; v.Count++; s.Tables[tableNames[0]] = v }},
		{"row digest", func(s *snapshot) {
			v := s.Tables[tableNames[0]]
			v.Rows[0].Digest = "bad"
			b, _ := json.Marshal(v.Rows)
			v.Digest = sum(b)
			s.Tables[tableNames[0]] = v
		}},
		{"verified", func(s *snapshot) { s.ScopeVerified = true }},
		{"executable", func(s *snapshot) { s.Executable = true }},
		{"orphan", func(s *snapshot) {
			v := s.Tables["ai_audience_package_version"]
			v.Rows[0].Fact = json.RawMessage(`{"id":3,"package_id":999}`)
			v.Rows[0].Digest = sum(v.Rows[0].Fact)
			b, _ := json.Marshal(v.Rows)
			v.Digest = sum(b)
			s.Tables["ai_audience_package_version"] = v
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := fixture(t)
			c.mutate(&s)
			if validate(s) == nil {
				t.Fatal("unsafe snapshot accepted")
			}
		})
	}
}
