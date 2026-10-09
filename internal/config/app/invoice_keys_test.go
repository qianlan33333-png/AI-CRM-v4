package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvoiceKeysImmutablePrivateAndBounded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	f := InvoiceKeyFiles{Directory: dir}
	key := strings.Repeat("A", 32)
	id, e := f.StageInvoiceKey(context.Background(), key)
	if e != nil {
		t.Fatal(e)
	}
	if len(id) != 32 || strings.Contains(id, key) {
		t.Fatal("unsafe reference")
	}
	if got, e := f.Read(id); e != nil || got != key {
		t.Fatal("private readback failed")
	}
	for _, path := range []string{dir, filepath.Join(dir, id+".key")} {
		st, e := os.Lstat(path)
		if e != nil || st.Mode().Perm()&0077 != 0 {
			t.Fatal("insecure permissions")
		}
	}
	other, e := f.StageInvoiceKey(context.Background(), strings.Repeat("B", 32))
	if e != nil || other == id {
		t.Fatal("upload overwrote original")
	}
	if got, _ := f.Read(id); got != key {
		t.Fatal("original key changed")
	}
	for _, bad := range []string{"", "../" + id, strings.Repeat("f", 31), strings.Repeat("g", 32)} {
		if _, e = f.Read(bad); e == nil {
			t.Fatal("unsafe read accepted")
		}
	}
	if _, e = f.StageInvoiceKey(context.Background(), strings.Repeat("x", 31)); e == nil {
		t.Fatal("short key accepted")
	}
	if e = os.Chmod(filepath.Join(dir, id+".key"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Read(id); e == nil {
		t.Fatal("public key file accepted")
	}
	if e = os.Remove(filepath.Join(dir, id+".key")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(filepath.Join(dir, other+".key"), filepath.Join(dir, id+".key")); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Read(id); e == nil {
		t.Fatal("symlink accepted")
	}
}
