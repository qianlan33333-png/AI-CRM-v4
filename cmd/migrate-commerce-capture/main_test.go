package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncryptedCompleteCaptureRoundTripAndFailClosed(t *testing.T) {
	s := snapshot{Schema: envelope, Source: "aicrm-production", At: time.Now().UTC(), Tables: map[string]json.RawMessage{}, Counts: map[string]int{}, Digests: map[string]string{}}
	for _, table := range sourceTables {
		s.Tables[table] = json.RawMessage(`[]`)
		d := sha256.Sum256(s.Tables[table])
		s.Counts[table] = 0
		s.Digests[table] = hex.EncodeToString(d[:])
	}
	s.Tables["wechat_pay_orders"] = json.RawMessage(`[{"id":1,"unionid":"protected-value","name":"<test>&url","amount_total":990}]`)
	s.Counts["wechat_pay_orders"] = 1
	d := sha256.Sum256(s.Tables["wechat_pay_orders"])
	s.Digests["wechat_pay_orders"] = hex.EncodeToString(d[:])
	if e := validate(s); e != nil {
		t.Fatal(e)
	}
	plain, _ := json.Marshal(s)
	key := bytes.Repeat([]byte{1}, 32)
	sealed, e := seal(key, plain)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(sealed, []byte("protected-value")) {
		t.Fatal("raw source leaked into encrypted artifact")
	}
	decoded, e := open(key, sealed)
	if e != nil {
		t.Fatal(e)
	}
	var back snapshot
	if json.Unmarshal(decoded, &back) != nil || validate(back) != nil {
		t.Fatal("roundtrip changed source digests")
	}
	if !bytes.Equal(back.Tables["wechat_pay_orders"], s.Tables["wechat_pay_orders"]) {
		t.Fatal("original bytes not recovered")
	}
	sealed[len(sealed)-1] ^= 1
	if _, e = open(key, sealed); e == nil {
		t.Fatal("tampered snapshot accepted")
	}
	delete(back.Tables, "alipay_pay_orders")
	if validate(back) == nil {
		t.Fatal("missing table reported zero")
	}
	back = s
	back.Counts["wechat_pay_orders"] = 0
	if validate(back) == nil {
		t.Fatal("count mismatch accepted")
	}
}
func TestProtectedEvidenceRefusesOverwriteAndPublicKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.enc")
	if e := writeExclusive(path, []byte("sealed")); e != nil {
		t.Fatal(e)
	}
	if e := writeExclusive(path, []byte("replace")); e == nil {
		t.Fatal("frozen evidence overwritten")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("evidence permissions")
	}
	keyPath := filepath.Join(dir, "key")
	os.WriteFile(keyPath, []byte("public"), 0644)
	if _, e := readKey(keyPath); e == nil {
		t.Fatal("public key file accepted")
	}
}
